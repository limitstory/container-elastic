package modules

import (
	"fmt"
	"time"

	internalapi "k8s.io/cri-api/pkg/apis"

	mod "elastic/modules"
	global "elastic/modules/global"
)

func CheckToPauseContainer(container global.ScaleCandidateContainer, pauseContainerList []global.PauseContainer) bool {
	for _, pauseContainer := range pauseContainerList {
		if pauseContainer.PodName == container.PodName {
			return false
		}
	}
	return true
}

func AppendPauseContainerList(pauseContainerList []global.PauseContainer, containerList []global.ScaleCandidateContainer) []global.PauseContainer {

	for _, container := range containerList {
		var pauseContainer global.PauseContainer

		pauseContainer.PodName = container.PodName
		pauseContainer.PodId = container.PodId
		pauseContainer.ContainerName = container.ContainerName
		pauseContainer.ContainerId = container.ContainerId
		pauseContainer.ContainerData = container.ContainerData
		pauseContainer.Timestamp = time.Now().Unix()

		pauseContainerList = append(pauseContainerList, pauseContainer)

	}
	return pauseContainerList
}

func PauseContainer(client internalapi.RuntimeService, pauseCandicate *global.ContainerData) {
	fmt.Println("Pause")
	pauseCandicate.OriginalContainerData.Linux.CpuQuota = global.LIMIT_CPU_QUOTA
	mod.UpdateContainerResources(client, pauseCandicate.Id, pauseCandicate.OriginalContainerData)
}

func ContinueContainer(client internalapi.RuntimeService, continueCandicate *global.ContainerData) {
	fmt.Println("Continue")
	continueCandicate.OriginalContainerData.Linux.CpuQuota = global.DEFAULT_CPU_QUOTA
	mod.UpdateContainerResources(client, continueCandicate.Id, continueCandicate.OriginalContainerData)
}
