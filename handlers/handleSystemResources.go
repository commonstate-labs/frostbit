package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type systemResourcesResponse struct {
	CollectedAtUnixMs     int64    `json:"collected_at_unix_ms"`
	ProcessCPUPercent     *float64 `json:"process_cpu_pct,omitempty"`
	ProcessMemoryBytes    uint64   `json:"process_memory_bytes,omitempty"`
	GoHeapAllocBytes      uint64   `json:"go_heap_alloc_bytes,omitempty"`
	GoHeapSysBytes        uint64   `json:"go_heap_sys_bytes,omitempty"`
	GoHeapInuseBytes      uint64   `json:"go_heap_inuse_bytes,omitempty"`
	GoRoutines            int      `json:"go_routines,omitempty"`
	HostCPUPercent        *float64 `json:"host_cpu_pct,omitempty"`
	HostMemTotalBytes     uint64   `json:"host_mem_total_bytes,omitempty"`
	HostMemUsedBytes      uint64   `json:"host_mem_used_bytes,omitempty"`
	HostMemAvailableBytes uint64   `json:"host_mem_available_bytes,omitempty"`
	DiskPath              string   `json:"disk_path,omitempty"`
	DiskTotalBytes        uint64   `json:"disk_total_bytes,omitempty"`
	DiskUsedBytes         uint64   `json:"disk_used_bytes,omitempty"`
	DiskFreeBytes         uint64   `json:"disk_free_bytes,omitempty"`
	Error                 string   `json:"error,omitempty"`
}

type systemResourceRawSample struct {
	at                    time.Time
	processCPUSeconds     float64
	hasProcessCPUSeconds  bool
	processCPUPercent     *float64
	processMemoryBytes    uint64
	hostCPUPercent        *float64
	hostCPUTotal          uint64
	hostCPUIdle           uint64
	hasHostCPUTicks       bool
	hostMemTotalBytes     uint64
	hostMemAvailableBytes uint64
	diskPath              string
	diskTotalBytes        uint64
	diskFreeBytes         uint64
	goHeapAllocBytes      uint64
	goHeapSysBytes        uint64
	goHeapInuseBytes      uint64
	goRoutines            int
	errors                []string
}

type systemResourceCollector struct {
	mu       sync.Mutex
	hasLast  bool
	last     systemResourceRawSample
	cachedAt time.Time
	cached   *systemResourcesResponse
}

var nodeSystemResourceCollector systemResourceCollector

func (hm *HandlerManager) HandleSystemResources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	hm.Stats.RecordAPICall("HandleSystemResources")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	resp := nodeSystemResourceCollector.Collect(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *systemResourceCollector) Collect(ctx context.Context) systemResourcesResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && time.Since(c.cachedAt) < 1500*time.Millisecond {
		return *c.cached
	}
	raw := collectSystemResourceRaw(ctx)
	resp := buildSystemResourcesResponse(raw, c.last, c.hasLast)
	c.last = raw
	c.hasLast = true
	c.cachedAt = time.Now()
	c.cached = &resp
	return resp
}

func buildSystemResourcesResponse(raw, prev systemResourceRawSample, hasPrev bool) systemResourcesResponse {
	resp := systemResourcesResponse{
		CollectedAtUnixMs:     raw.at.UnixMilli(),
		ProcessMemoryBytes:    raw.processMemoryBytes,
		GoHeapAllocBytes:      raw.goHeapAllocBytes,
		GoHeapSysBytes:        raw.goHeapSysBytes,
		GoHeapInuseBytes:      raw.goHeapInuseBytes,
		GoRoutines:            raw.goRoutines,
		HostMemTotalBytes:     raw.hostMemTotalBytes,
		HostMemAvailableBytes: raw.hostMemAvailableBytes,
		DiskPath:              raw.diskPath,
		DiskTotalBytes:        raw.diskTotalBytes,
		DiskFreeBytes:         raw.diskFreeBytes,
		Error:                 strings.Join(raw.errors, "; "),
	}
	if raw.hostMemTotalBytes >= raw.hostMemAvailableBytes {
		resp.HostMemUsedBytes = raw.hostMemTotalBytes - raw.hostMemAvailableBytes
	}
	if raw.diskTotalBytes >= raw.diskFreeBytes {
		resp.DiskUsedBytes = raw.diskTotalBytes - raw.diskFreeBytes
	}
	if raw.processCPUPercent != nil {
		resp.ProcessCPUPercent = cleanPercentPtr(*raw.processCPUPercent, false)
	} else if hasPrev && raw.hasProcessCPUSeconds && prev.hasProcessCPUSeconds {
		elapsed := raw.at.Sub(prev.at).Seconds()
		if elapsed > 0 {
			resp.ProcessCPUPercent = cleanPercentPtr((raw.processCPUSeconds-prev.processCPUSeconds)/elapsed*100, false)
		}
	}
	if raw.hostCPUPercent != nil {
		resp.HostCPUPercent = cleanPercentPtr(*raw.hostCPUPercent, true)
	} else if hasPrev && raw.hasHostCPUTicks && prev.hasHostCPUTicks && raw.hostCPUTotal > prev.hostCPUTotal {
		totalDelta := raw.hostCPUTotal - prev.hostCPUTotal
		idleDelta := raw.hostCPUIdle - prev.hostCPUIdle
		if totalDelta > 0 && totalDelta >= idleDelta {
			resp.HostCPUPercent = cleanPercentPtr(float64(totalDelta-idleDelta)/float64(totalDelta)*100, true)
		}
	}
	return resp
}

func collectSystemResourceRaw(ctx context.Context) systemResourceRawSample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	raw := systemResourceRawSample{
		at:               time.Now(),
		diskPath:         resourceDiskPath(),
		goHeapAllocBytes: ms.HeapAlloc,
		goHeapSysBytes:   ms.HeapSys,
		goHeapInuseBytes: ms.HeapInuse,
		goRoutines:       runtime.NumGoroutine(),
	}
	switch runtime.GOOS {
	case "linux":
		collectLinuxResources(ctx, &raw)
	case "darwin":
		collectDarwinResources(ctx, &raw)
	case "windows":
		collectWindowsResources(ctx, &raw)
	default:
		raw.errors = append(raw.errors, "unsupported os: "+runtime.GOOS)
	}
	return raw
}

func collectLinuxResources(ctx context.Context, raw *systemResourceRawSample) {
	if rss, err := linuxProcessRSS(); err == nil {
		raw.processMemoryBytes = rss
	} else {
		raw.errors = append(raw.errors, "linux rss: "+err.Error())
	}
	if seconds, err := linuxProcessCPUSeconds(); err == nil {
		raw.processCPUSeconds = seconds
		raw.hasProcessCPUSeconds = true
	} else {
		raw.errors = append(raw.errors, "linux proc cpu: "+err.Error())
	}
	if total, idle, err := linuxHostCPUTicks(); err == nil {
		raw.hostCPUTotal, raw.hostCPUIdle, raw.hasHostCPUTicks = total, idle, true
	} else {
		raw.errors = append(raw.errors, "linux host cpu: "+err.Error())
	}
	if total, avail, err := linuxHostMemory(); err == nil {
		raw.hostMemTotalBytes, raw.hostMemAvailableBytes = total, avail
	} else {
		raw.errors = append(raw.errors, "linux mem: "+err.Error())
	}
	collectDFDisk(ctx, raw)
}

func collectDarwinResources(ctx context.Context, raw *systemResourceRawSample) {
	if cpu, rss, err := psProcessStats(ctx); err == nil {
		raw.processCPUPercent = cleanPercentPtr(cpu, false)
		raw.processMemoryBytes = rss
	} else {
		raw.errors = append(raw.errors, "darwin ps: "+err.Error())
	}
	if cpu, err := darwinHostCPU(ctx); err == nil {
		raw.hostCPUPercent = cleanPercentPtr(cpu, true)
	} else {
		raw.errors = append(raw.errors, "darwin cpu: "+err.Error())
	}
	if total, avail, err := darwinHostMemory(ctx); err == nil {
		raw.hostMemTotalBytes, raw.hostMemAvailableBytes = total, avail
	} else {
		raw.errors = append(raw.errors, "darwin mem: "+err.Error())
	}
	collectDFDisk(ctx, raw)
}

func collectWindowsResources(ctx context.Context, raw *systemResourceRawSample) {
	stats, err := windowsResourceStats(ctx, raw.diskPath)
	if err != nil {
		raw.errors = append(raw.errors, "windows resources: "+err.Error())
		return
	}
	raw.processCPUSeconds = stats.ProcessCPUSeconds
	raw.hasProcessCPUSeconds = true
	raw.processMemoryBytes = stats.ProcessMemoryBytes
	raw.hostCPUPercent = cleanPercentPtr(stats.HostCPUPercent, true)
	raw.hostMemTotalBytes = stats.HostMemTotalBytes
	raw.hostMemAvailableBytes = stats.HostMemAvailableBytes
	raw.diskTotalBytes = stats.DiskTotalBytes
	raw.diskFreeBytes = stats.DiskFreeBytes
}

func linuxProcessRSS() (uint64, error) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("invalid statm")
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * uint64(os.Getpagesize()), nil
}

func linuxProcessCPUSeconds() (float64, error) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, err
	}
	text := string(data)
	end := strings.LastIndex(text, ")")
	if end < 0 || len(text) <= end+2 {
		return 0, fmt.Errorf("invalid proc stat")
	}
	fields := strings.Fields(text[end+2:])
	if len(fields) < 13 {
		return 0, fmt.Errorf("short proc stat")
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, err
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, err
	}
	return float64(utime+stime) / 100, nil
}

func linuxHostCPUTicks() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "cpu ") {
		return 0, 0, fmt.Errorf("missing cpu line")
	}
	var total, idle uint64
	fields := strings.Fields(lines[0])[1:]
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total, idle, nil
}

func linuxHostMemory() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.ParseUint(parts[1], 10, 64)
		if err == nil {
			values[strings.TrimSuffix(parts[0], ":")] = v * 1024
		}
	}
	total := values["MemTotal"]
	avail := values["MemAvailable"]
	if avail == 0 {
		avail = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("missing MemTotal")
	}
	return total, minUint64(avail, total), nil
}

func psProcessStats(ctx context.Context) (float64, uint64, error) {
	out, err := runCommand(ctx, "ps", "-p", strconv.Itoa(os.Getpid()), "-o", "%cpu=,rss=")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("invalid ps output")
	}
	cpu, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return 0, 0, err
	}
	rssKB, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return cpu, rssKB * 1024, nil
}

func darwinHostCPU(ctx context.Context) (float64, error) {
	out, err := runCommand(ctx, "top", "-l", "1", "-s", "0", "-n", "0")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "CPU usage:") {
			if idle := parsePercentBefore(line, "% idle"); idle != nil {
				return 100 - *idle, nil
			}
		}
	}
	return 0, fmt.Errorf("missing CPU usage")
}

func darwinHostMemory(ctx context.Context) (uint64, uint64, error) {
	totalOut, err := runCommand(ctx, "sysctl", "-n", "hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	total, err := strconv.ParseUint(strings.TrimSpace(totalOut), 10, 64)
	if err != nil {
		return 0, 0, err
	}
	vmOut, err := runCommand(ctx, "vm_stat")
	if err != nil {
		return total, 0, err
	}
	pageSize := uint64(os.Getpagesize())
	var freePages, inactivePages, speculativePages uint64
	for _, line := range strings.Split(vmOut, "\n") {
		if strings.Contains(line, "page size of") {
			if v := firstUintInString(line); v > 0 {
				pageSize = v
			}
			continue
		}
		v := lastUintInString(line)
		switch {
		case strings.HasPrefix(line, "Pages free:"):
			freePages = v
		case strings.HasPrefix(line, "Pages inactive:"):
			inactivePages = v
		case strings.HasPrefix(line, "Pages speculative:"):
			speculativePages = v
		}
	}
	avail := (freePages + inactivePages + speculativePages) * pageSize
	return total, minUint64(avail, total), nil
}

type windowsResourceJSON struct {
	ProcessCPUSeconds     float64 `json:"process_cpu_seconds"`
	ProcessMemoryBytes    uint64  `json:"process_memory_bytes"`
	HostCPUPercent        float64 `json:"host_cpu_pct"`
	HostMemTotalBytes     uint64  `json:"host_mem_total_bytes"`
	HostMemAvailableBytes uint64  `json:"host_mem_available_bytes"`
	DiskTotalBytes        uint64  `json:"disk_total_bytes"`
	DiskFreeBytes         uint64  `json:"disk_free_bytes"`
}

func windowsResourceStats(ctx context.Context, diskPath string) (windowsResourceJSON, error) {
	drive := filepath.VolumeName(diskPath)
	if drive == "" {
		drive = "C:"
	}
	drive = strings.ReplaceAll(drive, "'", "''")
	script := fmt.Sprintf(`$ErrorActionPreference='Stop';
$p=Get-Process -Id %d;
$os=Get-CimInstance Win32_OperatingSystem;
$cpu=(Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average).Average;
$disk=Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='%s'";
$pcpu=if($null -eq $p.CPU){0}else{[double]$p.CPU};
[pscustomobject]@{
process_cpu_seconds=$pcpu;
process_memory_bytes=[uint64]$p.WorkingSet64;
host_cpu_pct=[double]$cpu;
host_mem_total_bytes=[uint64]($os.TotalVisibleMemorySize*1024);
host_mem_available_bytes=[uint64]($os.FreePhysicalMemory*1024);
disk_total_bytes=[uint64]$disk.Size;
disk_free_bytes=[uint64]$disk.FreeSpace
} | ConvertTo-Json -Compress`, os.Getpid(), drive)
	out, err := runPowerShell(ctx, script)
	if err != nil {
		return windowsResourceJSON{}, err
	}
	var stats windowsResourceJSON
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		return windowsResourceJSON{}, err
	}
	return stats, nil
}

func collectDFDisk(ctx context.Context, raw *systemResourceRawSample) {
	out, err := runCommand(ctx, "df", "-kP", raw.diskPath)
	if err != nil {
		raw.errors = append(raw.errors, "df: "+err.Error())
		return
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		raw.errors = append(raw.errors, "df: short output")
		return
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		raw.errors = append(raw.errors, "df: invalid output")
		return
	}
	totalKB, err1 := strconv.ParseUint(fields[1], 10, 64)
	usedKB, err2 := strconv.ParseUint(fields[2], 10, 64)
	freeKB, err3 := strconv.ParseUint(fields[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		raw.errors = append(raw.errors, "df: parse failed")
		return
	}
	raw.diskTotalBytes = totalKB * 1024
	raw.diskFreeBytes = freeKB * 1024
	if usedKB > 0 && raw.diskTotalBytes >= raw.diskFreeBytes {
		raw.diskTotalBytes = maxUint64(raw.diskTotalBytes, (usedKB+freeKB)*1024)
	}
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, name, args...).CombinedOutput()
	if cmdCtx.Err() != nil {
		return "", cmdCtx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return string(out), nil
}

func runPowerShell(ctx context.Context, script string) (string, error) {
	out, err := runCommand(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err == nil {
		return out, nil
	}
	return runCommand(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}

func resourceDiskPath() string {
	if wd, err := os.Getwd(); err == nil {
		if abs, err := filepath.Abs(wd); err == nil {
			return abs
		}
		return wd
	}
	return os.TempDir()
}

func cleanPercentPtr(v float64, clamp100 bool) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	if v < 0 {
		v = 0
	}
	if clamp100 && v > 100 {
		v = 100
	}
	return &v
}

func parsePercentBefore(s, marker string) *float64 {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return nil
	}
	end := idx
	for end > 0 && s[end-1] == ' ' {
		end--
	}
	start := end
	for start > 0 {
		c := s[start-1]
		if (c >= '0' && c <= '9') || c == '.' {
			start--
			continue
		}
		break
	}
	if start == end {
		return nil
	}
	v, err := strconv.ParseFloat(s[start:end], 64)
	if err != nil {
		return nil
	}
	return &v
}

func firstUintInString(s string) uint64 {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		if f == "" {
			continue
		}
		v, _ := strconv.ParseUint(f, 10, 64)
		return v
	}
	return 0
}

func lastUintInString(s string) uint64 {
	var last uint64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		if f == "" {
			continue
		}
		if v, err := strconv.ParseUint(f, 10, 64); err == nil {
			last = v
		}
	}
	return last
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
