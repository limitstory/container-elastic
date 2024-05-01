package modules

const ENDPOINT string = "unix:///var/run/crio/crio.sock"
const NODENAME string = "limitstory-virtualbox"

const MAX_VALUE int = 10000
const MIN_VALUE int = -10000

const DEFAULT_CPU_QUOTA int64 = 20000
const LIMIT_CPU_QUOTA int64 = 1000

const CORES_TO_MILLICORES float64 = 1000.0
const NANOCORES_TO_MILLICORES int64 = 1000000
const NANOSECONDS int64 = 1000000000

const MAX_TIME_WINDOW int64 = 60
const SCALE_DOWN_THRESHOLD int = 10

const CONTAINER_MEMORY_SLO_UPPER float64 = 0.001
const CONTAINER_MEMORY_SLO float64 = 0.001
const CONTAINER_MEMORY_SLO_LOWER float64 = 0.03
const MAX_MEMORY_USAGE_THRESHOLD float64 = 0.97

const CONTAINER_MEMORY_USAGE_THRESHOLD float64 = 0.001

const MIN_SIZE_PER_CONTAINER int64 = 300 * 1048576 // 1Mibibyte = 1024*1024 = 1048576

const TIMEOUT_INTERVAL int32 = 3

// AvgConMemUtil, AvgNodeMemUtil, VarConMemUtil
// AvgCpuUtil, PriorityID, Penalty, Reward
var PRIORITY_VECTOR = []int64{1, 1, 1, 1, 1, 1, 1}

var NumOfTotalScale int64 = 0

type PriorityContainer struct {
	PodName       string
	ContainerName string
	ContainerId   string
	Priority      float64
}

type ScaleCandidateContainer struct {
	PodName       string
	PodId         string
	ContainerName string
	ContainerId   string
	ContainerData *ContainerData
	ScaleSize     int64
}

type PauseContainer struct {
	PodName               string
	PodId                 string
	ContainerName         string
	ContainerId           string
	DuringCheckpoint      bool
	IsCheckpoint          bool
	DuringCreateContainer bool
	CreateContainer       bool
	ContainerData         *ContainerData
	CheckpointData        CheckpointMetaData
}

type CheckpointMetaData struct {
	CheckpointName     string
	MemoryLimitInBytes int64
	RemoveStartTime    int64
	RemoveEndTime      int64
}
