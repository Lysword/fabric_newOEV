# Smallbank Usage

All commands below assume the current working directory is `examples/e2e_cli/benchmark_suite`.

Prerequisites:

- `python3`
- `bash`
- `docker`
- `docker compose` or `docker-compose`

## Deploy

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
```

Optional overrides:

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh --channel-name mychannel --chaincode-name smallbankcc --chaincode-version 1.0
```

## Generate Dataset and Run

```bash
bash scripts/run/smallbank_load_and_run.sh --generate
```

Parameter example:

```bash
bash scripts/run/smallbank_load_and_run.sh --generate --dataset-name sb-500 --account-start-id 10001 --account-count 500 --operation-count 2000 --conflict-rate 0.8 --hot-account-ratio 0.2 --max-debit-fraction 0.15 --parallelism 4 --batch-size 10
```

Recommended low-failure example (high initial balances + conservative debit):

```bash
bash scripts/run/smallbank_load_and_run.sh --generate --dataset-name sb-safe --account-start-id 10001 --account-count 500 --operation-count 2000 --conflict-rate 0.8 --hot-account-ratio 0.2 --max-debit-fraction 0.05 --parallelism 4 --batch-size 10
```

`--account-start-id` is the main knob for repeated runs on the same ledger because the load phase refuses to overwrite existing accounts.

## Reuse Existing Dataset

```bash
bash scripts/run/smallbank_load_and_run.sh --dataset-path datasets/generated/smallbank/sb-500
```

## Output

Results are written to:

```text
examples/e2e_cli/benchmark_suite/results/smallbank/<run_id>/
```

Important files:

- `execution.csv`
- `summary.json`
- `summary.md`
- `raw.log`
- `success_records.jsonl` (confirmed write successes)

Runtime and metric semantics:

- Reads (`Balance`) are counted as `success` when query transport succeeds.
- Writes are recorded as `submitted` in workload stage when invoke transport succeeds.
- After workload submission ends, submitted writes are confirmed in batches by chaincode `BatchGetReceipts`.
- Writes confirmed by receipt are marked `success`; unresolved writes after bounded rounds are marked `business_failure`.

This means reported `tps`/`verified_tps` is verification-success TPS for the formal workload phase.

Post-verify controls:

- `--verify-batch-size` (default `100`): request IDs per `BatchGetReceipts` query
- `--verify-rounds` (default `3`): max confirmation rounds
- `--verify-sleep-ms` (default `500`): sleep between rounds (milliseconds)

`parallelism` is the number of concurrent worker threads. `batch_size` is only the number of rows each worker executes sequentially in one client-side chunk; it is not an on-chain batch.

For normal generated workloads, `strict_valid_workload=true` together with the shadow-state generator logic is intended to avoid business-level failures such as insufficient funds in `TransactSavings` and `SendPayment`.

Default template has been tuned for stability:

- higher initial checking/savings ranges
- lower `max_debit_fraction`
- lower debit-heavy operation ratios
