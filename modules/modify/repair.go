package modules

import (
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"

	mod "elastic/modules"
	cp "elastic/modules/checkpoint"
	global "elastic/modules/global"

	internalapi "k8s.io/cri-api/pkg/apis"
)

func DecisionRepairContainer(resultChan chan global.PauseContainer, client internalapi.RuntimeService, systemInfoSet []global.SystemInfo, podIndex map[string]int64,
	podInfoSet []global.PodData, currentRunningPods []string, lenghOfCurrentRunningPods int, priorityMap map[string]global.PriorityContainer,
	removeContainerList []global.PauseContainer) {

	var mem int64
	var wg sync.WaitGroup
	var count int
	var repairContainerCandidateList []global.PauseContainer

	if len(removeContainerList) == 0 {
		return
	}

	// FIFO 구조로 일단 짰으며, 이 부분은 고민이 필요함.
	// 먼저 들어오고 먼저 나가는 방식이 아니라 메모리 조건 만족하면 바로 나갈 수 있게끔??
	// 연산 cost가 너무 커짐... 메모리 순으로 다시 sort해야 한다.
	// 아니면 우선순위가 높은 순으로 정렬?? 이 경우에는 다른 작업이 못나갈 가능성이 있음....

	// 현재 실행중인 파드의 메모리 limit 합을 더한 것
	var sumLimitMemorySize int64

	for _, podName := range currentRunningPods {
		pod := podInfoSet[podIndex[podName]]
		for _, container := range pod.Container {
			res := container.Resource

			// exception handling
			if len(res) == 0 {
				continue
			}
			sumLimitMemorySize += container.Cgroup.MemoryLimitInBytes
		}
	}

	for {
		if len(removeContainerList) == 0 {
			break
		}
		mem += int64(float64(removeContainerList[0].CheckpointData.MemoryLimitInBytes) * 1.1)

		if mem+sumLimitMemorySize > int64(float64(systemInfoSet[len(systemInfoSet)-1].Memory.Total)*global.MAX_MEMORY_USAGE_THRESHOLD) {
			break
		}
		repairContainerCandidateList = append(repairContainerCandidateList, removeContainerList[0])
		resultChan <- removeContainerList[0]
		if len(removeContainerList) == 1 {
			break
		}
		removeContainerList = removeContainerList[1 : len(removeContainerList)-1]
	}
	//채널 2개 만들어서 처리하는 방안은?

	for _, container := range repairContainerCandidateList {
		if (container.DuringCreateContainer == false) && (container.CreateContainer == false) {
			count++
		}
	}

	wg.Add(count)
	for _, repairContainerCandidate := range repairContainerCandidateList {
		if repairContainerCandidate.DuringCreateContainer || repairContainerCandidate.CreateContainer {
			continue
		}
		// 비동기 구현
		go func(container global.PauseContainer) {
			container.DuringCheckpoint = true
			cp.MakeContainerFromCheckpoint(container)
			container.DuringCheckpoint = false
			container.CreateContainer = true

			repairContainerCandidate.CheckpointData.RemoveEndTime = time.Now().Unix()
			RestoreContainer(container)
			podInfoSet = mod.UpdatePodData(client, container, podIndex, podInfoSet)
			fmt.Println("Complete!!!!!")
			wg.Done()
		}(repairContainerCandidate)
	}
	wg.Wait()
}

func RestoreContainer(container global.PauseContainer) {
	// kubernetes master에 연결해서 명령어 보내야 할듯....
	memoryLimits := int64(float64(container.CheckpointData.MemoryLimitInBytes) * 1.1)
	command1 := "kubectl create -f - <<EOF\napiVersion: v1\nkind: Pod\nmetadata:\n  name: " + container.PodName + "\n"
	command2 := "spec:\n  containers:\n  - name: " + container.ContainerName + "\n    image: localhost/" + container.PodName + ":latest\n    "
	command3 := "resources:\n      requests:\n        memory: " + strconv.FormatInt(global.MIN_SIZE_PER_CONTAINER, 10)
	command4 := "\n      limits:\n        cpu: " + strconv.FormatFloat(float64(global.DEFAULT_CPU_QUOTA)*0.00001, 'f', -1, 64) + "\n        memory: " + strconv.FormatInt(memoryLimits, 10) + "\n  "
	command5 := "nodeName: " + global.NODENAME + "\nEOF" // 프로그램마다 노드네임 다르게 설정해야 한다.
	//fmt.Println(command1 + command2 + command3 + command4 + command5)
	_, err := exec.Command("bash", "-c", command1+command2+command3+command4+command5).Output()
	if err != nil {
		fmt.Println(err)
	}
}
