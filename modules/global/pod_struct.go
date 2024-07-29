package modules

import (
	pb "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type PodDataSet struct {
	podIndex   map[string]int64
	podInfoSet []PodData
}

type PodData struct {
	Id string
	// Pod name of the sandbox. Same as the pod name in the Pod ObjectMeta.
	Name string
	// Pod UID of the sandbox. Same as the pod UID in the Pod ObjectMeta.
	Uid string
	// Pod namespace of the sandbox. Same as the pod namespace in the Pod ObjectMeta.
	Namespace string

	ContainerIndex map[string]int64
	Container      []ContainerData

	IsRepairPod bool
	RepairData  CheckpointContainer

	RequestMemory       int64
	RepairRequestMemory int64
}

type ContainerData struct {
	// ID of the container.
	Id string
	// Name of the container. Same as the container name in the PodSpec.
	Name string
	// Attempt number of creating the container. Default: 0.
	Attempt uint32

	PastAttempt uint32

	CreatedAt int64

	StartedAt int64

	FinishedAt int64

	Resource []ContainerResourceData

	Cgroup CgroupResourceData

	Priority PriorityData

	OriginalContainerData *pb.ContainerResources

	NumOfScale int64

	TimeAfterScaleup int64

	CPULimitTime int64

	DownTime int64

	TimeWindow int64

	NumOfRemove int64
}

type ContainerResourceData struct {
	// Cumulative CPU usage (sum across all cores) since object creation.
	CpuUsageCoreNanoSeconds uint64
	// Total CPU usage (sum of all cores) averaged over the sample window.
	// The "core" unit can be interpreted as CPU core-nanoseconds per second.
	CpuUsageNanoCores uint64
	// The amount of working set memory in bytes.
	MemoryWorkingSetBytes uint64
	// Available memory for use. This is defined as the memory limit - workingSetBytes.
	MemoryAvailableBytes uint64
	// Total memory in use. This includes all memory regardless of when it was accessed.
	MemoryUsageBytes uint64
	// The amount of anonymous and swap cache memory (includes transparent hugepages).
	MemoryRssBytes uint64
	// Cumulative number of minor page faults.
	PageFaults uint64
	// Cumulative number of major page faults.
	MajorPageFaults uint64

	//custom resources.
	// nano cores to millicores
	CpuUsageCoreMilliSeconds uint64
	// CpuUsageCoreNanoSeconds / NodeCpuCore
	CpuUtil float64
	// MemoryUsageBytes / MemoryLimitInBytes
	ConMemUtil float64
	// MemoryUsageBytes / NodeMemorySize
	NodeMemUtil float64
}

type CgroupResourceData struct {
	// CPU CFS (Completely Fair Scheduler) period. Default: 0 (not specified).
	CpuPeriod int64
	// CPU CFS (Completely Fair Scheduler) quota. Default: 0 (not specified).
	CpuQuota int64
	// CPU shares (relative weight vs. other containers). Default: 0 (not specified).
	CpuShares int64
	// Memory limit in bytes. Default: 0 (not specified).
	MemoryLimitInBytes int64
	// OOMScoreAdj adjusts the oom-killer score. Default: 0 (not specified).
	OomScoreAdj int64
	// CpusetCpus constrains the allowed set of logical CPUs. Default: "" (not specified).
	CpusetCpus string
	// CpusetMems constrains the allowed set of memory nodes. Default: "" (not specified).
	CpusetMems string
}

type PriorityData struct {
	// Calculated Priority Score
	PriorityScore float64
	// Lowest memory utilization since scale event (Max_time_window: t(?))
	MinConMemUtil float64
	// Average memory utilization since scale event (Max_time_window: t)
	// ∑((MemoryUsageBytes_t / MemoryLimitInBytes_t)/|T|)
	AvgConMemUtil float64
	// Highest memory utilization since scale event (Max_time_window: t(?))
	MaxConMemUtil float64
	// Variance on memory utilization since scale event (Max_time_window: t)
	// ∑((AvgConMemUtili - MemUtil_t)^2/|T|)
	VarConMemUtil float64
	// Average node memory utilization since scale event (Max_time_window: t)
	// ∑((MemoryUsageBytes_t / NodeMemorySizes_t)/|T|)
	AvgNodeMemUtil float64
	// Average Cpu utilization since scale evnet (Max_time_window: t)
	// ∑((CpuUtil_t)/|T|)
	AvgCpuUtil float64

	// ID --> 1/Timestamp
	PriorityID float64
	// Penalty = 1 - (NumOfScale/NumOfTotalScale)
	Penalty float64
	// CPULimitTime / ContainerRunningTime
	Reward float64

	// 중단 회수 및 중단시간은  고려하지 않았는데... 해야되나??
}
