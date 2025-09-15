package collector

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ProcessStartTime struct {
	processNames []string
	desc         *prometheus.Desc
}

func NewProcessStartTime(processNames []string) *ProcessStartTime {
	return &ProcessStartTime{
		processNames: processNames,
		desc: prometheus.NewDesc(
			"process_start_time_seconds",
			"Process start time in seconds since epoch",
			[]string{"process_name", "pid"},
			nil,
		),
	}
}

func (collector *ProcessStartTime) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.desc
}

func (collector *ProcessStartTime) Collect(ch chan<- prometheus.Metric) {
	for _, processName := range collector.processNames {
		processes, err := findProcessesByName(processName)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(collector.desc, fmt.Errorf("failed to find processes for %s: %w", processName, err))
			continue
		}

		for _, proc := range processes {
			startTime, err := getProcessStartTime(proc.PID)
			if err != nil {
				ch <- prometheus.NewInvalidMetric(collector.desc, fmt.Errorf("failed to get start time for PID %d: %w", proc.PID, err))
				continue
			}

			ch <- prometheus.MustNewConstMetric(
				collector.desc,
				prometheus.GaugeValue,
				float64(startTime.Unix()),
				processName,
				strconv.Itoa(proc.PID),
			)
		}
	}
}

type Process struct {
	PID  int
	Name string
}

func findProcessesByName(processName string) ([]Process, error) {
	var processes []Process

	procDir := "/proc"
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return processes, nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		cmdlinePath := filepath.Join(procDir, entry.Name(), "cmdline")
		cmdlineBytes, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		cmdline := string(cmdlineBytes)
		if cmdline == "" {
			continue
		}

		args := strings.Split(strings.TrimSuffix(cmdline, "\x00"), "\x00")
		if len(args) == 0 {
			continue
		}

		execName := filepath.Base(args[0])
		if strings.Contains(execName, processName) || strings.Contains(cmdline, processName) {
			processes = append(processes, Process{
				PID:  pid,
				Name: execName,
			})
		}
	}

	return processes, nil
}

func getProcessStartTime(pid int) (time.Time, error) {
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	file, err := os.Open(statPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open %s: %w", statPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return time.Time{}, fmt.Errorf("failed to read stat file for PID %d", pid)
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 22 {
		return time.Time{}, fmt.Errorf("invalid stat file format for PID %d", pid)
	}

	startTimeJiffies, err := strconv.ParseUint(fields[21], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse start time for PID %d: %w", pid, err)
	}

	bootTime, err := getBootTime()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get boot time: %w", err)
	}

	clockTicks := getClockTicks()
	startTimeSeconds := float64(startTimeJiffies) / float64(clockTicks)
	startTime := bootTime.Add(time.Duration(startTimeSeconds * float64(time.Second)))

	return startTime, nil
}

func getBootTime() (time.Time, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open /proc/stat: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				bootTimeUnix, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return time.Time{}, fmt.Errorf("failed to parse boot time: %w", err)
				}
				return time.Unix(bootTimeUnix, 0), nil
			}
		}
	}

	return time.Time{}, fmt.Errorf("boot time not found in /proc/stat")
}

func getClockTicks() int64 {
	return 100
}