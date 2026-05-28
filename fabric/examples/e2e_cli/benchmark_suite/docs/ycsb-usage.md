# YCSB Usage

All commands below assume the current working directory is `examples/e2e_cli/benchmark_suite`.

Prerequisites:

- `python3`
- `bash`
- `docker`
- `docker compose` or `docker-compose`

## Deploy

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
```

Optional overrides:

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh --channel-name mychannel --chaincode-name ycsbcc --chaincode-version 1.0
```

## Generate Dataset and Run

```bash
bash scripts/run/ycsb_load_and_run.sh --generate
```

Uniform example:

```bash
bash scripts/run/ycsb_load_and_run.sh --generate --dataset-name ycsb-uniform --key-start 1 --record-count 1000 --operation-count 5000 --distribution uniform --scan-length-min 1 --scan-length-max 8 --value-size 32 --parallelism 4 --batch-size 10
```

Zipfian example:

```bash
bash scripts/run/ycsb_load_and_run.sh --generate --dataset-name ycsb-zipf --record-count 1000 --operation-count 5000 --distribution zipfian --zipf-theta 0.99 --read-ratio 0.5 --update-ratio 0.3 --scan-ratio 0.1 --insert-ratio 0.1 --parallelism 4 --batch-size 10
```

`--key-start` is the main knob for repeated runs on the same ledger because the load phase refuses to overwrite existing keys.

## Reuse Existing Dataset

```bash
bash scripts/run/ycsb_load_and_run.sh --dataset-path datasets/generated/ycsb/ycsb-zipf
```

## Output

Results are written to:

```text
examples/e2e_cli/benchmark_suite/results/ycsb/<run_id>/
```

Important files:

- `execution.csv`
- `summary.json`
- `summary.md`
- `raw.log`
- `success_records.jsonl` (confirmed write successes)

Runtime and metric semantics:

- Reads (`Read`/`Scan`) are counted as `success` when query transport succeeds.
- Writes (`Insert`/`Update`) are recorded as `submitted` in workload stage when invoke transport succeeds.
- After workload submission ends, submitted writes are confirmed in batches by chaincode `BatchGetReceipts`.
- Writes confirmed by receipt are marked `success`; unresolved writes after bounded rounds are marked `business_failure`.

This means reported `tps`/`verified_tps` is verification-success TPS for the formal workload phase.

Post-verify controls:

- `--verify-batch-size` (default `100`): request IDs per `BatchGetReceipts` query
- `--verify-rounds` (default `3`): max confirmation rounds
- `--verify-sleep-ms` (default `500`): sleep between rounds (milliseconds)

`parallelism` is the number of concurrent worker threads. `batch_size` is only the number of rows each worker executes sequentially in one client-side chunk; it is not an on-chain batch.

`distribution` controls target-key selection for `Read`, `Update`, and `Scan`:

- `uniform`: equal-probability key selection
- `zipfian`: skewed selection with hot keys controlled by `--zipf-theta`
