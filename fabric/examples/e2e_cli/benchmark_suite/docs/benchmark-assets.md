# Benchmark Assets

This document lists the persistent benchmark assets that need to be preserved locally.

## Required Modified File Outside Benchmark Suite

- `examples/e2e_cli/docker-compose-cli.yaml`: adds the `benchmark_suite` volume mount into the `cli` container

## Benchmark Suite Entry

- `examples/e2e_cli/benchmark_suite/README.md`: benchmark suite entry point

## Documentation

- `examples/e2e_cli/benchmark_suite/docs/benchmark-assets.md`: persistent asset inventory
- `examples/e2e_cli/benchmark_suite/docs/architecture-overview-zh.md`: Chinese overview of the benchmark architecture
- `examples/e2e_cli/benchmark_suite/docs/workflow-details-zh.md`: Chinese step-by-step workflow explanation
- `examples/e2e_cli/benchmark_suite/docs/experiment-record-template-zh.md`: Chinese experiment record template
- `examples/e2e_cli/benchmark_suite/docs/Linux系统运行与测试说明.md`: Chinese Linux run and test guide
- `examples/e2e_cli/benchmark_suite/docs/linux-run-checklist.md`: Linux copy and execution checklist
- `examples/e2e_cli/benchmark_suite/docs/smallbank-usage.md`: Smallbank usage guide
- `examples/e2e_cli/benchmark_suite/docs/ycsb-usage.md`: YCSB usage guide
- `examples/e2e_cli/benchmark_suite/docs/operation-manual-verified-tps-zh.md`: full operation manual for verified TPS workflow

## Chaincode

- `examples/e2e_cli/benchmark_suite/chaincode/smallbank/smallbank.go`: Smallbank benchmark chaincode
- `examples/e2e_cli/benchmark_suite/chaincode/smallbank/smallbank_test.go`: Smallbank unit tests
- `examples/e2e_cli/benchmark_suite/chaincode/ycsb/ycsb.go`: YCSB benchmark chaincode
- `examples/e2e_cli/benchmark_suite/chaincode/ycsb/ycsb_test.go`: YCSB unit tests

## Scripts

- `examples/e2e_cli/benchmark_suite/scripts/common/peer_env.sh`: peer and TLS environment helpers
- `examples/e2e_cli/benchmark_suite/scripts/common/lib.sh`: Docker, deployment, and result helpers
- `examples/e2e_cli/benchmark_suite/scripts/common/metrics.sh`: benchmark result summarizer with verified TPS and read/write split metrics
- `examples/e2e_cli/benchmark_suite/scripts/common/run_benchmark.py`: concurrent workload runner with submit-stage timing and batched post-verify receipt confirmation
- `examples/e2e_cli/benchmark_suite/scripts/deploy/network_down.sh`: network shutdown helper
- `examples/e2e_cli/benchmark_suite/scripts/deploy/smallbank_up_and_deploy.sh`: network startup and Smallbank chaincode deployment
- `examples/e2e_cli/benchmark_suite/scripts/deploy/ycsb_up_and_deploy.sh`: network startup and YCSB chaincode deployment
- `examples/e2e_cli/benchmark_suite/scripts/run/smallbank_load_and_run.sh`: Smallbank dataset generation, import, and workload execution
- `examples/e2e_cli/benchmark_suite/scripts/run/ycsb_load_and_run.sh`: YCSB dataset generation, import, and workload execution

## Dataset Templates

- `examples/e2e_cli/benchmark_suite/datasets/templates/smallbank.default.json`: default Smallbank dataset parameters
- `examples/e2e_cli/benchmark_suite/datasets/templates/ycsb.default.json`: default YCSB dataset parameters

## Dataset Generators

- `examples/e2e_cli/benchmark_suite/datasets/generators/generate_smallbank_dataset.py`: Smallbank dataset generator
- `examples/e2e_cli/benchmark_suite/datasets/generators/generate_ycsb_dataset.py`: YCSB dataset generator

## Runtime Configuration

- `examples/e2e_cli/benchmark_suite/configs/smallbank/default.env`: Smallbank deployment and run defaults
- `examples/e2e_cli/benchmark_suite/configs/ycsb/default.env`: YCSB deployment and run defaults

## Generated Dataset Layout

Generated datasets are written under:

- `examples/e2e_cli/benchmark_suite/datasets/generated/smallbank/<dataset_name>/`
- `examples/e2e_cli/benchmark_suite/datasets/generated/ycsb/<dataset_name>/`

Smallbank generated dataset files:

- `manifest.json`
- `init_accounts.jsonl`
- `workload.jsonl`

YCSB generated dataset files:

- `manifest.json`
- `init_records.jsonl`
- `workload.jsonl`

## Result Layout

Benchmark results are written under:

- `examples/e2e_cli/benchmark_suite/results/smallbank/<run_id>/`
- `examples/e2e_cli/benchmark_suite/results/ycsb/<run_id>/`

Each run directory is expected to contain:

- `run_manifest.json`
- `execution.csv`
- `summary.json`
- `summary.md`
- `raw.log`
- `load_metrics.json`

## Structure Notes (2026-05-28)

This section records the current code-structure understanding for newly integrated `smallbank` and `ycsb` benchmarks.

### Top-Level Benchmark Flow

- Deploy stage:
  - `scripts/deploy/smallbank_up_and_deploy.sh`
  - `scripts/deploy/ycsb_up_and_deploy.sh`
  - Shared teardown: `scripts/deploy/network_down.sh`
- Run stage:
  - `scripts/run/smallbank_load_and_run.sh`
  - `scripts/run/ycsb_load_and_run.sh`
  - Shared runner: `scripts/common/run_benchmark.py`
- Data stage:
  - Templates: `datasets/templates/*.default.json`
  - Generators: `datasets/generators/generate_smallbank_dataset.py`, `datasets/generators/generate_ycsb_dataset.py`
  - Generated outputs: `datasets/generated/<benchmark>/<dataset_name>/`

### Smallbank Structure

- Chaincode entry: `chaincode/smallbank/smallbank.go`
- Business ops:
  - `CreateAccount`, `Balance`, `DepositChecking`, `TransactSavings`
  - `Amalgamate`, `WriteCheck`, `SendPayment`
- Verification/health ops:
  - `Ping`, `GetReceipt`, `BatchGetReceipts`
- Ledger key model:
  - Account key: `account:<account_id>`
  - Receipt key: `receipt:<request_id>`
- Run script contract:
  - `scripts/run/smallbank_load_and_run.sh` loads `configs/smallbank/default.env`
  - Dataset files required:
    - `init_accounts.jsonl`
    - `workload.jsonl`
  - Result output:
    - `results/smallbank/<run_id>/`
    - includes summary, execution CSV, load metrics, run manifest

### YCSB Structure

- Chaincode entry: `chaincode/ycsb/ycsb.go`
- Business ops:
  - `Insert`, `Read`, `Update`, `Scan`
- Verification/health ops:
  - `Ping`, `GetReceipt`, `BatchGetReceipts`
- Ledger model:
  - Business records stored by direct key
  - Receipt key prefix: `receipt:<request_id>`
  - `Scan` skips receipt keys to avoid control-plane pollution
- Payload conventions:
  - Insert/Update accepts JSON payload with `{"value":"..."}`
  - Record payload stored as `{"key":"...","value":"..."}`
- Run script contract:
  - `scripts/run/ycsb_load_and_run.sh` loads `configs/ycsb/default.env`
  - Dataset files required:
    - `init_records.jsonl`
    - `workload.jsonl`
  - Result output:
    - `results/ycsb/<run_id>/`
    - includes summary, execution CSV, load metrics, run manifest

### Shared Runtime Semantics

- Both run scripts optionally generate datasets (`--generate`) before execution.
- Both call shared `run_benchmark.py` with parallelism/batch/verify parameters.
- Both use post-submit receipt verification (`BatchGetReceipts`) and write persistent result artifacts under `results/`.
