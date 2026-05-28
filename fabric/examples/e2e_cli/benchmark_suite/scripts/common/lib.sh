#!/bin/bash

set -euo pipefail

COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_ROOT="$(cd "${COMMON_DIR}/../.." && pwd)"
E2E_CLI_ROOT="$(cd "${BENCHMARK_ROOT}/.." && pwd)"
REPO_ROOT="$(cd "${E2E_CLI_ROOT}/../.." && pwd)"

source "${COMMON_DIR}/peer_env.sh"

require_commands() {
  local missing=0
  local cmd
  for cmd in "$@"; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      echo "Missing required command: ${cmd}" >&2
      missing=1
    fi
  done

  if [ "$missing" -ne 0 ]; then
    return 1
  fi
}

load_env_file() {
  local env_file="$1"
  if [ ! -f "$env_file" ]; then
    echo "Config file not found: ${env_file}" >&2
    return 1
  fi

  set -a
  # shellcheck source=/dev/null
  source "$env_file"
  set +a
}

print_section() {
  echo
  echo "==== $* ===="
}

python_cmd() {
  if command -v python3 >/dev/null 2>&1; then
    printf '%s\n' "python3"
    return 0
  fi

  echo "Missing required command: python3" >&2
  return 1
}

verify_benchmark_python_scripts() {
  local python_bin="$1"
  shift
  local script
  for script in "$@"; do
    "${python_bin}" -m py_compile "${script}"
  done
}

timestamp_ms() {
  "$(python_cmd)" - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

compose_cmd() {
  if command -v docker-compose >/dev/null 2>&1; then
    printf '%s\n' "docker-compose"
    return 0
  fi
  if docker compose version >/dev/null 2>&1; then
    printf '%s\n' "docker compose"
    return 0
  fi

  echo "Missing required command: docker-compose or docker compose" >&2
  return 1
}

compose_http_timeout_value() {
  local default_timeout="${1:-300}"
  printf '%s\n' "${COMPOSE_HTTP_TIMEOUT:-${default_timeout}}"
}

docker_client_timeout_value() {
  local compose_timeout
  compose_timeout="$(compose_http_timeout_value)"
  printf '%s\n' "${DOCKER_CLIENT_TIMEOUT:-${compose_timeout}}"
}

docker_cli_bash() {
  local command_text="$1"
  docker exec cli bash -c "$command_text"
}

docker_container_running() {
  local container_name="$1"
  docker ps --format '{{.Names}}' | grep -Fx "$container_name" >/dev/null 2>&1
}

wait_for_cli_ready() {
  local timeout_seconds="${1:-180}"
  local start_ts
  start_ts="$(date +%s)"

  while true; do
    if docker logs cli 2>&1 | grep -q "End-2-End execution completed"; then
      return 0
    fi

    if [ $(( $(date +%s) - start_ts )) -ge "$timeout_seconds" ]; then
      echo "Timed out waiting for cli bootstrap logs" >&2
      return 1
    fi

    sleep 3
  done
}

ensure_network_started() {
  local channel_name="$1"
  local cli_timeout="${2:-10000}"
  local compose_command
  local compose_http_timeout
  local docker_client_timeout

  if docker_container_running "cli"; then
    return 0
  fi

  compose_command="$(compose_cmd)"
  compose_http_timeout="$(compose_http_timeout_value)"
  docker_client_timeout="$(docker_client_timeout_value)"

  (
    cd "${E2E_CLI_ROOT}"

    if [ ! -d "./crypto-config" ]; then
      bash ./generateArtifacts.sh "${channel_name}"
    fi

    CHANNEL_NAME="${channel_name}" \
    TIMEOUT="${cli_timeout}" \
    COMPOSE_HTTP_TIMEOUT="${compose_http_timeout}" \
    DOCKER_CLIENT_TIMEOUT="${docker_client_timeout}" \
    ${compose_command} -f docker-compose-cli.yaml up -d
  )

  wait_for_cli_ready
}

stop_network() {
  local remove_volumes="${1:-0}"
  local compose_command
  local compose_http_timeout
  local docker_client_timeout

  compose_command="$(compose_cmd)"
  compose_http_timeout="$(compose_http_timeout_value)"
  docker_client_timeout="$(docker_client_timeout_value)"

  (
    cd "${E2E_CLI_ROOT}"

    if [ "${remove_volumes}" -eq 1 ]; then
      COMPOSE_HTTP_TIMEOUT="${compose_http_timeout}" \
      DOCKER_CLIENT_TIMEOUT="${docker_client_timeout}" \
      ${compose_command} -f docker-compose-cli.yaml down --remove-orphans -v
    else
      COMPOSE_HTTP_TIMEOUT="${compose_http_timeout}" \
      DOCKER_CLIENT_TIMEOUT="${docker_client_timeout}" \
      ${compose_command} -f docker-compose-cli.yaml down --remove-orphans
    fi
  )
}

run_peer_command() {
  local peer_index="$1"
  shift
  local command_text="$*"
  local exports_text

  exports_text="$(peer_env_exports "$peer_index")"
  docker_cli_bash "${exports_text}; ${command_text}"
}

peer_chaincode_install() {
  local peer_index="$1"
  local chaincode_name="$2"
  local chaincode_version="$3"
  local chaincode_path="$4"

  run_peer_command "$peer_index" \
    "peer chaincode install -n ${chaincode_name} -v ${chaincode_version} -p ${chaincode_path}"
}

peer_chaincode_is_installed() {
  local peer_index="$1"
  local chaincode_name="$2"
  local chaincode_version="$3"
  local output

  output="$(run_peer_command "$peer_index" "peer chaincode list --installed" 2>&1)" || return 2
  printf '%s\n' "${output}" | grep -Fq "Name: ${chaincode_name}, Version: ${chaincode_version}"
}

peer_chaincode_instantiate() {
  local peer_index="$1"
  local channel_name="$2"
  local chaincode_name="$3"
  local chaincode_version="$4"
  local ctor_json="$5"
  local endorsement_policy="${6:-OR ('Org1MSP.member','Org2MSP.member')}"

  run_peer_command "$peer_index" \
    "peer chaincode instantiate -o orderer.example.com:7050 --tls \${CORE_PEER_TLS_ENABLED} --cafile \${ORDERER_CA} -C ${channel_name} -n ${chaincode_name} -v ${chaincode_version} -c '${ctor_json}' -P \"${endorsement_policy}\""
}

peer_chaincode_is_instantiated() {
  local peer_index="$1"
  local channel_name="$2"
  local chaincode_name="$3"
  local chaincode_version="$4"
  local output

  output="$(run_peer_command "$peer_index" "peer chaincode list --instantiated -C ${channel_name}" 2>&1)" || return 2
  printf '%s\n' "${output}" | grep -Fq "Name: ${chaincode_name}, Version: ${chaincode_version}"
}

run_peer_query() {
  local peer_index="$1"
  local channel_name="$2"
  local chaincode_name="$3"
  local ctor_json="$4"

  run_peer_command "$peer_index" \
    "peer chaincode query -C ${channel_name} -n ${chaincode_name} -c '${ctor_json}'"
}

run_peer_query_with_retry() {
  local peer_index="$1"
  local channel_name="$2"
  local chaincode_name="$3"
  local ctor_json="$4"
  local expected_pattern="${5:-}"
  local timeout_seconds="${6:-120}"
  local sleep_seconds="${7:-3}"
  local start_ts
  local elapsed
  local output

  start_ts="$(date +%s)"

  while true; do
    if output="$(run_peer_query "$peer_index" "$channel_name" "$chaincode_name" "$ctor_json" 2>&1)"; then
      if [ -z "${expected_pattern}" ] || printf '%s\n' "${output}" | grep -Eq "${expected_pattern}"; then
        printf '%s\n' "${output}"
        return 0
      fi
    fi

    elapsed=$(( $(date +%s) - start_ts ))
    if [ "${elapsed}" -ge "${timeout_seconds}" ]; then
      printf '%s\n' "${output}" >&2
      echo "Timed out waiting for successful chaincode query on ${chaincode_name}" >&2
      return 1
    fi

    echo "Query not ready yet for ${chaincode_name}; retrying in ${sleep_seconds}s (${elapsed}s elapsed)" >&2
    sleep "${sleep_seconds}"
  done
}

run_peer_invoke() {
  local peer_index="$1"
  local channel_name="$2"
  local chaincode_name="$3"
  local ctor_json="$4"

  run_peer_command "$peer_index" \
    "peer chaincode invoke -o orderer.example.com:7050 --tls \${CORE_PEER_TLS_ENABLED} --cafile \${ORDERER_CA} -C ${channel_name} -n ${chaincode_name} -c '${ctor_json}'"
}

ensure_result_dir() {
  local benchmark_name="$1"
  local run_id="$2"
  local result_dir="${BENCHMARK_ROOT}/results/${benchmark_name}/${run_id}"

  mkdir -p "${result_dir}"
  printf '%s\n' "${result_dir}"
}

append_execution_row() {
  local csv_path="$1"
  shift

  printf '%s\n' "$*" >> "${csv_path}"
}

write_run_manifest() {
  local manifest_path="$1"
  local benchmark_name="$2"
  local dataset_path="$3"
  local channel_name="$4"
  local chaincode_name="$5"
  local run_id="$6"
  local parallelism="$7"
  local batch_size="$8"
  local load_elapsed_ms="$9"

  "$(python_cmd)" - "$manifest_path" "$benchmark_name" "$dataset_path" "$channel_name" "$chaincode_name" "$run_id" "$parallelism" "$batch_size" "$load_elapsed_ms" <<'PY'
import json
import sys
from datetime import datetime

manifest_path, benchmark_name, dataset_path, channel_name, chaincode_name, run_id, parallelism, batch_size, load_elapsed_ms = sys.argv[1:]

payload = {
    "benchmark": benchmark_name,
    "dataset_path": dataset_path,
    "channel_name": channel_name,
    "chaincode_name": chaincode_name,
    "run_id": run_id,
    "parallelism": int(parallelism),
    "batch_size": int(batch_size),
    "load_elapsed_ms": int(load_elapsed_ms),
    "created_at": datetime.utcnow().isoformat() + "Z",
}

with open(manifest_path, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, indent=2, sort_keys=True)
PY
}
