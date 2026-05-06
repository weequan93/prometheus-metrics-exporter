package collector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus"
)

type ArbitrumNode struct {
	rpc       *rpc.Client
	parentRPC *rpc.Client

	l2BlockNumberDesc     *prometheus.Desc
	l2BlockTimestampDesc  *prometheus.Desc
	l1BlockNumberDesc     *prometheus.Desc
	parentBlockNumberDesc *prometheus.Desc
	l1BlockLagDesc        *prometheus.Desc
	syncingDesc           *prometheus.Desc
	syncStartingBlockDesc *prometheus.Desc
	syncCurrentBlockDesc  *prometheus.Desc
	syncHighestBlockDesc  *prometheus.Desc
	batchPosterL1LagDesc  *prometheus.Desc
}

type arbitrumBlockResult struct {
	Timestamp     rpcUint64 `json:"timestamp"`
	L1BlockNumber rpcUint64 `json:"l1BlockNumber"`
}

type arbitrumSyncingResult struct {
	StartingBlock rpcUint64 `json:"startingBlock"`
	CurrentBlock  rpcUint64 `json:"currentBlock"`
	HighestBlock  rpcUint64 `json:"highestBlock"`
}

func NewArbitrumNode(rpcClient *rpc.Client, parentRPC *rpc.Client) *ArbitrumNode {
	return &ArbitrumNode{
		rpc:       rpcClient,
		parentRPC: parentRPC,
		l2BlockNumberDesc: prometheus.NewDesc(
			"arbitrum_l2_block_number",
			"number of the most recent Arbitrum L2 block",
			nil,
			nil,
		),
		l2BlockTimestampDesc: prometheus.NewDesc(
			"arbitrum_l2_block_timestamp",
			"timestamp of the most recent Arbitrum L2 block",
			nil,
			nil,
		),
		l1BlockNumberDesc: prometheus.NewDesc(
			"arbitrum_l1_block_number",
			"L1 block number referenced by the most recent Arbitrum L2 block",
			nil,
			nil,
		),
		parentBlockNumberDesc: prometheus.NewDesc(
			"arbitrum_parent_chain_block_number",
			"latest block number reported by the configured Arbitrum parent-chain RPC",
			nil,
			nil,
		),
		l1BlockLagDesc: prometheus.NewDesc(
			"arbitrum_l1_block_lag",
			"difference between parent-chain head and the L1 block referenced by the latest Arbitrum L2 block",
			nil,
			nil,
		),
		syncingDesc: prometheus.NewDesc(
			"arbitrum_syncing",
			"whether the Arbitrum node reports that it is syncing",
			nil,
			nil,
		),
		syncStartingBlockDesc: prometheus.NewDesc(
			"arbitrum_sync_starting_block",
			"Arbitrum sync starting block reported by eth_syncing",
			nil,
			nil,
		),
		syncCurrentBlockDesc: prometheus.NewDesc(
			"arbitrum_sync_current_block",
			"Arbitrum sync current block reported by eth_syncing",
			nil,
			nil,
		),
		syncHighestBlockDesc: prometheus.NewDesc(
			"arbitrum_sync_highest_block",
			"Arbitrum sync highest block reported by eth_syncing",
			nil,
			nil,
		),
		batchPosterL1LagDesc: prometheus.NewDesc(
			"arbitrum_batch_poster_l1_block_lag",
			"parent-chain head minus the L1 block referenced by the latest Arbitrum L2 block; use as a batch posting freshness signal",
			nil,
			nil,
		),
	}
}

func (collector *ArbitrumNode) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.l2BlockNumberDesc
	ch <- collector.l2BlockTimestampDesc
	ch <- collector.l1BlockNumberDesc
	ch <- collector.syncingDesc
	ch <- collector.syncStartingBlockDesc
	ch <- collector.syncCurrentBlockDesc
	ch <- collector.syncHighestBlockDesc
	ch <- collector.parentBlockNumberDesc
	ch <- collector.l1BlockLagDesc
	ch <- collector.batchPosterL1LagDesc
}

func (collector *ArbitrumNode) Collect(ch chan<- prometheus.Metric) {
	var l2BlockNumber rpcUint64
	if err := collector.rpc.Call(&l2BlockNumber, "eth_blockNumber"); err != nil {
		ch <- prometheus.NewInvalidMetric(collector.l2BlockNumberDesc, err)
	} else {
		ch <- prometheus.MustNewConstMetric(collector.l2BlockNumberDesc, prometheus.GaugeValue, float64(l2BlockNumber))
	}

	var block *arbitrumBlockResult
	if err := collector.rpc.Call(&block, "eth_getBlockByNumber", "latest", false); err != nil {
		ch <- prometheus.NewInvalidMetric(collector.l2BlockTimestampDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.l1BlockNumberDesc, err)
	} else if block == nil {
		err := fmt.Errorf("latest block response was nil")
		ch <- prometheus.NewInvalidMetric(collector.l2BlockTimestampDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.l1BlockNumberDesc, err)
	} else {
		ch <- prometheus.MustNewConstMetric(collector.l2BlockTimestampDesc, prometheus.GaugeValue, float64(block.Timestamp))
		ch <- prometheus.MustNewConstMetric(collector.l1BlockNumberDesc, prometheus.GaugeValue, float64(block.L1BlockNumber))
	}

	collector.collectSyncing(ch)

	if collector.parentRPC == nil {
		return
	}

	var parentBlockNumber rpcUint64
	if err := collector.parentRPC.Call(&parentBlockNumber, "eth_blockNumber"); err != nil {
		ch <- prometheus.NewInvalidMetric(collector.parentBlockNumberDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.l1BlockLagDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.batchPosterL1LagDesc, err)
		return
	}

	ch <- prometheus.MustNewConstMetric(collector.parentBlockNumberDesc, prometheus.GaugeValue, float64(parentBlockNumber))
	if block == nil {
		err := fmt.Errorf("latest block response was nil")
		ch <- prometheus.NewInvalidMetric(collector.l1BlockLagDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.batchPosterL1LagDesc, err)
		return
	}

	lag := float64(parentBlockNumber) - float64(block.L1BlockNumber)
	ch <- prometheus.MustNewConstMetric(collector.l1BlockLagDesc, prometheus.GaugeValue, lag)
	ch <- prometheus.MustNewConstMetric(collector.batchPosterL1LagDesc, prometheus.GaugeValue, lag)
}

func (collector *ArbitrumNode) collectSyncing(ch chan<- prometheus.Metric) {
	var raw json.RawMessage
	if err := collector.rpc.Call(&raw, "eth_syncing"); err != nil {
		ch <- prometheus.NewInvalidMetric(collector.syncingDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.syncStartingBlockDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.syncCurrentBlockDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.syncHighestBlockDesc, err)
		return
	}

	var syncing bool
	if err := json.Unmarshal(raw, &syncing); err == nil {
		value := 0.0
		if syncing {
			value = 1.0
		}
		ch <- prometheus.MustNewConstMetric(collector.syncingDesc, prometheus.GaugeValue, value)
		return
	}

	var result arbitrumSyncingResult
	if err := json.Unmarshal(raw, &result); err != nil {
		ch <- prometheus.NewInvalidMetric(collector.syncingDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.syncStartingBlockDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.syncCurrentBlockDesc, err)
		ch <- prometheus.NewInvalidMetric(collector.syncHighestBlockDesc, err)
		return
	}

	ch <- prometheus.MustNewConstMetric(collector.syncingDesc, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(collector.syncStartingBlockDesc, prometheus.GaugeValue, float64(result.StartingBlock))
	ch <- prometheus.MustNewConstMetric(collector.syncCurrentBlockDesc, prometheus.GaugeValue, float64(result.CurrentBlock))
	ch <- prometheus.MustNewConstMetric(collector.syncHighestBlockDesc, prometheus.GaugeValue, float64(result.HighestBlock))
}

type rpcUint64 uint64

func (value *rpcUint64) UnmarshalJSON(input []byte) error {
	if string(input) == "null" {
		*value = 0
		return nil
	}

	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		parsed, err := parseRPCUint64(text)
		if err != nil {
			return err
		}
		*value = rpcUint64(parsed)
		return nil
	}

	var number uint64
	if err := json.Unmarshal(input, &number); err != nil {
		return err
	}
	*value = rpcUint64(number)
	return nil
}

func parseRPCUint64(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return strconv.ParseUint(value[2:], 16, 64)
	}
	return strconv.ParseUint(value, 10, 64)
}
