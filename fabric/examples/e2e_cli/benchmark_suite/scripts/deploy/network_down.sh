#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

source "${BENCHMARK_ROOT}/scripts/common/lib.sh"

usage() {
  cat <<'EOF'
Usage: network_down.sh [--remove-volumes]

Stops the e2e_cli network used by benchmark_suite scripts.

Options:
  --remove-volumes   Also remove compose-managed Docker volumes.
EOF
}

REMOVE_VOLUMES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --remove-volumes)
      REMOVE_VOLUMES=1
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

require_commands docker
compose_cmd >/dev/null

print_section "Stopping e2e_cli network"
stop_network "${REMOVE_VOLUMES}"

print_section "e2e_cli network stopped"
if [ "${REMOVE_VOLUMES}" -eq 1 ]; then
  echo "Compose-managed Docker volumes were removed."
fi
