package modules

type SystemInfo struct {
	Cpu    Cpu
	Memory Memory
}

type Cpu struct {
	User    float64
	System  float64 `json:"system"`
	Nice    float64 `json:"nice"`
	Irq     float64 `json:"irq"`
	Softirq float64 `json:"softirq"`
	Steal   float64 `json:"steal"`

	Idle   float64 `json:"idle"`
	Iowait float64 `json:"iowait"`

	TotalCore      float64
	TotalMilliCore float64
}

type Memory struct {
	// Total amount of RAM on this system
	Total uint64
	// RAM available for programs to allocate
	Available uint64
	// RAM used by programs
	Used uint64
	// Percentage of RAM used by programs
	UsedPercent float64
}

