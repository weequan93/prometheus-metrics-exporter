package collector

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParseNginxAccessLogLine(t *testing.T) {
	line := `10.0.0.1 - - [06/May/2026:10:00:00 +0000] "GET /rpc HTTP/1.1" 200 123 upstream="10.0.0.2:8545" rt="0.123" uct="0.001" uht="0.002" urt="0.120"`

	parsed, err := parseNginxAccessLogLine(line)
	if err != nil {
		t.Fatalf("parse error: %#v", err)
	}

	if parsed.Origin != "10.0.0.1" {
		t.Fatalf("origin got %s, want 10.0.0.1", parsed.Origin)
	}
	if parsed.Status != "200" {
		t.Fatalf("status got %s, want 200", parsed.Status)
	}
	if parsed.Upstream != "10.0.0.2:8545" {
		t.Fatalf("upstream got %s, want 10.0.0.2:8545", parsed.Upstream)
	}
	if parsed.Timings["request"] != "0.123" {
		t.Fatalf("request timing got %s, want 0.123", parsed.Timings["request"])
	}
}

func TestParseNginxAccessLogLineWithRefererAndUserAgent(t *testing.T) {
	line := `10.0.0.1 - - [06/May/2026:10:00:00 +0000] "GET /rpc HTTP/1.1" 200 123 upstream="10.0.0.2:8545" "https://example.com" "curl/8.0.0" rt="0.123" uct="0.001" uht="0.002" urt="0.120"`

	parsed, err := parseNginxAccessLogLine(line)
	if err != nil {
		t.Fatalf("parse error: %#v", err)
	}

	if parsed.Upstream != "10.0.0.2:8545" {
		t.Fatalf("upstream got %s, want 10.0.0.2:8545", parsed.Upstream)
	}
	if parsed.Timings["upstream_response"] != "0.120" {
		t.Fatalf("upstream response timing got %s, want 0.120", parsed.Timings["upstream_response"])
	}
}

func TestParseNginxTimingValues(t *testing.T) {
	sum, count, err := parseNginxTimingValues("0.100, -, 0.250")
	if err != nil {
		t.Fatalf("parse timing error: %#v", err)
	}
	if count != 2 {
		t.Fatalf("count got %d, want 2", count)
	}
	if math.Abs(sum-0.350) > 0.000001 {
		t.Fatalf("sum got %v, want 0.350", sum)
	}
}

func TestNginxAccessLogScanTracksOffsetsStateAndRotation(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "access.log")
	stateFile := filepath.Join(dir, "state.json")

	line1 := nginxTestLine("10.0.0.1", "200", "10.0.0.2:8545", "0.100")
	if err := os.WriteFile(logFile, []byte(line1), 0644); err != nil {
		t.Fatalf("write log: %#v", err)
	}

	collector, err := NewNginxAccessLog([]string{logFile}, stateFile)
	if err != nil {
		t.Fatalf("collector error: %#v", err)
	}
	state := collector.stateFor(logFile)
	if err := collector.scanFile(logFile, state); err != nil {
		t.Fatalf("scan error: %#v", err)
	}

	key1 := nginxRequestKey{File: logFile, Status: "200", Upstream: "10.0.0.2:8545", Origin: "10.0.0.1"}
	if got := state.Requests[key1]; got != 1 {
		t.Fatalf("line1 requests got %d, want 1", got)
	}
	if got := state.Offset; got != int64(len(line1)) {
		t.Fatalf("offset got %d, want %d", got, len(line1))
	}

	line2 := nginxTestLine("10.0.0.3", "502", "10.0.0.4:8545", "0.200")
	if err := os.WriteFile(logFile, []byte(line1+line2), 0644); err != nil {
		t.Fatalf("append log: %#v", err)
	}
	if err := collector.scanFile(logFile, state); err != nil {
		t.Fatalf("second scan error: %#v", err)
	}

	key2 := nginxRequestKey{File: logFile, Status: "502", Upstream: "10.0.0.4:8545", Origin: "10.0.0.3"}
	if got := state.Requests[key2]; got != 1 {
		t.Fatalf("line2 requests got %d, want 1", got)
	}

	changeKey := nginxUpstreamChangeKey{
		File:             logFile,
		PreviousUpstream: "10.0.0.2:8545",
		Upstream:         "10.0.0.4:8545",
	}
	if got := state.UpstreamChanges[changeKey]; got != 1 {
		t.Fatalf("upstream changes got %d, want 1", got)
	}

	if err := collector.saveState(); err != nil {
		t.Fatalf("save state: %#v", err)
	}
	reloaded, err := NewNginxAccessLog([]string{logFile}, stateFile)
	if err != nil {
		t.Fatalf("reload collector: %#v", err)
	}
	if got := reloaded.states[logFile].Offset; got != int64(len(line1+line2)) {
		t.Fatalf("reloaded offset got %d, want %d", got, len(line1+line2))
	}

	line3 := nginxTestLine("10.0.0.5", "200", "10.0.0.6:8545", "0.050")
	if err := os.WriteFile(logFile, []byte(line3), 0644); err != nil {
		t.Fatalf("rotate log: %#v", err)
	}
	rotatedState := reloaded.stateFor(logFile)
	if err := reloaded.scanFile(logFile, rotatedState); err != nil {
		t.Fatalf("rotation scan error: %#v", err)
	}

	key3 := nginxRequestKey{File: logFile, Status: "200", Upstream: "10.0.0.6:8545", Origin: "10.0.0.5"}
	if got := rotatedState.Requests[key3]; got != 1 {
		t.Fatalf("rotated request got %d, want 1", got)
	}
}

func nginxTestLine(origin string, status string, upstream string, requestTime string) string {
	return origin + ` - - [06/May/2026:10:00:00 +0000] "GET /rpc HTTP/1.1" ` + status + ` 123 upstream="` + upstream + `" rt="` + requestTime + `" uct="0.001" uht="0.002" urt="0.003"` + "\n"
}
