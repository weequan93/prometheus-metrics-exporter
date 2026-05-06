package collector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const maxNginxAccessLogLineBytes = 1024 * 1024

var nginxAccessLogPattern = regexp.MustCompile(`^(\S+) \S+ \S+ \[[^\]]+\] "[^"]*" (\d{3}) \S+ upstream="([^"]*)"\s*(?:(?:"[^"]*"\s*){0,2})rt="([^"]*)"\s+uct="([^"]*)"\s+uht="([^"]*)"\s+urt="([^"]*)"`)

type NginxAccessLog struct {
	patterns  []string
	statePath string

	mu          sync.Mutex
	states      map[string]*nginxAccessLogFileState
	stateErrors uint64

	requestsDesc    *prometheus.Desc
	upstreamChanges *prometheus.Desc
	durationDesc    *prometheus.Desc
	parseErrorsDesc *prometheus.Desc
	readErrorsDesc  *prometheus.Desc
	stateErrorsDesc *prometheus.Desc
	offsetDesc      *prometheus.Desc
	lastReadDesc    *prometheus.Desc
}

type nginxAccessLogFileState struct {
	Offset       int64  `json:"offset"`
	FileID       string `json:"file_id,omitempty"`
	LastUpstream string `json:"last_upstream,omitempty"`

	Requests        map[nginxRequestKey]uint64              `json:"-"`
	UpstreamChanges map[nginxUpstreamChangeKey]uint64       `json:"-"`
	Durations       map[nginxDurationKey]nginxDurationValue `json:"-"`
	ParseErrors     uint64                                  `json:"-"`
	ReadErrors      uint64                                  `json:"-"`
	LastReadUnix    int64                                   `json:"-"`
}

type nginxPersistedState struct {
	Offset       int64  `json:"offset"`
	FileID       string `json:"file_id,omitempty"`
	LastUpstream string `json:"last_upstream,omitempty"`
}

type nginxRequestKey struct {
	File     string
	Status   string
	Upstream string
	Origin   string
}

type nginxUpstreamChangeKey struct {
	File             string
	PreviousUpstream string
	Upstream         string
}

type nginxDurationKey struct {
	File     string
	Status   string
	Upstream string
	Phase    string
}

type nginxDurationValue struct {
	Sum   float64
	Count uint64
}

type nginxAccessLogLine struct {
	Origin   string
	Status   string
	Upstream string
	Timings  map[string]string
}

func NewNginxAccessLog(patterns []string, statePath string) (*NginxAccessLog, error) {
	collector := &NginxAccessLog{
		patterns:  cleanStringList(patterns),
		statePath: strings.TrimSpace(statePath),
		states:    make(map[string]*nginxAccessLogFileState),
		requestsDesc: prometheus.NewDesc(
			"nginx_access_requests_total",
			"number of parsed nginx access log requests",
			[]string{"file", "status", "upstream", "origin"},
			nil,
		),
		upstreamChanges: prometheus.NewDesc(
			"nginx_access_upstream_changes_total",
			"number of times the parsed upstream address changed between consecutive requests",
			[]string{"file", "previous_upstream", "upstream"},
			nil,
		),
		durationDesc: prometheus.NewDesc(
			"nginx_access_request_duration_seconds",
			"parsed nginx access log request durations by phase",
			[]string{"file", "status", "upstream", "phase"},
			nil,
		),
		parseErrorsDesc: prometheus.NewDesc(
			"nginx_access_log_parse_errors_total",
			"number of nginx access log lines that could not be parsed",
			[]string{"file"},
			nil,
		),
		readErrorsDesc: prometheus.NewDesc(
			"nginx_access_log_read_errors_total",
			"number of nginx access log read errors",
			[]string{"file"},
			nil,
		),
		stateErrorsDesc: prometheus.NewDesc(
			"nginx_access_log_state_errors_total",
			"number of nginx access log state file read or write errors",
			nil,
			nil,
		),
		offsetDesc: prometheus.NewDesc(
			"nginx_access_log_read_offset_bytes",
			"current byte offset read in the nginx access log",
			[]string{"file"},
			nil,
		),
		lastReadDesc: prometheus.NewDesc(
			"nginx_access_log_last_read_timestamp_seconds",
			"Unix timestamp when the nginx access log was last read",
			[]string{"file"},
			nil,
		),
	}

	if err := collector.loadState(); err != nil {
		return nil, err
	}

	return collector, nil
}

func (collector *NginxAccessLog) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.requestsDesc
	ch <- collector.upstreamChanges
	ch <- collector.durationDesc
	ch <- collector.parseErrorsDesc
	ch <- collector.readErrorsDesc
	ch <- collector.stateErrorsDesc
	ch <- collector.offsetDesc
	ch <- collector.lastReadDesc
}

func (collector *NginxAccessLog) Collect(ch chan<- prometheus.Metric) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	files, err := collector.expandFiles()
	if err != nil {
		collector.stateErrors++
		ch <- prometheus.NewInvalidMetric(collector.stateErrorsDesc, err)
	}

	for _, file := range files {
		state := collector.stateFor(file)
		if err := collector.scanFile(file, state); err != nil {
			state.ReadErrors++
			ch <- prometheus.NewInvalidMetric(collector.readErrorsDesc, fmt.Errorf("failed to read nginx log %s: %w", file, err))
		}
	}

	if err := collector.saveState(); err != nil {
		collector.stateErrors++
		ch <- prometheus.NewInvalidMetric(collector.stateErrorsDesc, err)
	}

	collector.collectState(ch)
}

func (collector *NginxAccessLog) collectState(ch chan<- prometheus.Metric) {
	files := make([]string, 0, len(collector.states))
	for file := range collector.states {
		files = append(files, file)
	}
	sort.Strings(files)

	for _, file := range files {
		state := collector.states[file]

		requestKeys := make([]nginxRequestKey, 0, len(state.Requests))
		for key := range state.Requests {
			requestKeys = append(requestKeys, key)
		}
		sort.Slice(requestKeys, func(i, j int) bool {
			return requestKeys[i].sortKey() < requestKeys[j].sortKey()
		})
		for _, key := range requestKeys {
			ch <- prometheus.MustNewConstMetric(
				collector.requestsDesc,
				prometheus.CounterValue,
				float64(state.Requests[key]),
				key.File,
				key.Status,
				key.Upstream,
				key.Origin,
			)
		}

		changeKeys := make([]nginxUpstreamChangeKey, 0, len(state.UpstreamChanges))
		for key := range state.UpstreamChanges {
			changeKeys = append(changeKeys, key)
		}
		sort.Slice(changeKeys, func(i, j int) bool {
			return changeKeys[i].sortKey() < changeKeys[j].sortKey()
		})
		for _, key := range changeKeys {
			ch <- prometheus.MustNewConstMetric(
				collector.upstreamChanges,
				prometheus.CounterValue,
				float64(state.UpstreamChanges[key]),
				key.File,
				key.PreviousUpstream,
				key.Upstream,
			)
		}

		durationKeys := make([]nginxDurationKey, 0, len(state.Durations))
		for key := range state.Durations {
			durationKeys = append(durationKeys, key)
		}
		sort.Slice(durationKeys, func(i, j int) bool {
			return durationKeys[i].sortKey() < durationKeys[j].sortKey()
		})
		for _, key := range durationKeys {
			value := state.Durations[key]
			ch <- prometheus.MustNewConstSummary(
				collector.durationDesc,
				value.Count,
				value.Sum,
				nil,
				key.File,
				key.Status,
				key.Upstream,
				key.Phase,
			)
		}

		ch <- prometheus.MustNewConstMetric(collector.parseErrorsDesc, prometheus.CounterValue, float64(state.ParseErrors), file)
		ch <- prometheus.MustNewConstMetric(collector.readErrorsDesc, prometheus.CounterValue, float64(state.ReadErrors), file)
		ch <- prometheus.MustNewConstMetric(collector.offsetDesc, prometheus.GaugeValue, float64(state.Offset), file)
		if state.LastReadUnix > 0 {
			ch <- prometheus.MustNewConstMetric(collector.lastReadDesc, prometheus.GaugeValue, float64(state.LastReadUnix), file)
		}
	}

	ch <- prometheus.MustNewConstMetric(collector.stateErrorsDesc, prometheus.CounterValue, float64(collector.stateErrors))
}

func (collector *NginxAccessLog) expandFiles() ([]string, error) {
	seen := make(map[string]struct{})
	var files []string

	for _, pattern := range collector.patterns {
		if hasGlobMeta(pattern) {
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return nil, err
			}
			for _, match := range matches {
				if _, ok := seen[match]; !ok {
					seen[match] = struct{}{}
					files = append(files, match)
				}
			}
			continue
		}

		if _, ok := seen[pattern]; !ok {
			seen[pattern] = struct{}{}
			files = append(files, pattern)
		}
	}

	sort.Strings(files)
	return files, nil
}

func (collector *NginxAccessLog) scanFile(file string, state *nginxAccessLogFileState) error {
	info, err := os.Stat(file)
	if err != nil {
		return err
	}

	currentFileID := fileID(info)
	if state.FileID != "" && currentFileID != "" && state.FileID != currentFileID {
		state.Offset = 0
		state.LastUpstream = ""
	}
	if info.Size() < state.Offset {
		state.Offset = 0
		state.LastUpstream = ""
	}
	state.FileID = currentFileID

	if info.Size() == state.Offset {
		state.LastReadUnix = time.Now().Unix()
		return nil
	}

	handle, err := os.Open(file)
	if err != nil {
		return err
	}
	defer handle.Close()

	if _, err := handle.Seek(state.Offset, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(handle, 64*1024)
	offset := state.Offset
	for {
		line, err := reader.ReadString('\n')
		if len(line) > maxNginxAccessLogLineBytes {
			state.ParseErrors++
			offset += int64(len(line))
		} else if len(line) > 0 {
			if err == io.EOF && !strings.HasSuffix(line, "\n") {
				break
			}
			offset += int64(len(line))
			collector.observeLine(file, strings.TrimRight(line, "\r\n"), state)
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	state.Offset = offset
	state.LastReadUnix = time.Now().Unix()
	return nil
}

func (collector *NginxAccessLog) observeLine(file string, line string, state *nginxAccessLogFileState) {
	parsed, err := parseNginxAccessLogLine(line)
	if err != nil {
		state.ParseErrors++
		return
	}

	requestKey := nginxRequestKey{
		File:     file,
		Status:   parsed.Status,
		Upstream: parsed.Upstream,
		Origin:   parsed.Origin,
	}
	state.Requests[requestKey]++

	if state.LastUpstream != "" && state.LastUpstream != parsed.Upstream {
		changeKey := nginxUpstreamChangeKey{
			File:             file,
			PreviousUpstream: state.LastUpstream,
			Upstream:         parsed.Upstream,
		}
		state.UpstreamChanges[changeKey]++
	}
	state.LastUpstream = parsed.Upstream

	for phase, raw := range parsed.Timings {
		sum, count, err := parseNginxTimingValues(raw)
		if err != nil {
			state.ParseErrors++
			continue
		}
		if count == 0 {
			continue
		}

		durationKey := nginxDurationKey{
			File:     file,
			Status:   parsed.Status,
			Upstream: parsed.Upstream,
			Phase:    phase,
		}
		value := state.Durations[durationKey]
		value.Sum += sum
		value.Count += uint64(count)
		state.Durations[durationKey] = value
	}
}

func parseNginxAccessLogLine(line string) (nginxAccessLogLine, error) {
	matches := nginxAccessLogPattern.FindStringSubmatch(line)
	if matches == nil {
		return nginxAccessLogLine{}, fmt.Errorf("line did not match upstream_time2 format")
	}

	upstream := strings.TrimSpace(matches[3])
	if upstream == "" {
		upstream = "-"
	}

	return nginxAccessLogLine{
		Origin:   matches[1],
		Status:   matches[2],
		Upstream: upstream,
		Timings: map[string]string{
			"request":           matches[4],
			"upstream_connect":  matches[5],
			"upstream_header":   matches[6],
			"upstream_response": matches[7],
		},
	}, nil
}

func parseNginxTimingValues(raw string) (float64, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return 0, 0, nil
	}

	var sum float64
	var count int
	values := strings.Split(raw, ",")
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "-" {
			continue
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, 0, err
		}
		sum += parsed
		count++
	}

	return sum, count, nil
}

func (collector *NginxAccessLog) loadState() error {
	if collector.statePath == "" {
		return nil
	}

	data, err := os.ReadFile(collector.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read nginx log state file: %w", err)
	}

	var persisted map[string]nginxPersistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("failed to parse nginx log state file: %w", err)
	}

	for file, state := range persisted {
		collector.states[file] = &nginxAccessLogFileState{
			Offset:          state.Offset,
			FileID:          state.FileID,
			LastUpstream:    state.LastUpstream,
			Requests:        make(map[nginxRequestKey]uint64),
			UpstreamChanges: make(map[nginxUpstreamChangeKey]uint64),
			Durations:       make(map[nginxDurationKey]nginxDurationValue),
		}
	}

	return nil
}

func (collector *NginxAccessLog) saveState() error {
	if collector.statePath == "" {
		return nil
	}

	persisted := make(map[string]nginxPersistedState, len(collector.states))
	for file, state := range collector.states {
		persisted[file] = nginxPersistedState{
			Offset:       state.Offset,
			FileID:       state.FileID,
			LastUpstream: state.LastUpstream,
		}
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode nginx log state: %w", err)
	}

	tmpPath := collector.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write nginx log state file: %w", err)
	}
	if err := os.Rename(tmpPath, collector.statePath); err != nil {
		return fmt.Errorf("failed to replace nginx log state file: %w", err)
	}

	return nil
}

func (collector *NginxAccessLog) stateFor(file string) *nginxAccessLogFileState {
	state, ok := collector.states[file]
	if ok {
		return state
	}

	state = &nginxAccessLogFileState{
		Requests:        make(map[nginxRequestKey]uint64),
		UpstreamChanges: make(map[nginxUpstreamChangeKey]uint64),
		Durations:       make(map[nginxDurationKey]nginxDurationValue),
	}
	collector.states[file] = state
	return state
}

func hasGlobMeta(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

func fileID(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}

func cleanStringList(values []string) []string {
	var cleaned []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func (key nginxRequestKey) sortKey() string {
	return key.File + "\x00" + key.Status + "\x00" + key.Upstream + "\x00" + key.Origin
}

func (key nginxUpstreamChangeKey) sortKey() string {
	return key.File + "\x00" + key.PreviousUpstream + "\x00" + key.Upstream
}

func (key nginxDurationKey) sortKey() string {
	return key.File + "\x00" + key.Status + "\x00" + key.Upstream + "\x00" + key.Phase
}
