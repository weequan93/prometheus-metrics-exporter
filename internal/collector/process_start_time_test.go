package collector

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestProcessStartTime_Describe(t *testing.T) {
	processNames := []string{"test_process"}
	collector := NewProcessStartTime(processNames)

	ch := make(chan *prometheus.Desc, 1)
	collector.Describe(ch)

	if len(ch) != 1 {
		t.Errorf("Expected 1 descriptor, got %d", len(ch))
	}

	desc := <-ch
	if desc == nil {
		t.Error("Expected non-nil descriptor")
	}
}

func TestProcessStartTime_Collect(t *testing.T) {
	processNames := []string{"nonexistent_process"}
	collector := NewProcessStartTime(processNames)

	ch := make(chan prometheus.Metric, 10)
	collector.Collect(ch)
	close(ch)

	metrics := make([]prometheus.Metric, 0)
	for metric := range ch {
		metrics = append(metrics, metric)
	}

	if len(metrics) > 0 {
		t.Logf("Found %d metrics for nonexistent process", len(metrics))
	}
}

func TestGetBootTime(t *testing.T) {
	bootTime, err := getBootTime()
	if err != nil {
		t.Skipf("Skipping boot time test: %v", err)
	}

	if bootTime.IsZero() {
		t.Error("Boot time should not be zero")
	}

	if bootTime.After(time.Now()) {
		t.Error("Boot time should be in the past")
	}
}

func TestGetClockTicks(t *testing.T) {
	ticks := getClockTicks()
	if ticks <= 0 {
		t.Errorf("Clock ticks should be positive, got %d", ticks)
	}
}