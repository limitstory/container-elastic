package modules

import (
	"fmt"
	"sync"

	internalapi "k8s.io/cri-api/pkg/apis"

	mod "elastic/modules"
	global "elastic/modules/global"
)

func DecisionScaleUp(client internalapi.RuntimeService, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string,
	systemInfoSet []global.SystemInfo, priorityMap map[string]global.PriorityContainer, scaleUpCandidateList []global.ScaleCandidateContainer,
	pauseContainerList []global.PauseContainer) ([]global.PodData, []global.ScaleCandidateContainer, []global.PauseContainer) {

	var wg sync.WaitGroup

	// Scale Candidate List
	var scaleUpMemorySize int64
	var sumLimitMemorySize int64

	memory := systemInfoSet[len(systemInfoSet)-1].Memory

	scaleUpCandidateList, sumLimitMemorySize = AppendToScaleUpCandidateList(scaleUpCandidateList, podIndex, podInfoSet, currentRunningPods)
	// calculate require memory

	for i := 0; i < len(scaleUpCandidateList); i++ {
		currentMemory := scaleUpCandidateList[i].ContainerData.OriginalContainerData.Linux.MemoryLimitInBytes
		scaleUpCandidateList[i].ScaleSize = CalculateScaleSize(scaleUpCandidateList[i].ContainerData)
		if currentMemory == global.MAX_SIZE_PER_CONTAINER {
			scaleUpCandidateList[i].ScaleSize = 0
		} else if currentMemory+scaleUpCandidateList[i].ScaleSize > global.MAX_SIZE_PER_CONTAINER {
			scaleUpCandidateList[i].ScaleSize = global.MAX_SIZE_PER_CONTAINER - currentMemory
		} else if scaleUpCandidateList[i].ScaleSize < global.MIN_SCALE_SIZE {
			scaleUpCandidateList[i].ScaleSize = global.MIN_SCALE_SIZE
		}
		scaleUpMemorySize += scaleUpCandidateList[i].ScaleSize
	}

	// Memory capacity is sufficient
	// 기존에 os에서 점유하는 메모리가 있기 때문에 그걸 offset으로 빼주어야 할 것이다.
	if float64(sumLimitMemorySize)+float64(scaleUpMemorySize) < float64(memory.Total)*global.MAX_MEMORY_USAGE_THRESHOLD {
		// Scale up all containers
		wg.Add(len(scaleUpCandidateList))
		for _, scaleCandidate := range scaleUpCandidateList {

			go func(scaleCandidate global.ScaleCandidateContainer) {
				defer wg.Done()

				if scaleCandidate.ScaleSize == 0 {
					return
				}
				if scaleCandidate.ContainerData.Cgroup.CpuQuota == global.LIMIT_CPU_QUOTA {
					ContinueContainer(client, scaleCandidate.ContainerData)
				}
				ScaleUp(client, scaleCandidate.ContainerData, scaleCandidate.ScaleSize)
				// update container info
				mod.UpdateContainerData(client, scaleCandidate.ContainerData)
				// increase the number of scale
				scaleCandidate.ContainerData.NumOfScale++
				// reset TimeWindow
				scaleCandidate.ContainerData.TimeWindow = 1
				// reset container resource slice
				scaleCandidate.ContainerData.Resource = scaleCandidate.ContainerData.Resource[len(scaleCandidate.ContainerData.Resource)-1:]
			}(scaleCandidate)
		}
		wg.Wait()
		// Reset scaleUpCandidateList
		scaleUpCandidateList = scaleUpCandidateList[:0]
	} else { // Memory capacity is not sufficient
		var listSize = len(scaleUpCandidateList)
		sortedScaleUpCandidateList := make([]global.ScaleCandidateContainer, 0, len(scaleUpCandidateList))
		var targetScaleUpList []global.ScaleCandidateContainer
		scaleUpMemorySize = 0

		// Sort scaleUpCandidateList with highest priority
		for i := 0; i < listSize; i++ {
			var highestPriority float64 = 0
			var highestPriorityIndex int
			for j := 0; j < listSize-i; j++ {
				if highestPriority < priorityMap[scaleUpCandidateList[j].PodName].Priority {
					highestPriorityIndex = j
				}
			}
			sortedScaleUpCandidateList = append(sortedScaleUpCandidateList, scaleUpCandidateList[highestPriorityIndex])
			scaleUpCandidateList = append(scaleUpCandidateList[:highestPriorityIndex], scaleUpCandidateList[highestPriorityIndex+1:]...)
		}

		// select container to scale up
		var noScaleUpIndex int //Do not scale-up from that index number
		var lastScaleUpSize int64
		for noScaleUpIndex = 0; noScaleUpIndex < len(sortedScaleUpCandidateList); noScaleUpIndex++ {
			if float64(sumLimitMemorySize)+float64(scaleUpMemorySize)+float64(sortedScaleUpCandidateList[noScaleUpIndex].ScaleSize) >
				float64(memory.Total)*global.MAX_MEMORY_USAGE_THRESHOLD {
				if noScaleUpIndex == 0 {
					break
				}
				lastScaleUpSize = int64(float64(memory.Total)*global.MAX_MEMORY_USAGE_THRESHOLD -
					(float64(sumLimitMemorySize) + float64(scaleUpMemorySize)))
				noScaleUpIndex++
				break
			} else {
				scaleUpMemorySize += sortedScaleUpCandidateList[noScaleUpIndex].ScaleSize
			}
		}
		// append index 0 to scaleUpindex
		if noScaleUpIndex >= len(sortedScaleUpCandidateList) {
			targetScaleUpList = sortedScaleUpCandidateList[:noScaleUpIndex]
		} else {
			targetScaleUpList = sortedScaleUpCandidateList[:noScaleUpIndex+1]
		}

		// Scale up selected container
		wg.Add(len(targetScaleUpList))
		for i, scaleCandidate := range targetScaleUpList {

			go func(scaleCandidate global.ScaleCandidateContainer, i int) {
				defer wg.Done()

				if lastScaleUpSize != 0 && i == len(targetScaleUpList)-1 {
					scaleCandidate.ScaleSize = lastScaleUpSize
					//fmt.Println("Size: ", lastScaleUpSize)
					if scaleCandidate.ContainerData.Cgroup.CpuQuota == global.LIMIT_CPU_QUOTA {
						ContinueContainer(client, scaleCandidate.ContainerData)
					}
					ScaleUp(client, scaleCandidate.ContainerData, scaleCandidate.ScaleSize)
					// update container info
					mod.UpdateContainerData(client, scaleCandidate.ContainerData)
				} else {
					if scaleCandidate.ContainerData.Cgroup.CpuQuota == global.LIMIT_CPU_QUOTA {
						ContinueContainer(client, scaleCandidate.ContainerData)
					}
					ScaleUp(client, scaleCandidate.ContainerData, scaleCandidate.ScaleSize)
					// update container info
					mod.UpdateContainerData(client, scaleCandidate.ContainerData)
				}
				// increase the number of scale
				scaleCandidate.ContainerData.NumOfScale++
				// reset TimeWindow
				scaleCandidate.ContainerData.TimeWindow = 1
				// reset container resource slice
				scaleCandidate.ContainerData.Resource = scaleCandidate.ContainerData.Resource[len(scaleCandidate.ContainerData.Resource)-1:]
			}(scaleCandidate, i)
		}
		wg.Wait()

		// Change scaleUpCandidateList (append index i to end)
		if noScaleUpIndex < len(sortedScaleUpCandidateList) { // If all containers are not scaled up
			scaleUpCandidateList = sortedScaleUpCandidateList[noScaleUpIndex:]
		} else { // If all containers are scaled up
			scaleUpCandidateList = scaleUpCandidateList[:0]
		}

		// logic to pause low priority container
		var pauseCandidateList []global.ScaleCandidateContainer
		wg.Add(len(scaleUpCandidateList))
		for _, pauseCandidate := range scaleUpCandidateList {
			go func(pauseCandidate global.ScaleCandidateContainer) {
				defer wg.Done()
				// Check for pauses
				if CheckToPauseContainer(pauseCandidate, pauseContainerList) { // if not pause yet
					PauseContainer(client, pauseCandidate.ContainerData)
					// update container info
					// 여기 예외처리 해야됨
					mod.UpdateContainerData(client, pauseCandidate.ContainerData)
					// append pauseContainerList
					pauseCandidateList = append(pauseCandidateList, pauseCandidate)
				}
			}(pauseCandidate)
		}
		wg.Wait()
		pauseContainerList = AppendPauseContainerList(pauseContainerList, pauseCandidateList)

	}

	var toRemovePauseContainer []int
	// logic to continue execute scaleup container
	for i := 0; i < len(pauseContainerList); i++ {
		isRequireContinue := true
		for _, scaleUpCandidate := range scaleUpCandidateList {
			if pauseContainerList[i].PodName == scaleUpCandidate.PodName {
				isRequireContinue = false
				break
			}
		}
		if isRequireContinue {
			wg.Add(1)
			go func(pauseContainerList []global.PauseContainer, i int) {
				defer wg.Done()

				ContinueContainer(client, pauseContainerList[i].ContainerData)
				// update container info
				mod.UpdateContainerData(client, pauseContainerList[i].ContainerData)
				toRemovePauseContainer = append(toRemovePauseContainer, i)
			}(pauseContainerList, i)
		}
	}
	wg.Wait()

	for i := len(toRemovePauseContainer) - 1; i >= 0; i-- {
		idx := toRemovePauseContainer[i]
		if idx < len(pauseContainerList) {
			pauseContainerList = append(pauseContainerList[:idx], pauseContainerList[idx+1:]...)
		} else {
			// idx가 슬라이스 범위를 벗어나는 경우, 마지막 요소를 제거
			pauseContainerList = pauseContainerList[:idx]
		}
	}

	return podInfoSet, scaleUpCandidateList, pauseContainerList
}

func DecisionScaleDown(client internalapi.RuntimeService, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string, systemInfoSet []global.SystemInfo) []global.PodData {
	var wg sync.WaitGroup

	// Scale Candidate List
	var scaleDownCandidateList []global.ScaleCandidateContainer
	podInfoSet, scaleDownCandidateList = AppendToScaleDownCandidateList(client, scaleDownCandidateList, podIndex, podInfoSet, currentRunningPods)

	wg.Add(len(scaleDownCandidateList))
	for _, scaleCandidate := range scaleDownCandidateList {
		go func(scaleCandidate global.ScaleCandidateContainer) {
			defer wg.Done()

			res := scaleCandidate.ContainerData.Resource
			if len(res) == 0 {
				return
			}
			scaleCandidate.ScaleSize = int64(float64(res[len(res)-1].MemoryUsageBytes) / float64(global.CONTAINER_MEMORY_SLO))

			if scaleCandidate.ContainerData.Cgroup.CpuQuota == global.LIMIT_CPU_QUOTA {
				ContinueContainer(client, scaleCandidate.ContainerData)
			}
			ScaleDown(client, scaleCandidate.ContainerData, scaleCandidate.ScaleSize)
			// reset TimeWindow
			scaleCandidate.ContainerData.TimeWindow = 1
			// update container info
			mod.UpdateContainerData(client, scaleCandidate.ContainerData)

			// reset container resource slice
			scaleCandidate.ContainerData.Resource = scaleCandidate.ContainerData.Resource[len(scaleCandidate.ContainerData.Resource)-1:]
		}(scaleCandidate)
	}
	wg.Wait()

	return podInfoSet
}

func AppendToScaleUpCandidateList(scaleUpCandidateList []global.ScaleCandidateContainer, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string) ([]global.ScaleCandidateContainer, int64) {
	var sumLimitMemorySize int64

	for _, podName := range currentRunningPods {
		pod := podInfoSet[podIndex[podName]]
		for i, container := range pod.Container {
			res := container.Resource

			// exception handling
			if len(res) == 0 {
				continue
			}

			conMemUtil := res[len(res)-1].ConMemUtil
			sumLimitMemorySize += container.Cgroup.MemoryLimitInBytes

			// Register scale candidates
			if conMemUtil > global.CONTAINER_MEMORY_SLO_UPPER {
				// No need to add to the array if it has already added
				var scaleUpCandiate global.ScaleCandidateContainer

				if CheckToAppendScaleCandidateList(podName, container, scaleUpCandidateList) {
					scaleUpCandiate.PodName = podName
					scaleUpCandiate.PodId = pod.Id
					scaleUpCandiate.ContainerName = container.Name
					scaleUpCandiate.ContainerId = container.Id
					scaleUpCandiate.ContainerData = &pod.Container[i]

					scaleUpCandidateList = append(scaleUpCandidateList, scaleUpCandiate)
				}
			}
		}
	}
	return scaleUpCandidateList, sumLimitMemorySize
}

func AppendToScaleDownCandidateList(client internalapi.RuntimeService, scaleDownCandidateList []global.ScaleCandidateContainer, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string) ([]global.PodData, []global.ScaleCandidateContainer) {

	for _, podName := range currentRunningPods {
		pod := podInfoSet[podIndex[podName]]
		for i, container := range pod.Container {
			res := container.Resource

			if len(res) == 0 {
				continue
			}
			conMemUtil := res[len(res)-1].ConMemUtil

			// Minimum size per container
			if len(pod.Container[i].Resource) < global.SCALE_DOWN_THRESHOLD {
				continue
			}
			// exception handling: MemoryUsageBytes < global.MIN_SIZE_PER_CONTAINER
			if int64(float64(res[len(res)-1].MemoryUsageBytes)) < global.MIN_SIZE_PER_CONTAINER {
				if container.Cgroup.MemoryLimitInBytes <= global.MIN_SIZE_PER_CONTAINER {
					continue
				}
				// 복구 파드의 경우 메모리 할당량이 올라오기 전까지 scaleDown되지 않음
				if pod.IsRepairPod && container.Cgroup.MemoryLimitInBytes <= pod.RepairRequestMemory { // 우선 생성 시간은 신경쓰지 말자.
					continue
				}
				if container.Cgroup.MemoryLimitInBytes > global.MIN_SIZE_PER_CONTAINER &&
					float64(container.Cgroup.MemoryLimitInBytes) < float64(pod.RequestMemory)*global.CONTAINER_MEMORY_SLO_LOWER {
					ScaleDown(client, &container, global.MIN_SIZE_PER_CONTAINER)
					// update container info
					mod.UpdateContainerData(client, &pod.Container[i])

					// reset TimeWindow
					pod.Container[i].TimeWindow = 1
					// reset container resource slice
					pod.Container[i].Resource = container.Resource[:1]

					continue
				}
			}

			// Register scale candidates
			if conMemUtil < global.CONTAINER_MEMORY_SLO_LOWER {
				// No need to add to the array if it has already added
				var scaleDownCandiate global.ScaleCandidateContainer
				scaleDownCandiate.PodName = podName
				scaleDownCandiate.PodId = pod.Id
				scaleDownCandiate.ContainerName = container.Name
				scaleDownCandiate.ContainerId = container.Id
				scaleDownCandiate.ContainerData = &pod.Container[i]

				scaleDownCandidateList = append(scaleDownCandidateList, scaleDownCandiate)

			}
		}
	}
	return podInfoSet, scaleDownCandidateList
}

func CalculateScaleSize(scaleCandidate *global.ContainerData) int64 {
	// Determining the size of a formula-based resource allocation
	// Scaleup_c=∑_(t=1)^(T/10)▒〖T/20t*(〖C_MemUsed〗^10(t-1) -〖C_MemUsed〗^10t)
	var scaleSize float64

	reslen := len(scaleCandidate.Resource)
	if reslen <= 2 {
		return int64(float64(scaleCandidate.Resource[0].MemoryUsageBytes) * 0.3)
	}

	for i := 0; i <= (reslen-1)/10; i++ {
		if i == (reslen-1)/10 {
			size := scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes - scaleCandidate.Resource[0].MemoryUsageBytes
			if scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes < scaleCandidate.Resource[0].MemoryUsageBytes {
				break
			} else {
				scaleSize += float64(size) * (float64(global.MAX_TIME_WINDOW) / float64(global.SACLE_WEIGHT*(i+1))) // 수식 조정이 필요할 수도 잇음
			}
			//fmt.Println(reslen-(i*10+1), " ~ 0", "Size: ", scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes-scaleCandidate.Resource[0].MemoryUsageBytes)
			//fmt.Println(i, "Size-first: ", float64(scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes))
			//fmt.Println(i, "Size-last: ", float64(scaleCandidate.Resource[0].MemoryUsageBytes))
		} else {
			size := scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes - scaleCandidate.Resource[reslen-((i+1)*10)].MemoryUsageBytes
			// 음수일 경우 loop를 벗어난다.
			if scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes < scaleCandidate.Resource[reslen-((i+1)*10)].MemoryUsageBytes {
				break
			} else {
				scaleSize += float64(size) * (float64(global.MAX_TIME_WINDOW) / float64(global.SACLE_WEIGHT*(i+1)))
			}
			//fmt.Println(reslen-(i*10+1), " ~ ", reslen-((i+1)*10), " ", "Size: ", scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes-scaleCandidate.Resource[reslen-((i+1)*10)].MemoryUsageBytes)
			//fmt.Println(i, "Size-first: ", scaleCandidate.Resource[reslen-(i*10+1)].MemoryUsageBytes)
			//fmt.Println(i, "Size-last: ", scaleCandidate.Resource[reslen-((i+1)*10)].MemoryUsageBytes)
		}
		//fmt.Println("ScaleSize ", i, ": ", scaleSize)
	}

	return int64(scaleSize)
}

func CheckToAppendScaleCandidateList(podName string, container global.ContainerData, scaleCandidateList []global.ScaleCandidateContainer) bool {
	for _, scaleCandidate := range scaleCandidateList {
		if scaleCandidate.ContainerName == container.Name && scaleCandidate.PodName == podName {
			return false
		}
	}
	return true
}

func ScaleUp(client internalapi.RuntimeService, scaleUpCandidate *global.ContainerData, scaleUpSize int64) {
	scaleUpCandidate.OriginalContainerData.Linux.MemoryLimitInBytes += scaleUpSize
	fmt.Println("ScaleUp")
	mod.UpdateContainerResources(client, scaleUpCandidate.Id, scaleUpCandidate.OriginalContainerData)
}

func ScaleDown(client internalapi.RuntimeService, scaleDownCandidate *global.ContainerData, scaleDownSize int64) {
	scaleDownCandidate.OriginalContainerData.Linux.MemoryLimitInBytes = scaleDownSize
	fmt.Println("ScaleDown")
	mod.UpdateContainerResources(client, scaleDownCandidate.Id, scaleDownCandidate.OriginalContainerData)
}
