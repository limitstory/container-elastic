package modules

import (
	global "elastic/modules/global"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

func DecisionCheckpoint(resultChan chan global.PauseContainer, pauseContainerList []global.PauseContainer) {

	var wg sync.WaitGroup
	var count int
	var err error

	for _, pauseContainer := range pauseContainerList {
		if (pauseContainer.DuringCheckpoint == false) && (pauseContainer.IsCheckpoint == false) {
			count++
		}
	}
	//fmt.Println("count: ", count)

	wg.Add(count)
	for i, pauseContainer := range pauseContainerList {
		if (pauseContainer.DuringCheckpoint == false) && (pauseContainer.IsCheckpoint == false) {
			pauseContainerList[i].DuringCheckpoint = true
			resultChan <- pauseContainerList[i]
			// 이 부분이 goroutine기반의 비동기식 프로그래밍으로 작성되여야 할 듯 함.
			go func(container global.PauseContainer) {
				container.CheckpointData, err = CheckpointContainer(container.PodName, *container.ContainerData)
				if err != nil {
					container.DuringCheckpoint = false
					fmt.Println("checkpoint failed: ", container.PodName)
					resultChan <- container

					wg.Done()
				} else {
					container.DuringCheckpoint = false
					container.IsCheckpoint = true
					fmt.Println("checkpoint complete: ", container.PodName)
					resultChan <- container

					wg.Done()
				}
			}(pauseContainer)
		}
	}
	wg.Wait()
}

func CheckpointContainer(podName string, container global.ContainerData) (global.CheckpointMetaData, error) {
	var checkpointInfo map[string][]interface{}
	var checkpointMetaData global.CheckpointMetaData

	command := "curl -sk -X POST \"https://localhost:10250/checkpoint/default/" + podName + "/" + container.Name + "\""
	out, _ := exec.Command("bash", "-c", command).Output()

	json.Unmarshal([]byte(out), &checkpointInfo)

	if checkpointInfo["items"] == nil {
		err := errors.New("Empty object.")
		return checkpointMetaData, err
	}

	// append checkpointMetaData
	checkpointMetaData.MemoryLimitInBytes = container.Cgroup.MemoryLimitInBytes
	checkpointMetaData.CheckpointName = string(checkpointInfo["items"][0].(string))

	return checkpointMetaData, nil
}

func MakeContainerFromCheckpoint(checkpoint global.PauseContainer) {
	// latest가 이미 있는 경우 기존 latest의 컨테이너 태그가 해제되고 새로 등록한 것이 latest가 됨
	// 굳이 삭제할 필요는 없을 것 같음
	command1 := ("newcontainer=$(sudo buildah from scratch) && sudo buildah add $newcontainer " + checkpoint.CheckpointData.CheckpointName + " / && ")
	command2 := "sudo buildah config --annotation=io.kubernetes.cri-o.annotations.checkpoint.name=" + checkpoint.ContainerName + " $newcontainer && "
	command3 := "sudo buildah commit $newcontainer " + checkpoint.PodName + ":latest && sudo buildah rm $newcontainer"

	fmt.Println(command1 + command2 + command3)
	// 정상적으로 처리되는 것은 확인하였음
	_, err := exec.Command("bash", "-c", command1+command2+command3).Output()
	if err != nil {
		fmt.Println(err)
	}
	//fmt.Println(string(out1))
}
