package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestArbitrumNodeCollectNotSyncingWithParentLag(t *testing.T) {
	nodeServer := newArbitrumRPCServer(t, map[string]interface{}{
		"eth_blockNumber": "0x7b",
		"eth_getBlockByNumber": map[string]interface{}{
			"timestamp":     "0x3e8",
			"l1BlockNumber": "0x1f4",
		},
		"eth_syncing": false,
	})
	defer nodeServer.Close()

	parentServer := newArbitrumRPCServer(t, map[string]interface{}{
		"eth_blockNumber": "0x208",
	})
	defer parentServer.Close()

	nodeRPC, err := rpc.DialHTTP(nodeServer.URL)
	if err != nil {
		t.Fatalf("node rpc connection error: %#v", err)
	}
	parentRPC, err := rpc.DialHTTP(parentServer.URL)
	if err != nil {
		t.Fatalf("parent rpc connection error: %#v", err)
	}

	collector := NewArbitrumNode(nodeRPC, parentRPC)
	values := collectGaugeValues(t, collector)
	want := []float64{123, 1000, 500, 0, 520, 20, 20}
	assertFloatValues(t, values, want)
}

func TestArbitrumNodeCollectSyncing(t *testing.T) {
	nodeServer := newArbitrumRPCServer(t, map[string]interface{}{
		"eth_blockNumber": "0x7b",
		"eth_getBlockByNumber": map[string]interface{}{
			"timestamp":     "0x3e8",
			"l1BlockNumber": "0x1f4",
		},
		"eth_syncing": map[string]interface{}{
			"startingBlock": "0xa",
			"currentBlock":  "0x14",
			"highestBlock":  "0x1e",
		},
	})
	defer nodeServer.Close()

	nodeRPC, err := rpc.DialHTTP(nodeServer.URL)
	if err != nil {
		t.Fatalf("node rpc connection error: %#v", err)
	}

	collector := NewArbitrumNode(nodeRPC, nil)
	values := collectGaugeValues(t, collector)
	want := []float64{123, 1000, 500, 1, 10, 20, 30}
	assertFloatValues(t, values, want)
}

func collectGaugeValues(t *testing.T, collector prometheus.Collector) []float64 {
	t.Helper()

	ch := make(chan prometheus.Metric, 16)
	collector.Collect(ch)
	close(ch)

	var values []float64
	for metric := range ch {
		var dtoMetric dto.Metric
		if err := metric.Write(&dtoMetric); err != nil {
			t.Fatalf("expected metric, got %#v", err)
		}
		if dtoMetric.Gauge == nil || dtoMetric.Gauge.Value == nil {
			t.Fatalf("expected gauge metric, got %#v", dtoMetric)
		}
		values = append(values, *dtoMetric.Gauge.Value)
	}
	return values
}

func assertFloatValues(t *testing.T, got []float64, want []float64) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d values %v, want %d values %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("value %d got %v, want %v; all values %v", i, got[i], want[i], got)
		}
	}
}

func newArbitrumRPCServer(t *testing.T, results map[string]interface{}) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("could not decode request: %#v", err)
		}

		result, ok := results[request.Method]
		if !ok {
			t.Fatalf("unexpected RPC method %s", request.Method)
		}

		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  result,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("could not write response: %#v", err)
		}
	}))
}
