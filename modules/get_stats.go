package modules

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	internalapi "k8s.io/cri-api/pkg/apis"
	pb "k8s.io/cri-api/pkg/apis/runtime/v1"

	global "elastic/modules/global"
)

type ResultPodData struct {
	PodName      string
	StartTime    int64
	StartedAt    int64
	FinishedAt   int64
	RunningTime  int64
	WaitTime     int64
	RemoveTime   int64
	RestartCount int32
}

func GetListPodStatsInfo(client internalapi.RuntimeService) ([]*pb.PodSandboxStats, error) {

	stats, err := client.ListPodSandboxStats(context.TODO(), &pb.PodSandboxStatsFilter{})
	if err != nil {
		fmt.Println(err)
	}
	return stats, err
}

func GetPodStatsInfo(client internalapi.RuntimeService, systemInfoSet []global.SystemInfo, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string,
	checkpointContainerList []global.CheckpointContainer, removeContainerList []global.CheckpointContainer, avgCheckpointTime []global.CheckpointTime,
	avgImageTime []global.ImageTime, avgRemoveTime []global.RemoveTime, avgRepairTime []global.RepairTime, resultChan chan global.CheckpointContainer) ([]global.PodData, []string) {

	isPodRunning := false

	listPodStats, err := GetListPodStatsInfo(client)
	if err != nil {
		return podInfoSet, currentRunningPods // See currentRunningPods from the previous round
	}

	currentRunningPods = nil // initialize currentRunningPods

	for _, podStats := range listPodStats {
		podName := podStats.Attributes.Metadata.Name

		// Do not store namespaces other than default namespaces
		if podStats.Attributes.Metadata.Namespace != "default" {
			continue
		}

		// Do not store info of not working pods
		status, _ := client.PodSandboxStatus(context.TODO(), podStats.Attributes.Id, false)
		if status == nil { // exception handling: nil pointer
			continue
		}
		// Do not store info of complete pods
		if status.Status.State == 1 { // exception handling: SANDBOX_NOTREADY
			continue
		}
		isPodRunning = true

		/*
			if len(podStats.Linux.Containers) == 0 { // exception handling: pod is created, but it not have containers
				continue
			}*/

		// check current running pods
		currentRunningPods = append(currentRunningPods, podName)

		// init pod data
		if _, exists := podIndex[podName]; !exists {
			podInfoSet = InitPodData(podName, podIndex, podInfoSet, podStats)
		}
		pod := &podInfoSet[podIndex[podName]]

		// get containers stats
		var isRunning bool
		isRunning = getContainerStatsInfo(client, podStats, pod, podName, checkpointContainerList, removeContainerList, resultChan)
		if !isRunning {
			currentRunningPods = currentRunningPods[:len(currentRunningPods)-1]
		}
	}

	if !isPodRunning {
		fmt.Println("There is no pod running.")

		PrintResult(systemInfoSet, podInfoSet, podIndex, avgCheckpointTime, avgImageTime, avgRemoveTime, avgRepairTime)

		os.Exit(0)
	}

	return podInfoSet, currentRunningPods
}

func InitPodData(podName string, podIndex map[string]int64, podInfoSet []global.PodData, podStats *pb.PodSandboxStats) []global.PodData {
	var podInfo global.PodData

	podInfo.Id = podStats.Attributes.Id
	podInfo.Name = podStats.Attributes.Metadata.Name
	podInfo.Uid = podStats.Attributes.Metadata.Uid
	podInfo.Namespace = podStats.Attributes.Metadata.Namespace

	command := "kubectl get po " + podName + " -o=json | jq '.spec.containers[0].resources.requests.memory'"
	out, _ := exec.Command("bash", "-c", command).Output()
	memoryRequestStr := string(out[:])
	memoryRequestStr = memoryRequestStr[1 : len(memoryRequestStr)-2] // remove double quotation
	memoryRequestStr = strings.Trim(memoryRequestStr, "Mi")          // Using Trim() function - remove Mi string
	memoryRequest, _ := strconv.Atoi(memoryRequestStr)               // type conversion - string to int
	memoryRequestToBytes := memoryRequest * 1048576                  // mibibyte to byte
	podInfo.RequestMemory = int64(memoryRequestToBytes)

	podIndex[podName] = int64(len(podInfoSet))
	podInfoSet = append(podInfoSet, podInfo) // append dynamic array

	return podInfoSet
}

// func checkRestartCount

func getContainerStatsInfo(client internalapi.RuntimeService, podStats *pb.PodSandboxStats, pod *global.PodData,
	podName string, checkpointContainerList []global.CheckpointContainer, removeContainerList []global.CheckpointContainer,
	resultChan chan global.CheckpointContainer) bool {

	// Detect if "restart container" is restarted
	command := "kubectl get po " + podName + " -o=json | jq '.status.containerStatuses[0].state.waiting.reason'"

	out, _ := exec.Command("bash", "-c", command).Output()
	isCreateContainerError := string(out[:])

	if len(isCreateContainerError) < 2 {
		return false
	}

	isCreateContainerError = isCreateContainerError[1 : len(isCreateContainerError)-2]
	//fmt.Println(isCreateContainerError)

	if isCreateContainerError == "CreateContainerError" {
		for _, checkpointContainer := range checkpointContainerList {
			if podName == checkpointContainer.PodName {
				RemoveRestartedRepairContainer(client, podName)
				resultChan <- checkpointContainer

				return false
			}
		}
	}

	if len(podStats.Linux.Containers) == 0 { // Crashbackoff(OOMKilled etc.)
		return false
	}

	for _, containerStats := range podStats.Linux.Containers {
		containerName := containerStats.Attributes.Metadata.Name

		var containerResource global.ContainerResourceData

		_, err := client.ContainerStatus(context.TODO(), containerStats.Attributes.Id, false)
		if err != nil { // exception handling
			fmt.Println(err)
			return false
		}

		// init container data
		if _, exists := pod.ContainerIndex[containerName]; !exists {
			pod.Container = InitContainerData(client, pod, containerName, containerStats, podName)
			//exception handling
			if pod.Container == nil {
				continue
			}
		}
		container := &pod.Container[pod.ContainerIndex[containerName]]

		// Detect if a container is restarted
		if container.Attempt < containerStats.Attributes.Metadata.Attempt {
			container.Attempt = containerStats.Attributes.Metadata.Attempt
			for _, checkpointContainer := range checkpointContainerList {
				if podName == checkpointContainer.PodName && checkpointContainer.IsCheckpoint {
					RemoveContainer(client, podName)
					checkpointContainer.CheckpointData.RemoveStartTime = time.Now().Unix()
					//checkpointContainer.ContainerData.NumOfRemove++
					resultChan <- checkpointContainer

					return false
				}
			}
		}

		// If the container-id changed due to a restart (No checkpointData)
		if container.Id != containerStats.Attributes.Id {
			container = FixRestartContainerData(client, container, containerName, containerStats, pod, podName)

			// 가장 뒤에 습득한 데이터부터...
			container.Resource = container.Resource[len(container.Resource)-1:]
			container.TimeWindow = 1
			// 다른 자료구조에도 바뀌었음을 전달해야 함
			//다른곳에도 전달이 필요할 것이고, 컨테이너 사이즈 이런거 리셋됬을 것인데 어떻게 할 것인데?
		}

		// update restore container data
		if pod.IsRepairPod {
			if container.Id != containerStats.Attributes.Id {
				container = UpdateRepairnitContainerData(client, container, containerName, containerStats, pod)
			}
		}

		if containerStats.Cpu == nil || containerStats.Memory == nil { // exception handling: nil pointer
			continue
		}

		containerResource.CpuUsageCoreNanoSeconds = containerStats.Cpu.UsageCoreNanoSeconds.Value
		// do not support CpuUsageNanoCores in cri-o runtime
		//podInfo.Container[i].Resource.CpuUsageNanoCores = containerStats.Cpu.UsageNanoCores.Value
		containerResource.MemoryAvailableBytes = containerStats.Memory.AvailableBytes.Value
		containerResource.MemoryUsageBytes = containerStats.Memory.WorkingSetBytes.Value
		// cache memory가 포함되어 있음... 주의
		// containerResource.MemoryUsageBytes = containerStats.Memory.UsageBytes.Value

		// convert nanocores to millicores
		containerResource.CpuUsageCoreMilliSeconds = containerResource.CpuUsageCoreNanoSeconds / uint64(global.NANOCORES_TO_MILLICORES)

		container.Resource = append(container.Resource, containerResource)

		// set time window size
		// Data 변경 시 지역 변수에 접근하면 값이 변경되지 않으니 주의한다.
		if container.TimeWindow < global.MAX_TIME_WINDOW {
			container.TimeWindow++
		} else {
			container.Resource = container.Resource[1:]
		}
	}
	return true
}

func InitContainerData(client internalapi.RuntimeService, pod *global.PodData, containerName string, containerStats *pb.ContainerStats, podName string) []global.ContainerData {
	var container global.ContainerData

	container.Id = containerStats.Attributes.Id
	container.Name = containerStats.Attributes.Metadata.Name
	container.Attempt = containerStats.Attributes.Metadata.Attempt

	containerStatus, err := client.ContainerStatus(context.TODO(), container.Id, false)
	if err != nil { // exception handling
		fmt.Println(err)
		return nil
	}

	container.CreatedAt = containerStatus.Status.CreatedAt
	container.StartedAt = containerStatus.Status.StartedAt
	container.FinishedAt = containerStatus.Status.FinishedAt

	containerRes := containerStatus.Status.Resources

	// Adjust request and limit values for operation (request == limit)
	command := "kubectl get po " + podName + " -o=json | jq '.spec.containers[0].resources.requests.memory'"
	out, _ := exec.Command("bash", "-c", command).Output()
	memoryRequestStr := string(out[:])
	memoryRequestStr = memoryRequestStr[1 : len(memoryRequestStr)-2] // remove double quotation
	memoryRequestStr = strings.Trim(memoryRequestStr, "Mi")          // Using Trim() function - remove Mi string
	memoryRequest, _ := strconv.Atoi(memoryRequestStr)               // type conversion - string to int
	memoryRequestToBytes := memoryRequest * 1048576                  // mibibyte to byte
	containerRes.Linux.MemoryLimitInBytes = int64(memoryRequestToBytes)
	UpdateContainerResources(client, container.Id, containerRes)

	// get containerCgroupResources
	container.Cgroup.CpuPeriod = containerRes.Linux.CpuPeriod
	container.Cgroup.CpuQuota = containerRes.Linux.CpuQuota
	container.Cgroup.CpuShares = containerRes.Linux.CpuShares
	container.Cgroup.MemoryLimitInBytes = containerRes.Linux.MemoryLimitInBytes
	//container.Cgroup.OomScoreAdj = containerRes.Linux.OomScoreAdj
	//container.Cgroup.CpusetCpus = containerRes.Linux.CpusetCpus
	//container.Cgroup.CpusetMems = containerRes.Linux.CpusetMems

	//append originalContainerData
	container.OriginalContainerData = containerRes

	//append container in podInfoSet
	pod.ContainerIndex = make(map[string]int64)
	pod.ContainerIndex[containerName] = int64(len(pod.Container))

	pod.Container = append(pod.Container, container)

	// Adjust request and limit values for operation

	return pod.Container
}

func FixRestartContainerData(client internalapi.RuntimeService, container *global.ContainerData, containerName string,
	containerStats *pb.ContainerStats, pod *global.PodData, podName string) *global.ContainerData {

	container.Id = containerStats.Attributes.Id
	container.Name = containerStats.Attributes.Metadata.Name
	container.Attempt = containerStats.Attributes.Metadata.Attempt

	containerStatus, err := client.ContainerStatus(context.TODO(), container.Id, false)
	if err != nil { // exception handling
		fmt.Println(err)
		return nil
	}

	containerRes := containerStatus.Status.Resources

	// Adjust request and limit values for operation (request == limit)
	command := "kubectl get po " + podName + " -o=json | jq '.spec.containers[0].resources.requests.memory'"
	out, _ := exec.Command("bash", "-c", command).Output()
	memoryRequestStr := string(out[:])
	memoryRequestStr = memoryRequestStr[1 : len(memoryRequestStr)-2] // remove double quotation
	memoryRequestStr = strings.Trim(memoryRequestStr, "Mi")          // Using Trim() function - remove Mi string
	memoryRequest, _ := strconv.Atoi(memoryRequestStr)               // type conversion - string to int
	memoryRequestToBytes := memoryRequest * 1048576                  // mibibyte to byte
	containerRes.Linux.MemoryLimitInBytes = int64(memoryRequestToBytes)
	UpdateContainerResources(client, container.Id, containerRes)

	// get containerCgroupResources
	container.Cgroup.CpuPeriod = containerRes.Linux.CpuPeriod
	container.Cgroup.CpuQuota = containerRes.Linux.CpuQuota
	container.Cgroup.CpuShares = containerRes.Linux.CpuShares
	container.Cgroup.MemoryLimitInBytes = containerRes.Linux.MemoryLimitInBytes
	//container.Cgroup.OomScoreAdj = containerRes.Linux.OomScoreAdj
	//container.Cgroup.CpusetCpus = containerRes.Linux.CpusetCpus
	//container.Cgroup.CpusetMems = containerRes.Linux.CpusetMems

	//append originalContainerData
	container.OriginalContainerData = containerRes

	return container
}

func UpdateRepairnitContainerData(client internalapi.RuntimeService, container *global.ContainerData, containerName string, containerStats *pb.ContainerStats, pod *global.PodData) *global.ContainerData {

	container.Id = containerStats.Attributes.Id
	container.Name = containerStats.Attributes.Metadata.Name
	container.Resource = make([]global.ContainerResourceData, 0)
	container.Cgroup.MemoryLimitInBytes = int64(float64(container.Cgroup.MemoryLimitInBytes) * 1.1)
	container.Attempt = containerStats.Attributes.Metadata.Attempt
	//container.Priority
	//NumOfScale int64
	//TimeAfterScaleup int64
	//container.CPULimitTime

	container.TimeWindow = 0
	container.NumOfRemove = pod.RepairData.ContainerData.NumOfRemove
	container.DownTime += pod.RepairData.CheckpointData.RemoveEndTime - pod.RepairData.CheckpointData.RemoveStartTime
	// reset repair data
	pod.RepairData = global.CheckpointContainer{}

	containerStatus, err := client.ContainerStatus(context.TODO(), container.Id, false)
	if err != nil { // exception handling
		fmt.Println(err)
	}

	//container.CreatedAt = containerStats.createdAt
	//container.StartedAt = containerStats.Attributes.startedAt
	//container.FinishedAt = containerStats.Attributes.finishedAt

	containerRes := containerStatus.Status.Resources
	containerRes.Linux.MemoryLimitInBytes = container.Cgroup.MemoryLimitInBytes
	UpdateContainerResources(client, container.Id, containerRes)

	// get containerCgroupResources
	container.Cgroup.CpuPeriod = containerRes.Linux.CpuPeriod
	container.Cgroup.CpuQuota = containerRes.Linux.CpuQuota
	container.Cgroup.CpuShares = containerRes.Linux.CpuShares
	container.Cgroup.MemoryLimitInBytes = containerRes.Linux.MemoryLimitInBytes
	//container.Cgroup.OomScoreAdj = containerRes.Linux.OomScoreAdj
	//container.Cgroup.CpusetCpus = containerRes.Linux.CpusetCpus
	//container.Cgroup.CpusetMems = containerRes.Linux.CpusetMems

	//append originalContainerData
	container.OriginalContainerData = containerRes

	return container
}

func UpdatePodData(client internalapi.RuntimeService, repairContainerCandidate global.CheckpointContainer, podIndex map[string]int64, podInfoSet []global.PodData, repairRequestMemory int64) []global.PodData {

	podName := repairContainerCandidate.PodName
	var podId string

	for {
		// 1초 이내에 보통 상황 종료됨
		command := "kubectl get po " + podName + " -o json | jq '.metadata.annotations.\"cni.projectcalico.org/containerID\"'"
		out, _ := exec.Command("bash", "-c", command).Output()
		strout := string(out[:])

		if len(strout) == 0 {
			time.Sleep(time.Second)
			continue
		}

		if strout[1:len(strout)-2] != "ul" {
			podId = strout
			break
		}
		time.Sleep(time.Second)
	}

	podId = podId[1 : len(podId)-2] // remove double quotation

	// get new pod data
	for {
		podStats, _ := client.PodSandboxStats(context.TODO(), podId)

		if podStats != nil {
			podInfoSet[podIndex[podName]].Id = podStats.Attributes.Id
			podInfoSet[podIndex[podName]].Name = podStats.Attributes.Metadata.Name
			podInfoSet[podIndex[podName]].Uid = podStats.Attributes.Metadata.Uid
			podInfoSet[podIndex[podName]].Namespace = podStats.Attributes.Metadata.Namespace
			podInfoSet[podIndex[podName]].IsRepairPod = true
			podInfoSet[podIndex[podName]].RepairRequestMemory = repairRequestMemory
			podInfoSet[podIndex[podName]].RepairData = repairContainerCandidate

			break
		}
		time.Sleep(time.Second)
	}

	return podInfoSet
}

func UpdateContainerData(client internalapi.RuntimeService, containerData *global.ContainerData) {

	containerStatus, _ := client.ContainerStatus(context.TODO(), containerData.Id, false)
	/* 문제 발생 시 다시 exception handling 고민
	if err != nil { // exception handling
		fmt.Println(err)
		fmt.Println("Remove Pod Set")
		podInfoSet = RemovePodofPodInfoSet(podInfoSet, i)
		break
	}*/

	if containerStatus == nil {
		return
	}
	// 이 시간도 굳이 갱신을 해야 하나??
	//containerData.CreatedAt = containerStatus.Status.CreatedAt
	//containerData.StartedAt = containerStatus.Status.StartedAt
	//containerData.FinishedAt = containerStatus.Status.FinishedAt

	containerRes := containerStatus.Status.Resources

	// get containerCgroupResources
	containerData.Cgroup.CpuPeriod = containerRes.Linux.CpuPeriod
	containerData.Cgroup.CpuQuota = containerRes.Linux.CpuQuota
	containerData.Cgroup.CpuShares = containerRes.Linux.CpuShares
	containerData.Cgroup.MemoryLimitInBytes = containerRes.Linux.MemoryLimitInBytes
	//container.Cgroup.OomScoreAdj = containerRes.Linux.OomScoreAdj
	//container.Cgroup.CpusetCpus = containerRes.Linux.CpusetCpus
	//container.Cgroup.CpusetMems = containerRes.Linux.CpusetMems

	//append originalContainerData
	containerData.OriginalContainerData = containerRes
}

/*
func GetContainerCgroupStatsInfo(client internalapi.RuntimeService, container *global.ContainerData) {
	cpuinfo := map[string]interface{}{}
	meminfo := map[string]interface{}{}

	command := "crictl inspect " + container.Id + " | jq '.info.runtimeSpec.linux.resources|"
	cpucommand := command + ".cpu'"
	memcommand := command + ".memory'"
	cpuout, _ := exec.Command("bash", "-c", cpucommand).Output()
	memout, _ := exec.Command("bash", "-c", memcommand).Output()

	json.Unmarshal(cpuout, &cpuinfo)
	json.Unmarshal(memout, &meminfo)

	fmt.Println(cpuinfo)

	// crictl inspect id
	container.Cgroup.CpuPeriod = int64(cpuinfo["period"].(float64))
	container.Cgroup.CpuQuota = int64(cpuinfo["quota"].(float64))
	container.Cgroup.CpuShares = int64(cpuinfo["shares"].(float64))
	container.Cgroup.MemoryLimitInBytes = int64(meminfo["limit"].(float64))
	// container.Cgroup.OomScoreAdj = status.Resources.Linux.OomScoreAdj
	// container.Cgroup.CpusetCpus = status.Resources.Linux.CpusetCpus
	// container.Cgroup.CpusetMems = status.Resources.Linux.CpusetMems
}*/

func GetSystemStatsInfo(systemInfoSet []global.SystemInfo) []global.SystemInfo {
	var getCpu global.Cpu
	var getMemory global.Memory
	var getSystemInfo global.SystemInfo

	per_cpu, err := cpu.Times(true)
	if err != nil {
		panic(err)
	}
	// fmt.Println(per_cpu)

	for i := 0; i < len(per_cpu); i++ {
		getCpu.User += per_cpu[i].User
		getCpu.System += per_cpu[i].System
		getCpu.Nice += per_cpu[i].Nice
		getCpu.Irq += per_cpu[i].Irq
		getCpu.Softirq += per_cpu[i].Softirq
		getCpu.Steal += per_cpu[i].Steal

		getCpu.Idle += per_cpu[i].Idle
		getCpu.Iowait += per_cpu[i].Iowait
	}
	getCpu.TotalCore = getCpu.User + getCpu.System + getCpu.Nice + getCpu.Irq + getCpu.Softirq + getCpu.Steal + getCpu.Idle + getCpu.Iowait
	getCpu.TotalMilliCore = getCpu.TotalCore * global.CORES_TO_MILLICORES // core to millicore

	memory, err := mem.VirtualMemory()
	if err != nil {
		panic(err)
	}
	// fmt.Println(memory)

	getMemory.Total = memory.Total
	getMemory.Available = memory.Available
	getMemory.Used = memory.Total - memory.Available
	getMemory.UsedPercent = float64(getMemory.Used) / float64(memory.Total) * 100

	getSystemInfo.Cpu = getCpu
	getSystemInfo.Memory = getMemory

	systemInfoSet = append(systemInfoSet, getSystemInfo)

	return systemInfoSet
}

func PrintResult(systemInfoSet []global.SystemInfo, podInfoSet []global.PodData, podIndex map[string]int64,
	avgCheckpointTime []global.CheckpointTime, avgImageTime []global.ImageTime, avgRemoveTime []global.RemoveTime, avgRepairTime []global.RepairTime) {
	// 성능측정지표를 여기에서 print하도록
	var pods *v1.PodList
	var err error

	var resultPod []ResultPodData

	var restartLog []int32
	var restartSum int32

	var sumAvgCheckpointTime int64
	var sumAvgImageTime int64
	var sumAvgRepairTime int64

	var runningTimeArr []int64
	var waitTimeArr []int64
	var checkpointTimeArr []int64
	var imageTimeArr []int64
	var repairTimeArr []int64

	var startedTestTime int64 = 9999999999999
	var finishedTestTime int64 = 0

	var minContainerRunningTime int64 = 9999999999999
	var maxContainerRunningTime int64 = 0
	var totalContinerRunningTime int64 = 0

	var minContainerWaitTime int64 = 9999999999999
	var maxContainerWaitTime int64 = 0
	var totalContainerWaitTime int64 = 0

	clientset := InitClient()
	if clientset == nil {
		fmt.Println("Could not create client!")
		os.Exit(-1)
	}

	for {
		pods, err = clientset.CoreV1().Pods("default").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err)
		}

		if IsSucceed(pods.Items) == false {
			time.Sleep(time.Second)
		} else {
			break
		}
	}

	for _, pod := range pods.Items {
		var newPod ResultPodData
		var collectPodInfo *global.PodData

		newPod.PodName = pod.Name

		index, exists := podIndex[newPod.PodName]
		if !exists {
			continue
		} else {
			// podIndex에 podName이 있을 때의 처리
			collectPodInfo = &podInfoSet[index]
		}

		newPod.StartTime = pod.Status.StartTime.Unix() // 이거... 데이터 수정해야 할듯.... startTime하고 startedAt은 수정필요
		newPod.StartedAt = collectPodInfo.Container[0].StartedAt / global.NANOSECONDS
		newPod.FinishedAt = pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Unix()

		for _, podRemoveTime := range avgRemoveTime {
			if podRemoveTime.PodName == newPod.PodName {
				newPod.RemoveTime += podRemoveTime.RemoveTime
			}
		}

		newPod.RunningTime = newPod.FinishedAt - newPod.StartedAt - newPod.RemoveTime
		//중단된 컨테이너 따로 계산해야 함... 얘가 어려움
		// Finish는 맞추고 Start를 다르게 계산해야 하며.... start가 컨테이너가 실행된 시점인지? 확인이 필요(얘만 수정하면되지않을까)

		if collectPodInfo.Container[0].NumOfRemove != 0 {
			newPod.RestartCount = int32(collectPodInfo.Container[0].PastAttempt) + int32(collectPodInfo.Container[0].NumOfRemove)
			fmt.Println(collectPodInfo.Container[0].PastAttempt, ", ", collectPodInfo.Container[0].NumOfRemove)
		} else {
			newPod.RestartCount = int32(collectPodInfo.Container[0].Attempt)
		}

		if startedTestTime > newPod.StartTime {
			startedTestTime = newPod.StartTime
		}
		if finishedTestTime < newPod.FinishedAt {
			finishedTestTime = newPod.FinishedAt
		}

		if minContainerRunningTime > newPod.RunningTime {
			minContainerRunningTime = newPod.RunningTime
		}
		if maxContainerRunningTime < newPod.RunningTime {
			maxContainerRunningTime = newPod.RunningTime
		}

		totalContinerRunningTime += newPod.RunningTime
		restartSum += newPod.RestartCount
		restartLog = append(restartLog, newPod.RestartCount)
		runningTimeArr = append(runningTimeArr, newPod.RunningTime)

		resultPod = append(resultPod, newPod)
	}

	for i, pod := range resultPod {
		var bias int64
		var err error
		bias, err = strconv.ParseInt(pod.PodName[len(pod.PodName)-3:], 10, 64)
		if err != nil {
			bias, err = strconv.ParseInt(pod.PodName[len(pod.PodName)-2:], 10, 64)
			if err != nil {
				bias, _ = strconv.ParseInt(pod.PodName[len(pod.PodName)-1:], 10, 64)
			}
		}
		pod.WaitTime = pod.StartedAt - startedTestTime - (bias / global.NUM_OF_WORKERS) + pod.RemoveTime // 말고도 중간에 퇴출되어있던 시간도 측정해야 함
		resultPod[i].WaitTime = pod.WaitTime

		if minContainerWaitTime > pod.WaitTime {
			minContainerWaitTime = pod.WaitTime
		}
		if maxContainerWaitTime < pod.WaitTime {
			maxContainerWaitTime = pod.WaitTime
		}
		totalContainerWaitTime += pod.WaitTime
		waitTimeArr = append(waitTimeArr, pod.WaitTime)
	}

	for _, time := range avgCheckpointTime {
		sumAvgCheckpointTime += time.CheckpointTime
		checkpointTimeArr = append(checkpointTimeArr, time.CheckpointTime)
	}

	for _, time := range avgImageTime {
		sumAvgImageTime += time.ImageTime
		imageTimeArr = append(imageTimeArr, time.ImageTime)
	}

	for _, time := range avgRepairTime {
		sumAvgRepairTime += time.RepairTime
		repairTimeArr = append(repairTimeArr, time.RepairTime)
	}

	fmt.Println("TotalRunningTime: ", finishedTestTime-startedTestTime)
	fmt.Println("RestartLog: ", restartLog)
	fmt.Println("TotalContainerRestart: ", restartSum)
	fmt.Println("AvgRestartCount: ", float64(restartSum)/float64(len(restartLog)))

	fmt.Println("averageContainerRunningTime:", float64(totalContinerRunningTime)/float64(len(pods.Items)))
	fmt.Println("containerRunningTimeArray:", runningTimeArr)
	fmt.Println("averageContainerWaitTime:", float64(totalContainerWaitTime)/float64(len(pods.Items)))
	fmt.Println("containerWaitTimeArray:", waitTimeArr)

	fmt.Println("AverageCheckpointTime: ", float64(sumAvgCheckpointTime)/float64(len(avgCheckpointTime)))
	fmt.Println(checkpointTimeArr)
	fmt.Println("AverageImageTime: ", float64(sumAvgImageTime)/float64(len(avgImageTime)))
	fmt.Println(imageTimeArr)
	fmt.Println("AverageRepairTime: ", float64(sumAvgRepairTime)/float64(len(avgRepairTime)))
	fmt.Println(repairTimeArr)
}

func IsSucceed(podsItems []v1.Pod) bool {
	for _, pod := range podsItems {
		if pod.Status.Phase != "Succeeded" {
			return false
		}
	}
	return true
}
