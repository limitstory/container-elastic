package modules

import (
	global "elastic/modules/global"
	"math"
	"sort"
	"time"
)

func GetPriorityMetric(podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string, systemInfoSet []global.SystemInfo) []global.PodData {
	for _, podName := range currentRunningPods {
		pod := podInfoSet[podIndex[podName]]

		// get priority metric of containers
		for i, container := range pod.Container {
			var sumConMemUtil float64 = 0.0
			var sumNodeMemUtil float64 = 0.0
			var sumVarConMemUtil float64 = 0.0
			var sumCpuUtil float64 = 0.0
			pod.Container[i].Priority.MaxConMemUtil = float64(global.MIN_VALUE)
			pod.Container[i].Priority.MinConMemUtil = float64(global.MAX_VALUE)

			for j := 1; j <= int(container.TimeWindow); j++ {
				conMemUtil := container.Resource[len(container.Resource)-j].ConMemUtil
				nodeMemUtil := container.Resource[len(container.Resource)-j].NodeMemUtil
				cpuUtil := container.Resource[len(container.Resource)-j].CpuUtil

				sumConMemUtil += conMemUtil
				sumNodeMemUtil += nodeMemUtil
				sumCpuUtil += cpuUtil

				//연산할때마다 60초 간 메트릭을 측정하는 것은 매우 비효율적...?
				if pod.Container[i].Priority.MaxConMemUtil < conMemUtil {
					pod.Container[i].Priority.MaxConMemUtil = conMemUtil
				}
				if pod.Container[i].Priority.MinConMemUtil > conMemUtil {
					pod.Container[i].Priority.MinConMemUtil = conMemUtil
				}
			}
			pod.Container[i].Priority.AvgConMemUtil = sumConMemUtil / float64(container.TimeWindow)
			pod.Container[i].Priority.AvgNodeMemUtil = sumNodeMemUtil / float64(container.TimeWindow)
			pod.Container[i].Priority.AvgCpuUtil = sumCpuUtil / float64(container.TimeWindow)

			for j := 1; j <= int(container.TimeWindow); j++ {
				conMemUtil := pod.Container[i].Resource[len(container.Resource)-j].ConMemUtil
				sumVarConMemUtil += math.Pow(container.Priority.AvgConMemUtil-conMemUtil, 2)
			}
			pod.Container[i].Priority.VarConMemUtil = sumVarConMemUtil / float64(container.TimeWindow)

			// StartedAt이 맞는지 확인이 필요함
			// 값이 너무 작아지는데 분자를 1이아닌 어떤값으로 할지 고민 필요
			pod.Container[i].Priority.PriorityID = 1 / float64(container.StartedAt/global.NANOSECONDS)

			if global.NumOfTotalScale == 0 {
				pod.Container[i].Priority.Penalty = 0
			} else {
				pod.Container[i].Priority.Penalty = 1 - (float64(container.NumOfScale) / float64(global.NumOfTotalScale))
			}

			// 컨테이너의 실제 동작시간을 파악해야 함...(중간에 컨테이너가 삭제되었다가 재시작하는 경우...처리 완료함 (Downtime))
			pod.Container[i].Priority.Reward = float64(container.CPULimitTime) / float64(time.Now().Unix()-(container.StartedAt/global.NANOSECONDS)-container.DownTime)
		}
	}

	return podInfoSet
}

func CalculatePriority(podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string) ([]global.PodData, map[string]global.PriorityContainer, []string) {

	priorityMap := make(map[string]global.PriorityContainer)

	for _, podName := range currentRunningPods {
		pod := podInfoSet[podIndex[podName]]

		for i, container := range pod.Container {
			priority := container.Priority
			priorityElements := []float64{priority.AvgConMemUtil, priority.AvgNodeMemUtil, priority.VarConMemUtil,
				priority.AvgCpuUtil, priority.PriorityID, priority.Penalty, priority.Reward}
			var priorityScore float64 = 0.0

			// AvgConMemUtil, AvgNodeMemUtil, VarConMemUtil
			// AvgCpuUtil, PriorityID, Penalty, Reward
			for j := 0; j < len(global.PRIORITY_VECTOR); j++ {
				priorityScore += priorityElements[j] * float64(global.PRIORITY_VECTOR[j])
			}
			pod.Container[i].Priority.PriorityScore = priorityScore

			priorityContainer := global.PriorityContainer{podName, container.Name, container.Id, priorityScore}

			// 하나의 파드에 하나의 컨테이너만 동작한다고 가정한다.
			priorityMap[pod.Id] = priorityContainer
		}
	}

	sortPriority := make([]string, 0, len(priorityMap))

	for k := range priorityMap {
		// 우선순위에 따라 정렬하고 리턴
		sortPriority = append(sortPriority, k)
	}
	sort.SliceStable(sortPriority, func(i, j int) bool {
		return priorityMap[sortPriority[i]].Priority > priorityMap[sortPriority[j]].Priority
	})

	return podInfoSet, priorityMap, sortPriority
}
