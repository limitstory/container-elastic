package modules

import (
	"fmt"
	"os/exec"
	"time"

	global "elastic/modules/global"

	internalapi "k8s.io/cri-api/pkg/apis"
)

func DecisionRemoveContainer(
	client internalapi.RuntimeService, scaleUpCandidateList []global.ScaleCandidateContainer, pauseContainerList []global.PauseContainer,
	checkPointContainerList []global.CheckpointContainer, currentRunningPods []string, lenghOfCurrentRunningPods int, priorityMap map[string]global.PriorityContainer,
	removeContainerList []global.CheckpointContainer, removeContainerToChan chan global.CheckpointContainer) ([]global.ScaleCandidateContainer, []global.PauseContainer, []global.CheckpointContainer, []string) {

	var removeCandidateContainerList []global.CheckpointContainer
	var toRemovePauseContainer []int
	var toRemovescaleUpCandidateList []int

	// When the memory usage of the container is very high
	for i := 0; i < len(pauseContainerList); i++ {
		for j := 0; j < len(checkPointContainerList); j++ {
			if pauseContainerList[i].PodName == checkPointContainerList[j].PodName {
				if checkPointContainerList[j].IsCheckpoint {
					res := checkPointContainerList[j].ContainerData.Resource
					if res[len(res)-1].ConMemUtil > global.CONTAINER_MEMORY_USAGE_THRESHOLD {
						removeCandidateContainerList = append(removeCandidateContainerList, checkPointContainerList[j])
						toRemovePauseContainer = append(toRemovePauseContainer, i)
						for k := 0; k < len(scaleUpCandidateList); k++ {
							if scaleUpCandidateList[k].PodName == pauseContainerList[i].PodName {
								toRemovescaleUpCandidateList = append(toRemovescaleUpCandidateList, k)
								break
							}
						}
					}
				}
				break
			}
		}
	}

	for i := len(toRemovePauseContainer) - 1; i >= 0; i-- {
		idx := toRemovePauseContainer[i]
		if idx < len(pauseContainerList) {
			pauseContainerList = append(pauseContainerList[:idx], pauseContainerList[idx+1:]...)
		} else {
			// idx가 슬라이스 범위를 벗어나는 경우, 마지막 요소를 제거
			pauseContainerList = pauseContainerList[:idx]
		}
	}

	for i := len(toRemovescaleUpCandidateList) - 1; i >= 0; i-- {
		idx := toRemovescaleUpCandidateList[i]
		if idx < len(scaleUpCandidateList) {
			scaleUpCandidateList = append(scaleUpCandidateList[:idx], scaleUpCandidateList[idx+1:]...)
		} else {
			// idx가 슬라이스 범위를 벗어나는 경우, 마지막 요소를 제거
			scaleUpCandidateList = scaleUpCandidateList[:idx]
		}
	}

	// Kill memory-pressure containers
	for _, removeCandidateContainer := range removeCandidateContainerList {
		RemoveContainer(client, removeCandidateContainer.PodName)
		for i := 0; i < len(currentRunningPods); i++ {
			if currentRunningPods[i] == removeCandidateContainer.PodName {
				currentRunningPods = append(currentRunningPods[:i], currentRunningPods[i+1:]...)
				break
			}
		}
		// move from removeCandidateContainerList to removeContainerList
		removeCandidateContainer.CheckpointData.RemoveStartTime = time.Now().Unix()
		removeCandidateContainer.ContainerData.NumOfRemove++
		if removeCandidateContainer.ContainerData.Attempt > 0 {
			removeCandidateContainer.ContainerData.PastAttempt = removeCandidateContainer.ContainerData.Attempt
		}
		removeContainerToChan <- removeCandidateContainer
	}

	// Deadlock과 유사한 상태에 빠진 경우
	lenghOfCurrentRunningPods = lenghOfCurrentRunningPods - len(removeCandidateContainerList)
	removeCandidateContainerList = nil
	var offset = lenghOfCurrentRunningPods / 2

	// checkpoint 중인 컨테이너가 있으면 삭제하면 안되며, checkpoint 중인 컨테이너만 있으면 동작을 중지해야 한다.
	//if !(pauseContainerList[i].IsCheckpoint) {
	//	break
	//}

	pauseContainerListSize := len(pauseContainerList)

	for pauseContainerListSize > offset {

		var lowestPriority float64 = 9999999999
		var lowestPriorityIndex_i int = -1
		var lowestPriorityIndex_j int = -1

		notCheckpointed := 0

		// count the number of uncheckpointed containers
		for i := 0; i < pauseContainerListSize; i++ {
			for j := 0; j < len(checkPointContainerList); j++ {
				if pauseContainerList[i].PodName == checkPointContainerList[j].PodName {
					if !(checkPointContainerList[j].IsCheckpoint) {
						notCheckpointed++
					}
				}
			}
		}
		// break out of the loop if there are only uncheckpointed containers
		if notCheckpointed == pauseContainerListSize {
			break
		}

		for i := 0; i < pauseContainerListSize; i++ {
			for j := 0; j < len(checkPointContainerList); j++ {
				if pauseContainerList[i].PodName == checkPointContainerList[j].PodName {
					if !(checkPointContainerList[j].IsCheckpoint) {
						continue
					}
					if lowestPriority > priorityMap[pauseContainerList[i].PodId].Priority {
						lowestPriority = priorityMap[pauseContainerList[i].PodId].Priority
						lowestPriorityIndex_i = i
						lowestPriorityIndex_j = j
					}
				}
			}
		}
		if lowestPriorityIndex_i != -1 {
			removeCandidateContainerList = append(removeCandidateContainerList, checkPointContainerList[lowestPriorityIndex_j])
			for k := 0; k < len(scaleUpCandidateList); k++ {
				if scaleUpCandidateList[k].PodName == pauseContainerList[lowestPriorityIndex_i].PodName {
					scaleUpCandidateList = append(scaleUpCandidateList[:k], scaleUpCandidateList[k+1:]...)
					break
				}
			}
			// 여기서 pauseContainerList에서 삭제됨
			pauseContainerList = append(pauseContainerList[:lowestPriorityIndex_i], pauseContainerList[lowestPriorityIndex_i+1:]...)
		}
		pauseContainerListSize--
	}

	// Kill low-priority containers
	for _, removeCandidateContainer := range removeCandidateContainerList {
		RemoveContainer(client, removeCandidateContainer.PodName)
		// delete removeContainer from currentRunningPods
		for i := 0; i < len(currentRunningPods); i++ {
			if currentRunningPods[i] == removeCandidateContainer.PodName {
				currentRunningPods = append(currentRunningPods[:i], currentRunningPods[i+1:]...)
				break
			}
		}
		// append to removeContainerList
		removeCandidateContainer.CheckpointData.RemoveStartTime = time.Now().Unix()
		removeCandidateContainer.ContainerData.NumOfRemove++
		if removeCandidateContainer.ContainerData.Attempt > 0 {
			removeCandidateContainer.ContainerData.PastAttempt = removeCandidateContainer.ContainerData.Attempt
		}
		removeContainerToChan <- removeCandidateContainer
	}

	return scaleUpCandidateList, pauseContainerList, checkPointContainerList, currentRunningPods
}

func RemoveContainer(client internalapi.RuntimeService, podName string) {

	command := "sudo crictl ps | grep " + podName + " | awk '{print $1}' | xargs sudo crictl stop && kubectl delete pods " + podName + " --grace-period=0 --force"

	for {
		output, err := exec.Command("bash", "-c", command).Output()

		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Println((string(output)))
			break
		}
	}
}

func RemoveRestartedRepairContainer(client internalapi.RuntimeService, podName string) {

	command := "kubectl delete pods " + podName + " --grace-period=0 --force"

	for {
		output, err := exec.Command("bash", "-c", command).Output()

		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Println((string(output)))
			break
		}
	}
}
