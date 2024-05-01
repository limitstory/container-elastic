package main

import (
	"fmt"
	"time"

	mod "elastic/modules"
	cp "elastic/modules/checkpoint"
	global "elastic/modules/global"
	modify "elastic/modules/modify"
	scale "elastic/modules/scale"

	remote "k8s.io/kubernetes/pkg/kubelet/cri/remote"
)

func main() {
	const ENDPOINT string = "unix:///var/run/crio/crio.sock"
	const DEFAULT_CPU_QUOTA int64 = 20000

	podIndex := make(map[string]int64)
	var podInfoSet []global.PodData
	var systemInfoSet []global.SystemInfo
	var scaleUpCandidateList []global.ScaleCandidateContainer
	var pauseContainerList []global.PauseContainer
	var removeContainerList []global.PauseContainer

	pauseContainerToChan := make(chan global.PauseContainer, 100)
	repairCandidateToChan := make(chan global.PauseContainer, 100)

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
		var currentRunningPods []string

		// definition of data structure to store
		//var selectContainerId = make([]string, 0)
		//var selectContainerResource = make([]*pb.ContainerResources, 0)

		// get system metrics
		// 자원 변경 시 TimeWindow Reset 수행하면서 다른 리소스 자원 기록도 전부 날리는 것으로....
		systemInfoSet = mod.GetSystemStatsInfo(systemInfoSet)

		podInfoSet, currentRunningPods = mod.MonitoringPodResources(client, podIndex, podInfoSet, currentRunningPods, systemInfoSet)

		for i := 0; i < len(currentRunningPods); i++ {
			if len(podInfoSet[i].Container) == 0 {
				continue
			}
			res := podInfoSet[i].Container[0].Resource
			// main.go 63라인에서 뻗으므로 예외처리 필요함
			if len(res) == 0 {
				continue
			}
			fmt.Println(podInfoSet[i].Name)
			fmt.Println(podInfoSet[i].Container[0].Cgroup.CpuQuota)
			fmt.Println(res[len(res)-1].ConMemUtil)
			fmt.Println(res[len(res)-1].MemoryUsageBytes)
			fmt.Println(podInfoSet[i].Container[0].Cgroup.MemoryLimitInBytes)

			//fmt.Println(res[len(res)-1].CpuUtil)
			//fmt.Println()
		}

		podInfoSet = mod.GetPriorityMetric(podIndex, podInfoSet, currentRunningPods, systemInfoSet)
		podInfoSet, priorityMap, sortPriority = mod.CalculatePriority(podIndex, podInfoSet, currentRunningPods)

		if false {
			fmt.Println(sortPriority)
		}

		podInfoSet = scale.DecisionScaleDown(client, podIndex, podInfoSet, currentRunningPods, systemInfoSet)
		podInfoSet, scaleUpCandidateList, pauseContainerList = scale.DecisionScaleUp(client, podIndex, podInfoSet, currentRunningPods,
			systemInfoSet, priorityMap, scaleUpCandidateList, pauseContainerList)

	EmptyChannel:
		for {
			select {
			case container := <-pauseContainerToChan:
				for i, pauseContainer := range pauseContainerList {
					if container.PodName == pauseContainer.PodName {
						pauseContainerList[i] = container
						//fmt.Println(pauseContainer.PodName + " 갱신됨.....")
					}
				}
			default:
				break EmptyChannel
			}
		}
		go cp.DecisionCheckpoint(pauseContainerToChan, pauseContainerList)

		scaleUpCandidateList, pauseContainerList, removeContainerList, currentRunningPods = modify.DecisionRemoveContainer(client,
			scaleUpCandidateList, pauseContainerList, currentRunningPods, len(currentRunningPods), priorityMap, removeContainerList)

	EmptyChannel2:
		for {
			select {
			case container := <-repairCandidateToChan:
				for i, removeContainer := range removeContainerList {
					if container.PodName == removeContainer.PodName {
						removeContainerList = append(removeContainerList[:i], removeContainerList[i+1:]...)
					}
				}
			default:
				break EmptyChannel2
			}
		}
		go modify.DecisionRepairContainer(repairCandidateToChan, client, systemInfoSet, podIndex, podInfoSet, currentRunningPods,
			len(currentRunningPods), priorityMap, removeContainerList)

		//modify.DecisionRepairContainer()
		// cp.MakeContainerFromCheckpoint(checkpointName, "nginx")
		// cp.RestoreContainer("nginx")

		//selectContainerId, selectContainerResource = mod.LimitContainerResources(client, selectContainerId, selectContainerResource)

		// After limiting CPU usage, watch the trend of memory usage.
		//mod.ControlRecursiveContainerResources(client, selectContainerId, selectContainerResource)

		fmt.Println("scaleUpCandidateList: ", scaleUpCandidateList)
		fmt.Println("pauseContainerList: ", pauseContainerList)
		fmt.Println("removeContainerList: ", removeContainerList)

		time.Sleep(time.Second)
	}
}
