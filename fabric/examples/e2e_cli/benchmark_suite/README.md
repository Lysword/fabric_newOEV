# Benchmark Suite

This directory contains the Fabric v1.0 benchmark suite for `smallbank` and `ycsb`.

Prerequisites:

- `python3`
- `bash`
- `docker`
- `docker compose` or `docker-compose`

All commands in this README assume the current working directory is:

```bash
examples/e2e_cli/benchmark_suite
```

## Layout

- `chaincode/`: benchmark chaincode implementations
- `scripts/deploy/`: network startup and chaincode deployment scripts
- `scripts/run/`: dataset import and benchmark execution scripts
- `datasets/`: generators, templates, and generated datasets
- `configs/`: default runtime configuration
- `results/`: local benchmark output
- `docs/`: usage guides and asset inventory

Important documents:

- `docs/benchmark-assets.md`: persistent file inventory for the benchmark suite
- `docs/architecture-overview-zh.md`: Chinese overview of the benchmark architecture
- `docs/workflow-details-zh.md`: Chinese step-by-step workflow explanation
- `docs/experiment-record-template-zh.md`: Chinese experiment record template
- `docs/Linux系统运行与测试说明.md`: Chinese Linux run and test guide
- `docs/linux-run-checklist.md`: Linux copy and execution checklist
- `docs/smallbank-usage.md`: Smallbank parameter and execution guide
- `docs/ycsb-usage.md`: YCSB parameter and execution guide

## Quick Start

Smallbank:

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
bash scripts/run/smallbank_load_and_run.sh --generate
```

YCSB:

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
bash scripts/run/ycsb_load_and_run.sh --generate
```

Stop the network:

```bash
bash scripts/deploy/network_down.sh
```

## Notes

- Results are written under `results/smallbank/<run_id>` or `results/ycsb/<run_id>`.
- Generated datasets are written under `datasets/generated/`.
- Workload stage uses lightweight client-side observation:
  - read operations are counted as success when query transport succeeds
  - write operations are first marked `submitted`
- After workload submission finishes, submitted writes are confirmed by batched receipt checks (`BatchGetReceipts`) with bounded rounds and sleep.
- Mixed read/write TPS definition:
  - `tps = overall_success_ops / elapsed_seconds`
  - `overall_success_ops` includes:
    - successful reads (reads are treated as normal success in mixed workloads),
    - validation-success writes.
- Success-rate definition:
  - `success_rate = overall_success_ops / attempted_ops`
  - denominator is total initiated operations (read + write).
- `pending_submitted_ops` counts write operations that were accepted by invoke but still not confirmed during the bounded post-verify window.
- `pending_submitted_ops` is tracked separately and is not forced into `business_failures` or `system_failures`.
- `write_submitted_ops` counts write rows with non-empty `request_id` (write invoke accepted by CLI).
- `write_verified_success_ops` counts writes finally marked `success`.
- `confirm_rate = write_verified_success_ops / write_submitted_ops`
- For MVCC/EOV-style conflict scenarios, you can run post-run recount to wait longer and then compute metrics:
  - Script: `scripts/common/recount_success.py`
  - It rechecks pending `submitted` writes by receipts and writes:
    - `recount_summary.json`
    - `recount_summary.md`
  - It also reports:
    - `confirmed_tps = overall_success_ops / total_execution_elapsed_seconds`
    - `total_execution_elapsed_seconds` is strictly the workload execution window (`max(end_ts) - min(start_ts)`), excluding recount wait time.

Example recount command:

```bash
python3 scripts/common/recount_success.py \
  --result-dir results/smallbank/<run_id> \
  --channel-name mychannel \
  --chaincode-name smallbankcc \
  --verify-batch-size 100 \
  --timeout-seconds 300 \
  --poll-interval-ms 1000 \
  --update-execution
```
- `parallelism` is the number of concurrent worker threads in `run_benchmark.py`.
- `batch_size` is the number of dataset rows each worker processes sequentially before the next worker task is scheduled. It is a client-side chunk size, not a Fabric batch transaction.
- `verify_batch_size` controls receipt check batch size in post-verify stage.
- `verify_rounds` controls max post-verify rounds.
- `verify_sleep_ms` controls sleep interval between rounds.
- The load phase refuses to overwrite existing ledger state. Reusing the same channel and chaincode normally requires a fresh network or a dataset with a disjoint account/key range.
- The deploy flow now uses higher default compose client timeouts to reduce `Read timed out` failures during `docker-compose up -d`. You can still override `COMPOSE_HTTP_TIMEOUT` and `DOCKER_CLIENT_TIMEOUT` manually.
- The deploy health check now retries the post-instantiate `Ping` query and verifies both the instantiating peer and the benchmark query peer to tolerate delayed chaincode availability after instantiation.
- Confirmed write successes are appended to `success_records.jsonl` under each run result directory.
- Use `--help` on each public script to inspect supported parameters.
