package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/31z4/ethereum-prometheus-exporter/internal/collector"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var version = "undefined"

func main() {
	flag.Usage = func() {
		const (
			usage = "Usage: ethereum_exporter [option] [arg]\n\n" +
				"Prometheus exporter for Ethereum client metrics\n\n" +
				"Options and arguments:\n"
		)

		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()

		os.Exit(2)
	}

	url := flag.String("url", "http://localhost:8545", "Ethereum JSON-RPC URL")
	addr := flag.String("addr", ":9368", "listen address")
	processes := flag.String("processes", "", "comma-separated list of process names to monitor start times")
	evmNode := flag.Bool("evm", false, "enable EVM node collectors (block number, timestamp)")
	ethNode := flag.Bool("eth", false, "enable full Ethereum node collectors (all metrics)")
	arbitrumNode := flag.Bool("arbitrum", false, "enable Arbitrum node collectors")
	arbitrumParentURL := flag.String("arbitrum-parent-url", "", "optional Arbitrum parent-chain JSON-RPC URL for L1 lag metrics")
	nginxNode := flag.Bool("nginx", false, "enable nginx access log collectors")
	nginxLogFiles := flag.String("nginx-log-files", "", "comma-separated nginx access log files or glob patterns using the upstream_time2 log format")
	nginxStateFile := flag.String("nginx-state-file", "", "optional state file for nginx access log offsets")
	ver := flag.Bool("v", false, "print version number and exit")

	flag.Parse()
	if len(flag.Args()) > 0 {
		flag.Usage()
	}

	if *ver {
		fmt.Println(version)
		os.Exit(0)
	}

	nodeTypes := 0
	for _, enabled := range []bool{*ethNode, *evmNode, *arbitrumNode} {
		if enabled {
			nodeTypes++
		}
	}
	if nodeTypes > 1 {
		log.Fatal("only one of -eth, -evm, or -arbitrum can be enabled")
	}

	var rpcClient *rpc.Client
	if *ethNode || *evmNode || *arbitrumNode {
		var err error
		rpcClient, err = rpc.Dial(*url)
		if err != nil {
			log.Fatal(err)
		}
	}

	registry := prometheus.NewPedanticRegistry()
	var collectors []prometheus.Collector

	if *ethNode {
		// Full Ethereum node includes all metrics
		collectors = append(collectors,
			collector.NewNetPeerCount(rpcClient),
			collector.NewEthBlockNumber(rpcClient),
			collector.NewEthBlockTimestamp(rpcClient),
			collector.NewEthGasPrice(rpcClient),
			collector.NewEthEarliestBlockTransactions(rpcClient),
			collector.NewEthLatestBlockTransactions(rpcClient),
			collector.NewEthPendingBlockTransactions(rpcClient),
			collector.NewEthHashrate(rpcClient),
			collector.NewEthSyncing(rpcClient),
			collector.NewParityNetPeers(rpcClient),
		)
	} else if *evmNode {
		// EVM node only includes basic metrics
		collectors = append(collectors,
			collector.NewEthBlockNumber(rpcClient),
			collector.NewEthBlockTimestamp(rpcClient),
			collector.NewEthPricingSurplus(rpcClient),
		)
	}

	if *arbitrumNode {
		var parentRPC *rpc.Client
		if strings.TrimSpace(*arbitrumParentURL) != "" {
			var err error
			parentRPC, err = rpc.Dial(*arbitrumParentURL)
			if err != nil {
				log.Fatal(err)
			}
		}
		collectors = append(collectors, collector.NewArbitrumNode(rpcClient, parentRPC))
	}

	if *nginxNode || strings.TrimSpace(*nginxLogFiles) != "" {
		logFiles := splitCommaSeparated(*nginxLogFiles)
		if len(logFiles) == 0 {
			log.Fatal("-nginx requires -nginx-log-files")
		}

		nginxCollector, err := collector.NewNginxAccessLog(logFiles, *nginxStateFile)
		if err != nil {
			log.Fatal(err)
		}
		collectors = append(collectors, nginxCollector)
	}

	if *processes != "" {
		processNames := splitCommaSeparated(*processes)
		collectors = append(collectors, collector.NewProcessStartTime(processNames))
	}

	registry.MustRegister(collectors...)

	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:      log.New(os.Stderr, log.Prefix(), log.Flags()),
		ErrorHandling: promhttp.ContinueOnError,
	})

	http.Handle("/metrics", handler)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
