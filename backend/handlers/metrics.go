package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type SystemMetrics struct {
	Timestamp     time.Time `json:"timestamp"`
	CPUPercent    float64   `json:"cpu_percent"`
	Load1         float64   `json:"load_1"`
	NumCPU        int       `json:"num_cpu"`
	MemoryUsedMB  uint64    `json:"memory_used_mb"`
	MemoryTotalMB uint64    `json:"memory_total_mb"`
	MemoryPercent float64   `json:"memory_percent"`
	DiskUsedGB    uint64    `json:"disk_used_gb"`
	DiskTotalGB   uint64    `json:"disk_total_gb"`
	DiskPercent   float64   `json:"disk_percent"`
	GoHeapAllocMB uint64    `json:"go_heap_alloc_mb"`
	GoGoroutines  int       `json:"go_goroutines"`
}

func SystemMetricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	m := collectSystemMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func collectSystemMetrics() SystemMetrics {
	numCPU := runtime.NumCPU()
	load1 := readLoad1()
	memTotal, memAvailable := readMemInfo()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	diskTotal, diskFree := readDisk("/")

	memUsed := uint64(0)
	if memTotal > memAvailable {
		memUsed = memTotal - memAvailable
	}
	diskUsed := uint64(0)
	if diskTotal > diskFree {
		diskUsed = diskTotal - diskFree
	}

	return SystemMetrics{
		Timestamp:     time.Now(),
		CPUPercent:    clampPercent((load1 / float64(maxInt(numCPU, 1))) * 100),
		Load1:         load1,
		NumCPU:        numCPU,
		MemoryUsedMB:  memUsed / 1024 / 1024,
		MemoryTotalMB: memTotal / 1024 / 1024,
		MemoryPercent: percent(memUsed, memTotal),
		DiskUsedGB:    diskUsed / 1024 / 1024 / 1024,
		DiskTotalGB:   diskTotal / 1024 / 1024 / 1024,
		DiskPercent:   percent(diskUsed, diskTotal),
		GoHeapAllocMB: mem.Alloc / 1024 / 1024,
		GoGoroutines:  runtime.NumGoroutine(),
	}
}

func readLoad1() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func readMemInfo() (total, available uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(parts[1], 10, 64)
		switch strings.TrimSuffix(parts[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		}
	}
	return total, available
}

func readDisk(path string) (total, free uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bavail * uint64(stat.Bsize)
	return total, free
}

func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return clampPercent((float64(used) / float64(total)) * 100)
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
