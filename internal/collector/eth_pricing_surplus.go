package collector

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus"
)

type EthPricingSurplus struct {
	rpc  *rpc.Client
	desc *prometheus.Desc
}

func NewEthPricingSurplus(rpc *rpc.Client) *EthPricingSurplus {
	return &EthPricingSurplus{
		rpc: rpc,
		desc: prometheus.NewDesc(
			"eth_pricing_surplus",
			"surplus of gas pricing",
			nil,
			nil,
		),
	}
}

func (collector *EthPricingSurplus) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.desc
}

func (collector *EthPricingSurplus) Collect(ch chan<- prometheus.Metric) {
	var result string

	// Example 2: More complete transaction object
	callParams := map[string]interface{}{
		"to":   "0x000000000000000000000000000000000000006c",
		"data": "0x520acdd7",
	}

	if err := collector.rpc.Call(&result, "eth_call", callParams, "latest"); err != nil {
		ch <- prometheus.NewInvalidMetric(collector.desc, err)
		return
	}

	// Convert hex string to signed integer, handling negative values
	// Remove 0x prefix
	hexStr := strings.TrimPrefix(result, "0x")

	// Handle edge case where result is empty
	if hexStr == "" {
		hexStr = "0"
	}

	// Parse as big.Int to handle large values and two's complement
	bigInt := new(big.Int)
	bigInt, ok := bigInt.SetString(hexStr, 16)
	if !ok {
		ch <- prometheus.NewInvalidMetric(collector.desc, strconv.ErrSyntax)
		return
	}

	// Check if this should be interpreted as a negative number (two's complement)
	// For 256-bit values, if the most significant bit is 1, it's negative
	maxUint256 := new(big.Int)
	maxUint256.SetString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 16)

	// If the value is greater than 2^255-1, treat as negative (two's complement)
	maxInt256 := new(big.Int)
	maxInt256.SetString("7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 16)

	var value float64
	if bigInt.Cmp(maxInt256) > 0 {
		// Convert from two's complement: subtract 2^256
		temp := new(big.Int).Add(maxUint256, big.NewInt(1))
		bigInt.Sub(bigInt, temp)
		value = float64(bigInt.Int64())
	} else {
		value = float64(bigInt.Int64())
	}
	ch <- prometheus.MustNewConstMetric(collector.desc, prometheus.GaugeValue, value)
}
