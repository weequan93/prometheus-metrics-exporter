# Ethereum Prometheus Exporter

[![CircleCI](https://circleci.com/gh/31z4/ethereum-prometheus-exporter.svg?style=shield&circle-token=3c4469ca8c3360117a7b843958e5537fa2530682)](https://circleci.com/gh/31z4/ethereum-prometheus-exporter)
[![codecov](https://codecov.io/gh/31z4/ethereum-prometheus-exporter/branch/master/graph/badge.svg)](https://codecov.io/gh/31z4/ethereum-prometheus-exporter)
[![Go Report Card](https://goreportcard.com/badge/github.com/31z4/ethereum-prometheus-exporter)](https://goreportcard.com/report/github.com/31z4/ethereum-prometheus-exporter)

This service exports various metrics from Ethereum clients for consumption by [Prometheus](https://prometheus.io). It uses [JSON-RPC](https://github.com/ethereum/wiki/wiki/JSON-RPC) interface to collect the metrics. Any JSON-RPC 2.0 enabled client should be supported. Although, it has only been tested with [OpenEthereum](https://openethereum.github.io/).

## Usage

You can deploy this exporter using the [31z4/ethereum-prometheus-exporter](https://hub.docker.com/r/31z4/ethereum-prometheus-exporter/) Docker image.

    docker run -d -p 9368:9368 --name ethereum-exporter 31z4/ethereum-prometheus-exporter -url http://ethereum:8545

Keep in mind that your container needs to be able to communicate with the Ethereum client using the specified `url` (default is `http://localhost:8545`).

By default the exporter serves on `:9368` at `/metrics`. The listen address can be changed by specifying the `-addr` flag.

### Node Type Configuration

Configure which metrics to collect based on your node type:

```bash
# EVM-compatible node (basic metrics: block number, timestamp)
ethereum_exporter -evm -url http://localhost:8545

# Full Ethereum node (all metrics: peers, gas price, transactions, etc.)
ethereum_exporter -eth -url http://localhost:8545

# Arbitrum Nitro node with parent-chain lag metrics
ethereum_exporter -arbitrum \
  -url http://localhost:8547 \
  -arbitrum-parent-url http://l1-node:8545

# Combine with process monitoring
ethereum_exporter -eth -processes "geth,parity" -url http://localhost:8545

# EVM node with process monitoring (e.g., Polygon, BSC)
ethereum_exporter -evm -processes "polygon,bsc" -url http://localhost:8545
```

**Flags:**
- `-evm`: Enable basic EVM node collectors (block number, timestamp)
- `-eth`: Enable full Ethereum node collectors (all available metrics)
- `-arbitrum`: Enable Arbitrum Nitro collectors
- `-arbitrum-parent-url`: Optional parent-chain JSON-RPC URL used to calculate L1 block lag and batch posting freshness
- **Note**: `-eth`, `-evm`, and `-arbitrum` are mutually exclusive. Use `-eth` for full Ethereum nodes, `-evm` for EVM-compatible chains, and `-arbitrum` for Nitro nodes.
- Node type flags can be combined with `-processes` and `-nginx` for process and access-log monitoring

### Nginx Access Log Monitoring

The exporter can tail nginx access logs that use this log format:

```nginx
log_format upstream_time2 '$remote_addr - $remote_user [$time_local] '
                          '"$request" $status $body_bytes_sent '
                          'upstream="$upstream_addr" '
                          '"$http_referer" "$http_user_agent" '
                          'rt="$request_time" uct="$upstream_connect_time" '
                          'uht="$upstream_header_time" urt="$upstream_response_time"';
```

Run it with:

```bash
ethereum_exporter \
  -nginx \
  -nginx-log-files "/var/log/nginx/access.log" \
  -nginx-state-file "/state/nginx-access-log.json"
```

`-nginx-log-files` accepts comma-separated files or glob patterns. `-nginx-state-file` is optional, but recommended in Docker so the exporter can resume from the previous byte offset after restarts. The collector handles common log rotation cases by resetting the offset when the active file changes or is truncated.

The nginx metrics label by `origin` and `upstream`, so keep the monitored log scope narrow enough to avoid excessive Prometheus label cardinality.

### Process Monitoring

You can also monitor process start times by specifying a comma-separated list of process names using the `-processes` flag:

```bash
# Monitor start times for geth and parity processes
ethereum_exporter -url http://localhost:8545 -processes "geth,parity,ethereum"

# Docker example with process monitoring
docker run -d -p 9368:9368 --name ethereum-exporter \
  -v /proc:/proc:ro \
  31z4/ethereum-prometheus-exporter \
  -url http://ethereum:8545 \
  -processes "geth,parity"
```

**Note**: Process monitoring requires access to the `/proc` filesystem and is only supported on Linux systems. For Docker deployments, mount `/proc` as a read-only volume.

### Docker Compose Example

A complete `docker-compose.yml` example for an Arbitrum Nitro node, nginx logs, process monitoring, and Prometheus:

```yaml
services:
  metrics-exporter:
    image: quanquanah/prometheus-metrics-exporter:dev
    container_name: prometheus-metrics-exporter
    user: "0:0"
    pid: host
    ports:
      - "6071:9368"
    volumes:
      - /var/log/nginx:/var/log/nginx:ro
      - exporter-state:/state
    command: [
      "-url", "http://arbitrum-node:8547",
      "-arbitrum",
      "-arbitrum-parent-url", "http://l1-node:8545",
      "-processes", "nitro",
      "-nginx",
      "-nginx-log-files", "/var/log/nginx/access.log",
      "-nginx-state-file", "/state/nginx-access-log.json"
    ]
    restart: unless-stopped

  prometheus:
    image: prom/prometheus:latest
    container_name: prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
    restart: unless-stopped

volumes:
  exporter-state:
```

Start with: `docker-compose up -d`

The same configuration is available as [`examples/docker-compose.arbitrum-nginx.yml`](examples/docker-compose.arbitrum-nginx.yml). The container runs as root in that example because host nginx logs and `/proc` entries are commonly not readable by the image's default `nobody` user.

### Building Docker Image

To build your own Docker image with the latest changes:

```bash
# Build for current platform (Mac)
docker build -t quanquanah/prometheus-metrics-exporter:dev .

# Build for Linux x64 (when building on Mac for Linux deployment)
docker buildx build --platform linux/amd64 -t quanquanah/prometheus-metrics-exporter:dev .

# Build and push multi-platform image to registry
docker buildx build --platform linux/amd64,linux/arm64 -t quanquanah/prometheus-metrics-exporter:dev --push .

docker image push  quanquanah/prometheus-metrics-exporter:dev

# Run with your custom image
docker run -d -p 6071:9368 --name ethereum-exporter \
  -v /proc:/proc:ro \
  ethereum-prometheus-exporter \
  -url http://11.201.0.111:8449 \
  -eth \
  -processes "geth,parity,ethereum"

# Or update docker-compose.yml to use your custom image
# Replace: image: 31z4/ethereum-prometheus-exporter
# With:    image: ethereum-prometheus-exporter
```

**Note**: When building on Mac for Linux deployment, use `--platform linux/amd64` to ensure compatibility with Linux servers.

Here is an example [`scrape_config`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#scrape_config) for Prometheus.

```yaml
- job_name: ethereum
  static_configs:
  - targets:
    - ethereum-exporter:9368
```

## Exported Metrics

| Name | Description | Flag Required |
| ---- | ----------- | ------------- |
| eth_block_number | Number of the most recent block. | `-evm` or `-eth` |
| eth_block_timestamp | Timestamp of the most recent block. | `-evm` or `-eth` |
| net_peers | Number of peers currently connected to the client. | `-eth` |
| eth_gas_price | Current gas price in wei. *Might be inaccurate*. | `-eth` |
| eth_earliest_block_transactions | Number of transactions in the earliest block. | `-eth` |
| eth_latest_block_transactions | Number of transactions in the latest block. | `-eth` |
| eth_pending_block_transactions | The number of transactions in pending block. | `-eth` |
| eth_hashrate | Hashes per second that this node is mining with. | `-eth` |
| eth_sync_starting | Block number at which current import started. | `-eth` |
| eth_sync_current | Number of most recent block. | `-eth` |
| eth_sync_highest | Estimated number of highest block. | `-eth` |
| parity_net_active_peers | Number of active peers. *Available only for OpenEthereum*. | `-eth` |
| parity_net_connected_peers | Number of peers currently connected to this client. *Available only for OpenEthereum*. | `-eth` |
| process_start_time_seconds | Process start time in seconds since epoch. Labels: `process_name`, `pid`. | `-processes` |
| arbitrum_l2_block_number | Number of the most recent Arbitrum L2 block. | `-arbitrum` |
| arbitrum_l2_block_timestamp | Timestamp of the most recent Arbitrum L2 block. | `-arbitrum` |
| arbitrum_l1_block_number | L1 block number referenced by the latest Arbitrum L2 block. | `-arbitrum` |
| arbitrum_syncing | Whether the Arbitrum node reports it is syncing. | `-arbitrum` |
| arbitrum_sync_starting_block | Arbitrum sync starting block. | `-arbitrum` |
| arbitrum_sync_current_block | Arbitrum sync current block. | `-arbitrum` |
| arbitrum_sync_highest_block | Arbitrum sync highest block. | `-arbitrum` |
| arbitrum_parent_chain_block_number | Latest parent-chain block number. | `-arbitrum-parent-url` |
| arbitrum_l1_block_lag | Parent-chain head minus the latest L2 block's referenced L1 block. | `-arbitrum-parent-url` |
| arbitrum_batch_poster_l1_block_lag | Same lag exposed as a batch posting freshness signal. | `-arbitrum-parent-url` |
| nginx_access_requests_total | Parsed nginx requests. Labels: `file`, `status`, `upstream`, `origin`. | `-nginx` |
| nginx_access_upstream_changes_total | Consecutive request upstream changes. Labels: `file`, `previous_upstream`, `upstream`. | `-nginx` |
| nginx_access_request_duration_seconds | Parsed nginx timings by `phase`, exported as Prometheus summary `_sum` and `_count` series. | `-nginx` |
| nginx_access_log_parse_errors_total | Nginx log lines or timing values that could not be parsed. | `-nginx` |
| nginx_access_log_read_errors_total | Nginx log read errors. | `-nginx` |
| nginx_access_log_state_errors_total | Nginx state file read or write errors. | `-nginx` |
| nginx_access_log_read_offset_bytes | Current byte offset read in each nginx log file. | `-nginx` |
| nginx_access_log_last_read_timestamp_seconds | Last successful nginx log read timestamp. | `-nginx` |

## Development

[Go modules](https://github.com/golang/go/wiki/Modules) is used for dependency management. Hence Go 1.11 is a minimum required version.

## Contributing

Contributions are greatly appreciated. The project follows the typical GitHub pull request model. Before starting any work, please either comment on an existing issue or file a new one.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
