# Linux Run Checklist

This checklist is for copying the benchmark assets from the current workspace to a Linux experiment machine and running them there.

## 1. Files To Copy

Copy the following from the Fabric repository:

- `examples/e2e_cli/docker-compose-cli.yaml`
- `examples/e2e_cli/benchmark_suite/`

The `docker-compose-cli.yaml` change is required because it mounts:

- `./benchmark_suite:/opt/gopath/src/github.com/hyperledger/fabric/examples/e2e_cli/benchmark_suite`

## 2. Linux Prerequisites

Install or verify:

- `bash`
- `python3`
- `docker`
- `docker compose` or `docker-compose`

The target Linux machine should contain a usable Hyperledger Fabric v1.0 `examples/e2e_cli` environment, including:

- `generateArtifacts.sh`
- `base/docker-compose-base.yaml`
- `crypto-config/` generation support
- `channel-artifacts/` generation support

## 3. Recommended Copy Location

Place the copied files back into the same relative repository layout:

```text
<fabric-repo>/
  examples/
    e2e_cli/
      docker-compose-cli.yaml
      benchmark_suite/
```

This keeps the mounted GOPATH-style chaincode path valid inside the `cli` container.

## 4. Make Scripts Executable

Run inside `examples/e2e_cli/benchmark_suite`:

```bash
chmod +x scripts/deploy/*.sh
chmod +x scripts/run/*.sh
chmod +x scripts/common/*.sh
```

## 5. Smallbank Execution

Deploy:

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
```

Generate dataset and run:

```bash
bash scripts/run/smallbank_load_and_run.sh --generate
```

Example with custom account range and contention:

```bash
bash scripts/run/smallbank_load_and_run.sh \
  --generate \
  --dataset-name sb-exp-1 \
  --account-start-id 100000 \
  --account-count 1000 \
  --operation-count 10000 \
  --conflict-rate 0.8 \
  --hot-account-ratio 0.2 \
  --max-debit-fraction 0.15 \
  --parallelism 4 \
  --batch-size 10 \
  --verify-batch-size 100 \
  --verify-rounds 3 \
  --verify-sleep-ms 500
```

Stop the network when needed:

```bash
bash scripts/deploy/network_down.sh
```

## 6. YCSB Execution

Deploy:

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
```

Generate dataset and run:

```bash
bash scripts/run/ycsb_load_and_run.sh --generate
```

Uniform example:

```bash
bash scripts/run/ycsb_load_and_run.sh \
  --generate \
  --dataset-name ycsb-uniform-exp \
  --key-start 1 \
  --record-count 1000 \
  --operation-count 10000 \
  --distribution uniform \
  --read-ratio 0.5 \
  --update-ratio 0.3 \
  --insert-ratio 0.1 \
  --scan-ratio 0.1 \
  --parallelism 4 \
  --batch-size 10 \
  --verify-batch-size 100 \
  --verify-rounds 3 \
  --verify-sleep-ms 500
```

Zipfian example:

```bash
bash scripts/run/ycsb_load_and_run.sh \
  --generate \
  --dataset-name ycsb-zipf-exp \
  --key-start 200000 \
  --record-count 1000 \
  --operation-count 10000 \
  --distribution zipfian \
  --zipf-theta 0.99 \
  --read-ratio 0.5 \
  --update-ratio 0.3 \
  --insert-ratio 0.1 \
  --scan-ratio 0.1 \
  --parallelism 4 \
  --batch-size 10 \
  --verify-batch-size 100 \
  --verify-rounds 3 \
  --verify-sleep-ms 500
```

If Docker Compose reports `Read timed out` during startup, you can explicitly increase the client-side timeout before rerunning:

```bash
export COMPOSE_HTTP_TIMEOUT=300
export DOCKER_CLIENT_TIMEOUT=300
```

## 7. Reusing A Generated Dataset

Smallbank:

```bash
bash scripts/run/smallbank_load_and_run.sh --dataset-path datasets/generated/smallbank/<dataset_name>
```

YCSB:

```bash
bash scripts/run/ycsb_load_and_run.sh --dataset-path datasets/generated/ycsb/<dataset_name>
```

## 8. Result Files

Smallbank results:

```text
examples/e2e_cli/benchmark_suite/results/smallbank/<run_id>/
```

YCSB results:

```text
examples/e2e_cli/benchmark_suite/results/ycsb/<run_id>/
```

Each run directory contains:

- `run_manifest.json`
- `execution.csv`
- `summary.json`
- `summary.md`
- `raw.log`
- `load_metrics.json`

## 9. Measuring TPS And Success Rate

The main metrics are written locally after each run:

- `summary.json`
- `summary.md`

Definitions used by the runner:

- `successful_ops`: final successes in the formal workload phase (reads transport-success + writes receipt-confirmed)
- `business_failures`: application-level failures
- `system_failures`: transport or infrastructure failures
- `read_success_ops`: read successes counted in workload stage
- `write_submitted_ops`: write invokes accepted in workload stage
- `write_verified_success_ops`: writes confirmed by post-verify receipt checks
- `confirm_rate`: `write_verified_success_ops / write_submitted_ops`
- `verified_tps` (`tps`): verification-success TPS for the formal workload phase
- `load_tps`: TPS for the initial dataset import phase
- `workload_elapsed_seconds`: workload submission stage elapsed time (TPS denominator)
- `verify_elapsed_seconds`: post-verify stage elapsed time
- `total_wall_seconds`: total run elapsed time

Timing is measured on the benchmark client side in `run_benchmark.py` using millisecond timestamps per operation and per load phase.

## 10. Repeated Runs On The Same Ledger

The load phase refuses to overwrite existing business state.

For repeated runs on the same deployed chaincode, either:

- restart with a fresh network, or
- use a disjoint key/account range

Main range controls:

- Smallbank: `--account-start-id`
- YCSB: `--key-start`

If you want to restart from a fresh network:

```bash
bash scripts/deploy/network_down.sh
```

If you also want compose-managed Docker volumes removed:

```bash
bash scripts/deploy/network_down.sh --remove-volumes
```

## 11. Static Validation Status

In the current Windows workspace, static validation has been completed for:

- Python benchmark scripts and dataset generators by `python -m py_compile`
- chaincode logic by source review
- chaincode tests by source review

Linux runtime verification still needs to be performed on the actual experiment machine.
