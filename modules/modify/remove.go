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
	currentRunningPods []string, lenghOfCurrentRunningPods int, priorityMap map[string]global.PriorityContainer,
	removeContainerList []global.PauseContainer) ([]global.ScaleCandidateContainer, []global.PauseContainer, []global.PauseContainer, []string) {

	var removeCandidateContainerList []global.PauseContainer

	// When the memory usage of the container is very high
	for i := 0; i < len(pauseContainerList); i++ {
		if !(pauseContainerList[i].IsCheckpoint) {
			continue
		}

		res := pauseContainerList[i].ContainerData.Resource
		if pauseContainerList[i].IsCheckpoint && res[len(res)-1].ConMemUtil > global.CONTAINER_MEMORY_USAGE_THRESHOLD {
			removeCandidateContainerList = append(removeCandidateContainerList, pauseContainerList[i])
			for j := 0; j < len(scaleUpCandidateList); j++ {
				if scaleUpCandidateList[j].PodName == pauseContainerList[i].PodName {
					scaleUpCandidateList = append(scaleUpCandidateList[:j], scaleUpCandidateList[j+1:]...)
					break
				}
			}
			pauseContainerList = append(pauseContainerList[:i], pauseContainerList[i+1:]...)
			i--
		}
	}

	// Kill memory-pressure containers
	for _, removeCandidateContainer := range removeCandidateContainerList {
		RemoveContainer(client, removeCandidateContainer.PodName)
		// move from removeCandidateContainerList to removeContainerList
		removeCandidateContainer.CheckpointData.RemoveStartTime = time.Now().Unix()
		removeCandidateContainer.ContainerData.NumOfRemove++
		removeContainerList = append(removeContainerList, removeCandidateContainer)
	}

	// Deadlock과 유사한 상태에 빠진 경우
	lenghOfCurrentRunningPods = lenghOfCurrentRunningPods - len(removeCandidateContainerList)
	removeCandidateContainerList = nil
	var offset = lenghOfCurrentRunningPods / 2

	// checkpoint 중인 컨테이너가 있으면 삭제하면 안되며, checkpoint 중인 컨테이너만 있으면 동작을 중지해야 한다.
	//if !(pauseContainerList[i].IsCheckpoint) {
	//	break
	//}

	for len(pauseContainerList) > offset {
		var lowestPriority float64 = 9999999999
		var lowestPriorityIndex int

		listSize := len(pauseContainerList)
		notCheckpointed := 0

		// count the number of uncheckpointed containers
		for i := 0; i < listSize; i++ {
			if !(pauseContainerList[i].IsCheckpoint) {
				notCheckpointed++
			}
		}
		// break out of the loop if there are only uncheckpointed containers
		if notCheckpointed == listSize {
			break
		}

		for j := 0; j < listSize; j++ {
			if !(pauseContainerList[j].IsCheckpoint) {
				continue
			}
			if lowestPriority > priorityMap[pauseContainerList[j].PodId].Priority {
				lowestPriorityIndex = j
			}
		}
		removeCandidateContainerList = append(removeCandidateContainerList, pauseContainerList[lowestPriorityIndex])

		for j := 0; j < len(scaleUpCandidateList); j++ {
			if scaleUpCandidateList[j].PodName == pauseContainerList[lowestPriorityIndex].PodName {
				scaleUpCandidateList = append(scaleUpCandidateList[:j], scaleUpCandidateList[j+1:]...)
				break
			}
		}
		pauseContainerList = append(pauseContainerList[:lowestPriorityIndex], pauseContainerList[lowestPriorityIndex+1:]...)
	}

	// Kill low-priority containers
	for _, removeCandidateContainer := range removeCandidateContainerList {
		fmt.Println("Trigger!!2")
		RemoveContainer(client, removeCandidateContainer.PodName)
		// delete removeContainer from currentRunningPods
		for i := 0; i < len(currentRunningPods); i++ {
			if currentRunningPods[i] == removeCandidateContainer.PodName {
				currentRunningPods = append(currentRunningPods[:i], currentRunningPods[i+1:]...)
			}
			// move from pauseContainerList to removeContainerList
			for i := 0; i < len(pauseContainerList); i++ {
				if pauseContainerList[i].PodName == removeCandidateContainer.PodName {
					pauseContainerList[i].CheckpointData.RemoveStartTime = time.Now().Unix()
					pauseContainerList[i].ContainerData.NumOfRemove++
					removeContainerList = append(removeContainerList, pauseContainerList[i])
				}
			}
		}
	}

	return scaleUpCandidateList, pauseContainerList, removeContainerList, currentRunningPods
}

func RemoveContainer(client internalapi.RuntimeService, podName string) {
	command := "kubectl delete pods " + podName + " --grace-period=0 --force"
	_, err := exec.Command("bash", "-c", command).Output()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(podName, " is deleted.")
}
