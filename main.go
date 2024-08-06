package main

import (
	"context"
	"fmt"
	"time"

	mod "elastic/modules"
	cp "elastic/modules/checkpoint"
	global "elastic/modules/global"
	scale "elastic/modules/scale"

	remote "k8s.io/kubernetes/pkg/kubelet/cri/remote"
)

func main() {
	const ENDPOINT string = "unix:///var/run/crio/crio.sock"

	podIndex := make(map[string]int64)
	var currentRunningPods []string

	var podInfoSet []global.PodData
	var systemInfoSet []global.SystemInfo
	var scaleUpCandidateList []global.ScaleCandidateContainer
	var pauseContainerList []global.PauseContainer
	var checkpointContainerList []global.CheckpointContainer
	var removeContainerList []global.CheckpointContainer

	var avgCheckpointTime []global.CheckpointTime
	var avgRepairTime []global.RepairTime
	var avgRemoveTime []global.RemoveTime

	appendCheckpointContainerToChan := make(chan global.CheckpointContainer, 100)
	modifyCheckpointContainerToChan := make(chan global.CheckpointContainer, 100)
	removeContainerToChan := make(chan global.CheckpointContainer, 100)
	repairCandidateToChan := make(chan global.CheckpointContainer, 100)

	// 동시에 실행할 고루틴 수를 제한하는 채널(세마포어 역할)
	semaphore := make(chan struct{}, 3) // 최대 3개의 고루틴만 동시에 실행

	priorityMap := make(map[string]global.PriorityContainer)
	var sortPriority []string

	/*
		// kubernetes api 클라이언트 생성하는 모듈
		clientset := mod.InitClient()
		if clientset != nil {
			fmt.Println("123")
		}*/

	//get new internal client service
	client, err := remote.NewRemoteRuntimeService(ENDPOINT, time.Second*2, nil)
	if err != nil {
		panic(err)
	}

	// execute monitoring & resource management logic
	for {

		// definition of data structure to store
		//var selectContainerId = make([]string, 0)
		//var selectContainerResource = make([]*pb.ContainerResources, 0)

		// get system metrics
		// 자원 변경 시 TimeWindow Reset 수행하면서 다른 리소스 자원 기록도 전부 날리는 것으로....
		systemInfoSet = mod.GetSystemStatsInfo(systemInfoSet)

		podInfoSet, currentRunningPods = mod.MonitoringPodResources(client, podIndex, podInfoSet, currentRunningPods, systemInfoSet,
			checkpointContainerList, removeContainerList, avgCheckpointTime, avgRepairTime, avgRemoveTime, removeContainerToChan)

		/*
			for i := 0; i < len(currentRunningPods); i++ {
				res := podInfoSet[podIndex[currentRunningPods[i]]].Container[0].Resource
				// main.go 63라인에서 뻗으므로 예외처리 필요함
				if len(res) == 0 {
					continue
				}
				fmt.Println(podInfoSet[podIndex[currentRunningPods[i]]].Name)
				fmt.Println(podInfoSet[podIndex[currentRunningPods[i]]].Container[0].Cgroup.CpuQuota)
				fmt.Println(res[len(res)-1].ConMemUtil)
				fmt.Println(res[len(res)-1].MemoryUsageBytes)
				fmt.Println(podInfoSet[podIndex[currentRunningPods[i]]].Container[0].Cgroup.MemoryLimitInBytes)

				//fmt.Println(res[len(res)-1].CpuUtil)
				//fmt.Println()
			}
		*/

		podInfoSet = mod.GetPriorityMetric(podIndex, podInfoSet, currentRunningPods, systemInfoSet)
		podInfoSet, priorityMap, sortPriority = mod.CalculatePriority(podIndex, podInfoSet, currentRunningPods)

		if false {
			fmt.Println(sortPriority)
		}

		podInfoSet = scale.DecisionScaleDown(client, podIndex, podInfoSet, currentRunningPods, systemInfoSet)
		podInfoSet, scaleUpCandidateList, pauseContainerList = scale.DecisionScaleUp(client, podIndex, podInfoSet, currentRunningPods,
			systemInfoSet, priorityMap, scaleUpCandidateList, pauseContainerList)

		go func() {
			for {
				select {
				case container1 := <-appendCheckpointContainerToChan:
					found := false
					for i, checkpointContainer := range checkpointContainerList {
						if container1.PodName == checkpointContainer.PodName {
							found = true
							if container1.Timestamp >= checkpointContainer.Timestamp+global.RECHECKPOINT_THRESHOLD {
								checkpointContainerList[i] = container1
								fmt.Println("Modify timestamp: ", checkpointContainerList[i])
							}
						}
					}
					if !found {
						checkpointContainerList = append(checkpointContainerList, container1)
						//fmt.Println("Appended container: ", container1)
					}

				case container2 := <-modifyCheckpointContainerToChan:
					for i, checkpointContainer := range checkpointContainerList {
						if container2.PodName == checkpointContainer.PodName {
							checkpointContainerList[i] = container2
							//fmt.Println("Data: ", checkpointContainerList[i])

							if container2.IsCheckpoint {
								time.Sleep(time.Second / 2)
								stats, _ := client.PodSandboxStats(context.TODO(), container2.PodId)
								if stats == nil || len(stats.Linux.Containers) == 0 {
									container2.IsCheckpoint = false
									container2.AbortedCheckpoint = true
									fmt.Println("checkpoint aborted: ", container2.PodName)
									checkpointContainerList = append(checkpointContainerList[:i], checkpointContainerList[i+1:]...)
									break
								} else {
									fmt.Println("checkpoint complete: ", container2.PodName)
									checkpointContainerList[i].EndCheckpointTime = time.Now().Unix()
									var podCheckpointTime global.CheckpointTime
									podCheckpointTime.PodName = container2.PodName
									podCheckpointTime.CheckpointTime = checkpointContainerList[i].EndCheckpointTime - checkpointContainerList[i].StartCheckpointTime
									avgCheckpointTime = append(avgCheckpointTime, podCheckpointTime)
									for j, pauseContainer := range pauseContainerList {
										if container2.PodName == pauseContainer.PodName {
											pauseContainerList[j].IsCheckpoint = true
											break
										}
									}
								}
							}
							break
						}
					}
				}
			}
		}()
		go cp.DecisionCheckpoint(appendCheckpointContainerToChan, modifyCheckpointContainerToChan, pauseContainerList, checkpointContainerList, semaphore)

		//go
		scaleUpCandidateList, pauseContainerList, checkpointContainerList, currentRunningPods = mod.DecisionRemoveContainer(client,
			scaleUpCandidateList, pauseContainerList, checkpointContainerList, currentRunningPods, len(currentRunningPods), priorityMap, removeContainerList, removeContainerToChan)

		// remove container 모두 채널로 통신가능하도록 변경해야 할듯
		go func() {
			for {
				select {
				case container1 := <-removeContainerToChan:
					container1.StartRemoveTime = time.Now().Unix()
					removeContainerList = append(removeContainerList, container1)
				case container2 := <-repairCandidateToChan:
					for i, removeContainer := range removeContainerList {
						if container2.PodName == removeContainer.PodName && (container2.DuringCreateContainer || container2.CreatingContainer) {
							removeContainerList[i] = container2
							break
						} else if container2.PodName == removeContainer.PodName && container2.CreateContainer {
							var podRemoveTime global.RemoveTime
							var podRepairTime global.RepairTime

							podRemoveTime.PodName = container2.PodName
							podRemoveTime.RemoveTime = time.Now().Unix() - container2.StartRemoveTime
							avgRemoveTime = append(avgRemoveTime, podRemoveTime)

							podRepairTime.PodName = container2.PodName
							podRepairTime.RepairTime = container2.EndRepairTime - container2.StartRepairTime
							avgRepairTime = append(avgRepairTime, podRepairTime)
							removeContainerList = append(removeContainerList[:i], removeContainerList[i+1:]...)
							break
						}
					}
				}
			}
		}()
		go mod.DecisionRepairContainer(repairCandidateToChan, client, systemInfoSet, podIndex, podInfoSet, currentRunningPods,
			len(currentRunningPods), priorityMap, removeContainerList)

		//modify.DecisionRepairContainer()
		// cp.MakeContainerFromCheckpoint(checkpointName, "nginx")
		// cp.RestoreContainer("nginx")

		//selectContainerId, selectContainerResource = mod.LimitContainerResources(client, selectContainerId, selectContainerResource)

		// After limiting CPU usage, watch the trend of memory usage.
		//mod.ControlRecursiveContainerResources(client, selectContainerId, selectContainerResource)

		var scaleUpCandidateNameList []string
		var pauseContainerNameList []string
		var checkPointContainerNameList []string
		var removeContainerNameList []string

		for _, scaleUpCandiate := range scaleUpCandidateList {
			scaleUpCandidateNameList = append(scaleUpCandidateNameList, scaleUpCandiate.PodName)
		}
		for _, pauseContainer := range pauseContainerList {
			pauseContainerNameList = append(pauseContainerNameList, pauseContainer.PodName)
		}
		for _, checkPointContainer := range checkpointContainerList {
			checkPointContainerNameList = append(checkPointContainerNameList, checkPointContainer.PodName)
		}
		for _, removeContainer := range removeContainerList {
			removeContainerNameList = append(removeContainerNameList, removeContainer.PodName)
		}

		fmt.Println("scaleUpCandidateList: ", scaleUpCandidateNameList)
		fmt.Println("pauseContainerList: ", pauseContainerNameList)
		fmt.Println("checkPointContainerList: ", checkPointContainerNameList)
		fmt.Println("removeContainerList: ", removeContainerNameList)
		fmt.Println("=====================================================")
		fmt.Println()

		time.Sleep(time.Second)
	}
}

/*
func IsPodRunning(container global.CheckpointContainer, currentRunningPods []string) bool {
	for _, pods := range currentRunningPods {
		if container.PodName == pods {
			return true
		}
	}

	return false
}*/
