#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

source "${BENCHMARK_ROOT}/scripts/common/lib.sh"
source "${BENCHMARK_ROOT}/scripts/common/metrics.sh"

usage() {
  cat <<'EOF'
Usage: ycsb_load_and_run.sh [--generate] [--dataset-path PATH] [--dataset-name NAME]
                            [--template PATH] [--key-start N] [--record-count N] [--operation-count N]
                            [--distribution uniform|zipfian] [--zipf-theta FLOAT]
                            [--read-ratio FLOAT] [--update-ratio FLOAT]
                            [--insert-ratio FLOAT] [--scan-ratio FLOAT]
                            [--scan-length-min N] [--scan-length-max N] [--value-size N]
                            [--parallelism N] [--batch-size N]
                            [--verify-batch-size N] [--verify-rounds N] [--verify-sleep-ms N]
                            [--lightweight]
                            [--channel-name NAME] [--chaincode-name NAME]

Generates or loads a YCSB dataset, imports initial state, executes the workload,
and writes metrics under benchmark_suite/results/ycsb/<run_id>.
EOF
}

GENERATE_DATASET=0
DATASET_PATH_OVERRIDE=""
DATASET_NAME_OVERRIDE=""
TEMPLATE_OVERRIDE=""
KEY_START_OVERRIDE=""
RECORD_COUNT_OVERRIDE=""
OPERATION_COUNT_OVERRIDE=""
DISTRIBUTION_OVERRIDE=""
ZIPF_THETA_OVERRIDE=""
READ_RATIO_OVERRIDE=""
UPDATE_RATIO_OVERRIDE=""
INSERT_RATIO_OVERRIDE=""
SCAN_RATIO_OVERRIDE=""
SCAN_LENGTH_MIN_OVERRIDE=""
SCAN_LENGTH_MAX_OVERRIDE=""
VALUE_SIZE_OVERRIDE=""
PARALLELISM_OVERRIDE=""
BATCH_SIZE_OVERRIDE=""
VERIFY_BATCH_SIZE_OVERRIDE=""
VERIFY_ROUNDS_OVERRIDE=""
VERIFY_SLEEP_MS_OVERRIDE=""
CHANNEL_NAME_OVERRIDE=""
CHAINCODE_NAME_OVERRIDE=""
LIGHTWEIGHT=0

while [ $# -gt 0 ]; do
  case "$1" in
    --generate)
      GENERATE_DATASET=1
      shift
      ;;
    --dataset-path)
      DATASET_PATH_OVERRIDE="$2"
      shift 2
      ;;
    --dataset-name)
      DATASET_NAME_OVERRIDE="$2"
      shift 2
      ;;
    --template)
      TEMPLATE_OVERRIDE="$2"
      shift 2
      ;;
    --key-start)
      KEY_START_OVERRIDE="$2"
      shift 2
      ;;
    --record-count)
      RECORD_COUNT_OVERRIDE="$2"
      shift 2
      ;;
    --operation-count)
      OPERATION_COUNT_OVERRIDE="$2"
      shift 2
      ;;
    --distribution)
      DISTRIBUTION_OVERRIDE="$2"
      shift 2
      ;;
    --zipf-theta)
      ZIPF_THETA_OVERRIDE="$2"
      shift 2
      ;;
    --read-ratio)
      READ_RATIO_OVERRIDE="$2"
      shift 2
      ;;
    --update-ratio)
      UPDATE_RATIO_OVERRIDE="$2"
      shift 2
      ;;
    --insert-ratio)
      INSERT_RATIO_OVERRIDE="$2"
      shift 2
      ;;
    --scan-ratio)
      SCAN_RATIO_OVERRIDE="$2"
      shift 2
      ;;
    --scan-length-min)
      SCAN_LENGTH_MIN_OVERRIDE="$2"
      shift 2
      ;;
    --scan-length-max)
      SCAN_LENGTH_MAX_OVERRIDE="$2"
      shift 2
      ;;
    --value-size)
      VALUE_SIZE_OVERRIDE="$2"
      shift 2
      ;;
    --parallelism)
      PARALLELISM_OVERRIDE="$2"
      shift 2
      ;;
    --batch-size)
      BATCH_SIZE_OVERRIDE="$2"
      shift 2
      ;;
    --verify-batch-size)
      VERIFY_BATCH_SIZE_OVERRIDE="$2"
      shift 2
      ;;
    --verify-rounds)
      VERIFY_ROUNDS_OVERRIDE="$2"
      shift 2
      ;;
    --verify-sleep-ms)
      VERIFY_SLEEP_MS_OVERRIDE="$2"
      shift 2
      ;;
    --channel-name)
      CHANNEL_NAME_OVERRIDE="$2"
      shift 2
      ;;
    --chaincode-name)
      CHAINCODE_NAME_OVERRIDE="$2"
      shift 2
      ;;
    --lightweight)
      LIGHTWEIGHT=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

load_env_file "${BENCHMARK_ROOT}/configs/ycsb/default.env"

PYTHON_BIN="$(python_cmd)"
verify_benchmark_python_scripts "${PYTHON_BIN}" \
  "${BENCHMARK_ROOT}/datasets/generators/generate_smallbank_dataset.py" \
  "${BENCHMARK_ROOT}/datasets/generators/generate_ycsb_dataset.py" \
  "${BENCHMARK_ROOT}/scripts/common/run_benchmark.py"
DATASET_NAME="${DATASET_NAME_OVERRIDE:-${DEFAULT_DATASET_NAME}}"
CHANNEL_NAME="${CHANNEL_NAME_OVERRIDE:-${CHANNEL_NAME}}"
CHAINCODE_NAME="${CHAINCODE_NAME_OVERRIDE:-${CHAINCODE_NAME}}"
PARALLELISM="${PARALLELISM_OVERRIDE:-${PARALLELISM}}"
BATCH_SIZE="${BATCH_SIZE_OVERRIDE:-${BATCH_SIZE}}"
VERIFY_BATCH_SIZE="${VERIFY_BATCH_SIZE_OVERRIDE:-100}"
VERIFY_ROUNDS="${VERIFY_ROUNDS_OVERRIDE:-3}"
VERIFY_SLEEP_MS="${VERIFY_SLEEP_MS_OVERRIDE:-500}"

if [ "$GENERATE_DATASET" -eq 1 ]; then
  GENERATOR_ARGS=("${PYTHON_BIN}" "${BENCHMARK_ROOT}/datasets/generators/generate_ycsb_dataset.py" --output-name "${DATASET_NAME}")
  if [ -n "${TEMPLATE_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--template "${TEMPLATE_OVERRIDE}")
  fi
  if [ -n "${KEY_START_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--key-start "${KEY_START_OVERRIDE}")
  fi
  if [ -n "${RECORD_COUNT_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--record-count "${RECORD_COUNT_OVERRIDE}")
  fi
  if [ -n "${OPERATION_COUNT_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--operation-count "${OPERATION_COUNT_OVERRIDE}")
  fi
  if [ -n "${DISTRIBUTION_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--distribution "${DISTRIBUTION_OVERRIDE}")
  fi
  if [ -n "${ZIPF_THETA_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--zipf-theta "${ZIPF_THETA_OVERRIDE}")
  fi
  if [ -n "${READ_RATIO_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--read-ratio "${READ_RATIO_OVERRIDE}")
  fi
  if [ -n "${UPDATE_RATIO_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--update-ratio "${UPDATE_RATIO_OVERRIDE}")
  fi
  if [ -n "${INSERT_RATIO_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--insert-ratio "${INSERT_RATIO_OVERRIDE}")
  fi
  if [ -n "${SCAN_RATIO_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--scan-ratio "${SCAN_RATIO_OVERRIDE}")
  fi
  if [ -n "${SCAN_LENGTH_MIN_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--scan-length-min "${SCAN_LENGTH_MIN_OVERRIDE}")
  fi
  if [ -n "${SCAN_LENGTH_MAX_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--scan-length-max "${SCAN_LENGTH_MAX_OVERRIDE}")
  fi
  if [ -n "${VALUE_SIZE_OVERRIDE}" ]; then
    GENERATOR_ARGS+=(--value-size "${VALUE_SIZE_OVERRIDE}")
  fi
  "${GENERATOR_ARGS[@]}"
fi

DATASET_PATH="${DATASET_PATH_OVERRIDE:-${BENCHMARK_ROOT}/datasets/generated/ycsb/${DATASET_NAME}}"
INIT_PATH="${DATASET_PATH}/init_records.jsonl"
WORKLOAD_PATH="${DATASET_PATH}/workload.jsonl"

if [ ! -f "${INIT_PATH}" ] || [ ! -f "${WORKLOAD_PATH}" ]; then
  echo "Dataset files not found under ${DATASET_PATH}" >&2
  exit 1
fi

RUN_ID="ycsb-$("${PYTHON_BIN}" - <<'PY'
from datetime import datetime
print(datetime.utcnow().strftime('%Y%m%dT%H%M%SZ'))
PY
)"

RESULT_DIR="$(ensure_result_dir "ycsb" "${RUN_ID}")"
SUMMARY_JSON="${RESULT_DIR}/summary.json"
SUMMARY_MD="${RESULT_DIR}/summary.md"
RUN_MANIFEST="${RESULT_DIR}/run_manifest.json"
LOAD_METRICS_JSON="${RESULT_DIR}/load_metrics.json"

print_section "Running YCSB benchmark"
RUN_BENCHMARK_ARGS=()
if [ "${LIGHTWEIGHT}" -eq 1 ]; then
  RUN_BENCHMARK_ARGS+=(--lightweight)
fi
"${PYTHON_BIN}" "${BENCHMARK_ROOT}/scripts/common/run_benchmark.py" \
  --benchmark ycsb \
  --dataset-path "${DATASET_PATH}" \
  --result-dir "${RESULT_DIR}" \
  --channel-name "${CHANNEL_NAME}" \
  --chaincode-name "${CHAINCODE_NAME}" \
  --parallelism "${PARALLELISM}" \
  --batch-size "${BATCH_SIZE}" \
  --verify-batch-size "${VERIFY_BATCH_SIZE}" \
  --verify-rounds "${VERIFY_ROUNDS}" \
  --verify-sleep-ms "${VERIFY_SLEEP_MS}" \
  ${RUN_BENCHMARK_ARGS[@]+"${RUN_BENCHMARK_ARGS[@]}"}

LOAD_ELAPSED_MS="$("${PYTHON_BIN}" - "${LOAD_METRICS_JSON}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    payload = json.load(handle)
print(int(payload["load_elapsed_ms"]))
PY
)"

LOAD_OPS="$("${PYTHON_BIN}" - "${LOAD_METRICS_JSON}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    payload = json.load(handle)
print(int(payload["load_ops"]))
PY
)"

summarize_execution_csv "${RESULT_DIR}/execution.csv" "${SUMMARY_JSON}" "${SUMMARY_MD}" "${LOAD_ELAPSED_MS}" "${LOAD_OPS}"
write_run_manifest "${RUN_MANIFEST}" "ycsb" "${DATASET_PATH}" "${CHANNEL_NAME}" "${CHAINCODE_NAME}" "${RUN_ID}" "${PARALLELISM}" "${BATCH_SIZE}" "${LOAD_ELAPSED_MS}"

print_section "YCSB benchmark complete"
echo "Results: ${RESULT_DIR}"
