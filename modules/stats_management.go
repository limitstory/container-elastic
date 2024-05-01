package modules

import (
	"context"
	"fmt"

	internalapi "k8s.io/cri-api/pkg/apis"
	pb "k8s.io/cri-api/pkg/apis/runtime/v1"

	global "elastic/modules/global"
)

func MonitoringPodResources(client internalapi.RuntimeService, podIndex map[string]int64, podInfoSet []global.PodData, currentRunningPods []string, systemInfoSet []global.SystemInfo) ([]global.PodData, []string) {
	//var containerResourceSet = make([]*pb.ContainerResources, 0) //Dynamic array to store container system metric

	// Slice operates on pointer basis(call by reference).
	// get pod stats
	podInfoSet, currentRunningPods = GetPodStatsInfo(client, podIndex, podInfoSet, currentRunningPods)

	for _, podName := range currentRunningPods {
		pod := podInfoSet[podIndex[podName]]

		for _, container := range pod.Container {
			// exception handling
			if len(container.Resource) == 0 {
				continue
			}

			// Need a pointer to change the value
			res := &container.Resource[len(container.Resource)-1]
			sysres := systemInfoSet[len(systemInfoSet)-1]

			res.CpuUsageCoreMilliSeconds = res.CpuUsageCoreNanoSeconds / uint64(global.NANOCORES_TO_MILLICORES) //nanocores to millicores
			if container.Cgroup.MemoryLimitInBytes == 0 {
				res.ConMemUtil = -1
			} else {
				res.ConMemUtil = float64(res.MemoryUsageBytes) / float64(container.Cgroup.MemoryLimitInBytes)
			}
			res.NodeMemUtil = float64(res.MemoryUsageBytes) / float64(sysres.Memory.Total)

			if len(container.Resource) <= 1 {
				res.CpuUtil = 0.0
			} else {
				preres := container.Resource[len(container.Resource)-2]
				presysres := systemInfoSet[len(systemInfoSet)-2]

				res.CpuUtil = float64(res.CpuUsageCoreMilliSeconds-preres.CpuUsageCoreMilliSeconds) / (sysres.Cpu.TotalMilliCore - presysres.Cpu.TotalMilliCore)
			}
		}
	}

	return podInfoSet, currentRunningPods

	// get container stats
	//podInfoSet, containerResourceSet = GetContainerStatsInfo(client, podInfoSet, containerResourceSet)

	// get memory usage percents each containers
	//podInfoSet = GetmemoryUsagePercents(podInfoSet)

}

func UpdateContainerResources(client internalapi.RuntimeService, id string, resource *pb.ContainerResources) {

	err := client.UpdateContainerResources(context.TODO(), id, resource)
	if err != nil {
		fmt.Println(err)
	}
}

/*
func LimitContainerResources(client internalapi.RuntimeService, selectContainerId []string, selectContainerResource []*pb.ContainerResources) ([]string, []*pb.ContainerResources) {

	var selectMemoryUsagePercents float64
	var indexOfSelectContainers int32

	// Monitoring Pod Resources
	podInfoSet, containerResourceSet := MonitoringPodResources(client)

	// select restrict containers
	selectMemoryUsagePercents, indexOfSelectContainers, selectContainerId = SelectRestrictContainers(podInfoSet, selectContainerId)

	// kill the last restricted container because all containers are restrict
	// update selectContainerId and selectContainerResource
	if selectMemoryUsagePercents == 100.00 {
		RemoveContainer(client, selectContainerId, selectContainerResource)

		return selectContainerId, selectContainerResource
	}

	// append select containers
	selectContainerId = append(selectContainerId, podInfoSet[indexOfSelectContainers].ContainerData.Id)
	selectContainerResource = append(selectContainerResource, containerResourceSet[indexOfSelectContainers])

	fmt.Printf("\nRestrict ContainerId:%s, UsedPercent:%f%%\n", podInfoSet[indexOfSelectContainers].ContainerData.Id, selectMemoryUsagePercents*100)

	// limit CPU usage for containers with the low memory usage percents
	selectContainerResource[len(selectContainerResource)-1].Linux.CpuQuota = LIMIT_CPU_QUOTA // limit cpu usage to 10m
	UpdateContainerResources(client, selectContainerId[len(selectContainerId)-1], selectContainerResource[len(selectContainerResource)-1])

	return selectContainerId, selectContainerResource
}

func ControlRecursiveContainerResources(client internalapi.RuntimeService, selectContainerId []string, selectContainerResource []*pb.ContainerResources) {
	// get system metrics & memory usage exceeds to threshold value
	timeout := MonitoringSystemResources(true)

	if timeout == 1 { // when timeout occurs, additional containers are restricted and monitor system resource again
		selectContainerId, selectContainerResource = LimitContainerResources(client, selectContainerId, selectContainerResource)
		// recursive하게 호출
		ControlRecursiveContainerResources(client, selectContainerId, selectContainerResource)
	} else { // revert CPU usage of all containers if memory usage is low
		for i := 0; i < len(selectContainerId); i++ {
			selectContainerResource[i].Linux.CpuQuota = DEFAULT_CPU_QUOTA
			UpdateContainerResources(client, selectContainerId[i], selectContainerResource[i])
			fmt.Printf("\nRelease ContainerId:%s\n", selectContainerId[i])
		}
	}
}*/
