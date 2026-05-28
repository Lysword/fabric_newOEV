#!/bin/bash

set -euo pipefail

summarize_execution_csv() {
  local csv_path="$1"
  local summary_json="$2"
  local summary_md="$3"
  local load_elapsed_ms="${4:-0}"
  local load_ops="${5:-0}"
  local python_bin

  if command -v python3 >/dev/null 2>&1; then
    python_bin="python3"
  else
    echo "Missing required command: python3" >&2
    return 1
  fi

  "${python_bin}" - "$csv_path" "$summary_json" "$summary_md" "$load_elapsed_ms" "$load_ops" <<'PY'
import csv
import json
import math
import statistics
import sys

csv_path, summary_json, summary_md, load_elapsed_ms, load_ops = sys.argv[1:]
load_elapsed_ms = int(load_elapsed_ms)
load_ops = int(load_ops)

rows = []
with open(csv_path, "r", encoding="utf-8", newline="") as handle:
    reader = csv.DictReader(handle)
    for row in reader:
        rows.append(row)

latencies = []
start_values = []
end_values = []
successful_ops = 0
business_failures = 0
system_failures = 0
pending_submitted_ops = 0
read_success_ops = 0
attempted_tx_ops = 0
validated_success_tx_ops = 0
overall_success_ops = 0
write_submitted_ops = 0
write_verified_success_ops = 0

for row in rows:
    status = row["status"]
    latency = float(row["latency_ms"])
    latencies.append(latency)
    start_values.append(int(row["start_ts"]))
    end_values.append(int(row["end_ts"]))

    op_type = row["op_type"]
    is_read_op = op_type in ("Balance", "Read", "Scan")
    is_write_op = not is_read_op
    request_id = str(row.get("request_id", "")).strip()

    if status == "success":
        successful_ops += 1
        overall_success_ops += 1
        if is_read_op:
            read_success_ops += 1
        else:
            validated_success_tx_ops += 1
            write_verified_success_ops += 1
    elif status == "submitted":
        pending_submitted_ops += 1
    elif status == "business_failure":
        business_failures += 1
    else:
        system_failures += 1

    if is_write_op and request_id:
        write_submitted_ops += 1
    if is_write_op:
        attempted_tx_ops += 1

attempted_ops = len(rows)
failed_ops = attempted_ops - successful_ops
elapsed_seconds = 0.0
if start_values and end_values:
    elapsed_seconds = max(0.0, (max(end_values) - min(start_values)) / 1000.0)

def percentile(values, pct):
    if not values:
        return 0.0
    ordered = sorted(values)
    if len(ordered) == 1:
        return float(ordered[0])
    rank = (len(ordered) - 1) * pct
    lower = math.floor(rank)
    upper = math.ceil(rank)
    if lower == upper:
        return float(ordered[int(rank)])
    lower_value = ordered[lower]
    upper_value = ordered[upper]
    return float(lower_value + (upper_value - lower_value) * (rank - lower))

# Mixed read/write throughput: successful reads + validated-success writes over workload elapsed time.
overall_tps = 0.0 if elapsed_seconds <= 0 else overall_success_ops / elapsed_seconds
load_elapsed_seconds = load_elapsed_ms / 1000.0 if load_elapsed_ms > 0 else 0.0
load_tps = 0.0 if load_elapsed_seconds <= 0 else load_ops / load_elapsed_seconds

summary = {
    "attempted_ops": attempted_ops,
    "attempted_tx_ops": attempted_tx_ops,
    "successful_ops": successful_ops,
    "overall_success_ops": overall_success_ops,
    "validated_success_tx_ops": validated_success_tx_ops,
    "failed_ops": failed_ops,
    "business_failures": business_failures,
    "system_failures": system_failures,
    "pending_submitted_ops": pending_submitted_ops,
    "success_rate": 0.0 if attempted_ops == 0 else overall_success_ops / attempted_ops,
    "elapsed_seconds": elapsed_seconds,
    "tps": overall_tps,
    "verified_tps": overall_tps,
    "read_success_ops": read_success_ops,
    "write_submitted_ops": write_submitted_ops,
    "write_verified_success_ops": write_verified_success_ops,
    "confirm_rate": 0.0 if write_submitted_ops == 0 else write_verified_success_ops / write_submitted_ops,
    "avg_latency_ms": 0.0 if not latencies else statistics.mean(latencies),
    "p50_latency_ms": percentile(latencies, 0.50),
    "p95_latency_ms": percentile(latencies, 0.95),
    "load_elapsed_seconds": load_elapsed_seconds,
    "load_tps": load_tps,
}

with open(summary_json, "w", encoding="utf-8") as handle:
    json.dump(summary, handle, indent=2, sort_keys=True)

with open(summary_md, "w", encoding="utf-8") as handle:
    handle.write("# Benchmark Summary\n\n")
    for key, value in summary.items():
      handle.write("- `{}`: {}\n".format(key, value))
PY
}
