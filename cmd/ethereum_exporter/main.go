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
	ver := flag.Bool("v", false, "print version number and exit")

	flag.Parse()
	if len(flag.Args()) > 0 {
		flag.Usage()
	}

	if *ver {
		fmt.Println(version)
		os.Exit(0)
	}

	rpc, err := rpc.Dial(*url)
	if err != nil {
		log.Fatal(err)
	}

	registry := prometheus.NewPedanticRegistry()
	var collectors []prometheus.Collector

	if *ethNode {
		// Full Ethereum node includes all metrics
		collectors = append(collectors,
			collector.NewNetPeerCount(rpc),
			collector.NewEthBlockNumber(rpc),
			collector.NewEthBlockTimestamp(rpc),
			collector.NewEthGasPrice(rpc),
			collector.NewEthEarliestBlockTransactions(rpc),
			collector.NewEthLatestBlockTransactions(rpc),
			collector.NewEthPendingBlockTransactions(rpc),
			collector.NewEthHashrate(rpc),
			collector.NewEthSyncing(rpc),
			collector.NewParityNetPeers(rpc),
		)
	} else if *evmNode {
		// EVM node only includes basic metrics
		collectors = append(collectors,
			collector.NewEthBlockNumber(rpc),
			collector.NewEthBlockTimestamp(rpc),
		)
	}

	if *processes != "" {
		processNames := strings.Split(*processes, ",")
		for i, name := range processNames {
			processNames[i] = strings.TrimSpace(name)
		}
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
