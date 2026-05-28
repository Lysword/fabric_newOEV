#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

source "${BENCHMARK_ROOT}/scripts/common/lib.sh"

usage() {
  cat <<'EOF'
Usage: smallbank_up_and_deploy.sh [--channel-name NAME] [--chaincode-name NAME] [--chaincode-version VERSION]

Starts the e2e_cli network if needed and deploys the Smallbank benchmark chaincode.
EOF
}

CHANNEL_NAME_OVERRIDE=""
CHAINCODE_NAME_OVERRIDE=""
CHAINCODE_VERSION_OVERRIDE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --channel-name)
      CHANNEL_NAME_OVERRIDE="$2"
      shift 2
      ;;
    --chaincode-name)
      CHAINCODE_NAME_OVERRIDE="$2"
      shift 2
      ;;
    --chaincode-version)
      CHAINCODE_VERSION_OVERRIDE="$2"
      shift 2
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

load_env_file "${BENCHMARK_ROOT}/configs/smallbank/default.env"

CHANNEL_NAME="${CHANNEL_NAME_OVERRIDE:-${CHANNEL_NAME}}"
CHAINCODE_NAME="${CHAINCODE_NAME_OVERRIDE:-${CHAINCODE_NAME}}"
CHAINCODE_VERSION="${CHAINCODE_VERSION_OVERRIDE:-${CHAINCODE_VERSION}}"

require_commands docker
compose_cmd >/dev/null

print_section "Ensuring e2e_cli network is up"
ensure_network_started "${CHANNEL_NAME}"

CHAINCODE_PATH="github.com/hyperledger/fabric/examples/e2e_cli/benchmark_suite/chaincode/smallbank"

print_section "Installing Smallbank chaincode on Org1 peer0"
if peer_chaincode_is_installed 0 "${CHAINCODE_NAME}" "${CHAINCODE_VERSION}"; then
  echo "Smallbank chaincode already installed on Org1 peer0"
else
  peer_chaincode_install 0 "${CHAINCODE_NAME}" "${CHAINCODE_VERSION}" "${CHAINCODE_PATH}"
fi

print_section "Installing Smallbank chaincode on Org2 peer0"
if peer_chaincode_is_installed 2 "${CHAINCODE_NAME}" "${CHAINCODE_VERSION}"; then
  echo "Smallbank chaincode already installed on Org2 peer0"
else
  peer_chaincode_install 2 "${CHAINCODE_NAME}" "${CHAINCODE_VERSION}" "${CHAINCODE_PATH}"
fi

print_section "Instantiating Smallbank chaincode"
if peer_chaincode_is_instantiated 2 "${CHANNEL_NAME}" "${CHAINCODE_NAME}" "${CHAINCODE_VERSION}"; then
  echo "Smallbank chaincode already instantiated on channel ${CHANNEL_NAME}"
else
  peer_chaincode_instantiate 2 "${CHANNEL_NAME}" "${CHAINCODE_NAME}" "${CHAINCODE_VERSION}" '{"Args":[]}'
fi

print_section "Running Smallbank health check"
run_peer_query_with_retry 2 "${CHANNEL_NAME}" "${CHAINCODE_NAME}" '{"Args":["Ping"]}' '"benchmark":"smallbank".*"status":"ok"|\"status\":\"ok\".*\"benchmark\":\"smallbank\"' 120 3
run_peer_query_with_retry 0 "${CHANNEL_NAME}" "${CHAINCODE_NAME}" '{"Args":["Ping"]}' '"benchmark":"smallbank".*"status":"ok"|\"status\":\"ok\".*\"benchmark\":\"smallbank\"' 120 3

print_section "Smallbank deployment complete"
