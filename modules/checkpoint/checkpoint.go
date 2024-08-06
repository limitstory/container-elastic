package modules

import (
	global "elastic/modules/global"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

func DecisionCheckpoint(resultChan1 chan global.CheckpointContainer, resultChan2 chan global.CheckpointContainer, pauseContainerList []global.PauseContainer,
	checkpointContainerList []global.CheckpointContainer, semaphore chan struct{}) {

	var wg sync.WaitGroup
	var count int
	var err error
	var updateCandidateList []global.CheckpointContainer

	for _, pauseContainer := range pauseContainerList {
		isAppend := AppendToCheckpointList(pauseContainer, checkpointContainerList)

		if isAppend {
			var container global.CheckpointContainer

			container.PodName = pauseContainer.PodName
			container.PodId = pauseContainer.PodId
			container.ContainerName = pauseContainer.ContainerName
			container.Timestamp = time.Now().Unix() // current timestamp value
			container.PodId = pauseContainer.PodId
			container.ContainerData = pauseContainer.ContainerData

			resultChan1 <- container
			updateCandidateList = append(updateCandidateList, container)

			count++
		}
	}
	//fmt.Println("UpdateCandidateList", updateCandidateList)
	// 해당 워크로드에서 처리되어야 할 체크포인트 리스트
	//복사본 필요

	//time.Sleep(time.Second) // checkpoint 여부가 바뀔 시간을 줄 수 있도록

	wg.Add(count)
	for _, checkpointContainer := range updateCandidateList {
		//if !checkpointContainer.DuringCheckpoint && !checkpointContainer.IsCheckpoint { //여기에서... 체크를 해야할까?

		go func(container global.CheckpointContainer) {
			semaphore <- struct{}{}
			defer wg.Done()
			defer func() {
				<-semaphore // 작업이 끝나면 세마포어에서 자리를 비움
			}()

			container.DuringCheckpoint = true
			resultChan2 <- container

			//checkpoint container list도 확인해야 하는데?
			// if container.DuringCheckpoint || container.IsCheckpoint {

			var copyPauseContainerList []global.PauseContainer
			copy(copyPauseContainerList, pauseContainerList)

			//for _, pauseContainer := range copyPauseContainerList { //차라리 여기에서 pause와 함께 checkpointContainerList확인하는 것이 나을듯
			//if container.PodName == pauseContainer.PodName { //pauseContainerList에서 삭제되었다면 체크포인트하지 않음

			container.StartCheckpointTime = time.Now().Unix()
			container.CheckpointData, err = CheckpointContainer(container.PodName, *container.ContainerData)
			if err != nil {
				container.DuringCheckpoint = false
				fmt.Println("checkpoint failed: ", container.PodName)
				resultChan2 <- container
				//break
			} else {
				container.DuringCheckpoint = false
				container.IsCheckpoint = true
				resultChan2 <- container
			}
		}(checkpointContainer)
	}
	wg.Wait()
}

func AppendToCheckpointList(pauseContainer global.PauseContainer, checkpointContainerList []global.CheckpointContainer) bool {
	res := pauseContainer.ContainerData.Resource
	if pauseContainer.IsCheckpoint || res[len(res)-1].ConMemUtil <= global.CHECKPOINT_THRESHOLD {
		return false
	}

	for _, checkpointContainer := range checkpointContainerList {
		if pauseContainer.PodName == checkpointContainer.PodName {
			if pauseContainer.Timestamp < checkpointContainer.Timestamp+global.RECHECKPOINT_THRESHOLD {
				return false
			}
		}
	}
	return true
}

func CheckpointContainer(podName string, container global.ContainerData) (global.CheckpointMetaData, error) {
	var checkpointInfo map[string][]interface{}
	var checkpointMetaData global.CheckpointMetaData

	command := "curl -sk -X POST \"https://localhost:10250/checkpoint/default/" + podName + "/" + container.Name + "\""
	out, err := exec.Command("bash", "-c", command).Output()
	if err != nil {
		fmt.Println(err)
		return checkpointMetaData, err
	}

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

func MakeContainerFromCheckpoint(checkpoint global.CheckpointContainer) bool {
	// latest가 이미 있는 경우 기존 latest의 컨테이너 태그가 해제되고 새로 등록한 것이 latest가 됨
	// 굳이 삭제할 필요는 없을 것 같음parsec-raytrace1
	command1 := ("newcontainer=$(sudo buildah from scratch) && sudo buildah add $newcontainer " + checkpoint.CheckpointData.CheckpointName + " / && ")
	command2 := "sudo buildah config --annotation=io.kubernetes.cri-o.annotations.checkpoint.name=" + checkpoint.ContainerName + " $newcontainer && "
	command3 := "sudo buildah commit $newcontainer " + checkpoint.PodName + ":latest && sudo buildah rm $newcontainer"

	fmt.Println(command1 + command2 + command3)

	// 정상적으로 처리되는 것은 확인하였음
	_, err := exec.Command("bash", "-c", command1+command2+command3).Output()
	if err != nil {
		fmt.Println(err)
		return false
	}
	//fmt.Println(string(out1))
	return true
}
