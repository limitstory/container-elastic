package modules

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	internalapi "k8s.io/cri-api/pkg/apis"
	pb "k8s.io/cri-api/pkg/apis/runtime/v1"

	global "elastic/modules/global"
)

func GetListPodStatsInfo(client internalapi.RuntimeService) []*pb.PodSandboxStats {
	for {
		stats, err := client.ListPodSandboxStats(context.TODO(), &pb.PodSandboxStatsFilter{})
		if err != nil {
			fmt.Println(err)
			return stats
		} else {
			return stats
		}
	}
}

func GetPodStatsInfo(client internalapi.RuntimeService, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string) ([]global.PodData, []string) {

	listPodStats := GetListPodStatsInfo(client)
	isPodRunning := false

	for _, podStats := range listPodStats {
		podName := podStats.Attributes.Metadata.Name

		// Do not store namespaces other than default namespaces
		if podStats.Attributes.Metadata.Namespace != "default" {
			continue
		}
		isPodRunning = true

		// Do not store info of notworking pods
		status, _ := client.PodSandboxStatus(context.TODO(), podStats.Attributes.Id, false)
		if status == nil { // exception handling: nil pointer
			continue
		}
		if status.Status.State == 1 { // exception handling: SANDBOX_NOTREADY
			continue
		}

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
		getContainerStatsInfo(client, podStats, pod, podName)
	}

	if !isPodRunning {
		fmt.Println("There is no pod running.")
		//os.Exit(0)
	}

	return podInfoSet, currentRunningPods
}

func InitPodData(podName string, podIndex map[string]int64, podInfoSet []global.PodData, podStats *pb.PodSandboxStats) []global.PodData {
	var podInfo global.PodData

	podInfo.Id = podStats.Attributes.Id
	podInfo.Name = podStats.Attributes.Metadata.Name
	podInfo.Uid = podStats.Attributes.Metadata.Uid
	podInfo.Namespace = podStats.Attributes.Metadata.Namespace

	podIndex[podName] = int64(len(podInfoSet))
	podInfoSet = append(podInfoSet, podInfo) // append dynamic array

	return podInfoSet
}

func getContainerStatsInfo(client internalapi.RuntimeService, podStats *pb.PodSandboxStats, pod *global.PodData, podName string) {

	for _, containerStats := range podStats.Linux.Containers {
		containerName := containerStats.Attributes.Metadata.Name

		var containerResource global.ContainerResourceData

		// init container data
		if _, exists := pod.ContainerIndex[containerName]; !exists {
			pod.Container = InitContainerData(client, pod, containerName, containerStats, podName)
			//exception handling
			if pod.Container == nil {
				continue
			}
		}
		container := &pod.Container[pod.ContainerIndex[containerName]]

		// update restore container data
		if pod.IsRepairPod {
			if container.Id != containerStats.Attributes.Id {
				fmt.Println("Trigger!")
				container = UpdateRepairnitContainerData(client, container, containerName, containerStats, pod)
			}
		}

		/*
			// 컨테이너 아이디 변경되었는가?? (재시작으로 인해... 발생한 경우)
			if container.Id != containerStats.Attributes.Id {
				fmt.Println("Trigger!")
				container.Id = containerStats.Attributes.Id
				// 다른 자료구조에도 바뀌었음을 전달해야 함
				//다른곳에도 전달이 필요할 것이고, 컨테이너 사이즈 이런거 리셋됬을 것인데 어떻게 할 것인데?
			}*/

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

func UpdateRepairnitContainerData(client internalapi.RuntimeService, container *global.ContainerData, containerName string, containerStats *pb.ContainerStats, pod *global.PodData) *global.ContainerData {

	container.Id = containerStats.Attributes.Id
	container.Name = containerStats.Attributes.Metadata.Name
	container.Resource = make([]global.ContainerResourceData, 0)
	container.Cgroup.MemoryLimitInBytes = int64(float64(container.Cgroup.MemoryLimitInBytes) * 1.1)
	//container.Priority
	//NumOfScale int64
	//TimeAfterScaleup int64
	//container.CPULimitTime

	container.TimeWindow = 0
	container.NumOfRemove = pod.RepairData.ContainerData.NumOfRemove
	container.DownTime += pod.RepairData.CheckpointData.RemoveEndTime - pod.RepairData.CheckpointData.RemoveEndTime
	// reset repair data
	pod.RepairData = global.PauseContainer{}

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

func UpdatePodData(client internalapi.RuntimeService, repairContainerCandidate global.PauseContainer, podIndex map[string]int64, podInfoSet []global.PodData) []global.PodData {

	podName := repairContainerCandidate.PodName
	var podId string

	for {
		// 1초 이내에 보통 상황 종료됨
		command := "kubectl get po " + podName + " -o json | jq '.metadata.annotations.\"cni.projectcalico.org/containerID\"'"
		out, _ := exec.Command("bash", "-c", command).Output()
		strout := string(out[:])

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
	containerData.CreatedAt = containerStatus.Status.CreatedAt
	containerData.StartedAt = containerStatus.Status.StartedAt
	containerData.FinishedAt = containerStatus.Status.FinishedAt

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
