#!/usr/bin/env python3

import argparse
import csv
import json
import math
import re
import shlex
import statistics
import subprocess
import time
from pathlib import Path


QUERY_RESULT_RE = re.compile(r"Query Result(?: \(Raw\))?:\s*(.*)")

PEER_ENVS = {
    0: {
        "CORE_PEER_LOCALMSPID": "Org1MSP",
        "CORE_PEER_TLS_ROOTCERT_FILE": "/opt/gopath/src/github.com/hyperledger/fabric/peer/crypto/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt",
        "CORE_PEER_MSPCONFIGPATH": "/opt/gopath/src/github.com/hyperledger/fabric/peer/crypto/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp",
        "CORE_PEER_ADDRESS": "peer0.org1.example.com:7051",
    },
    2: {
        "CORE_PEER_LOCALMSPID": "Org2MSP",
        "CORE_PEER_TLS_ROOTCERT_FILE": "/opt/gopath/src/github.com/hyperledger/fabric/peer/crypto/peerOrganizations/org2.example.com/peers/peer0.org2.example.com/tls/ca.crt",
        "CORE_PEER_MSPCONFIGPATH": "/opt/gopath/src/github.com/hyperledger/fabric/peer/crypto/peerOrganizations/org2.example.com/users/Admin@org2.example.com/msp",
        "CORE_PEER_ADDRESS": "peer0.org2.example.com:7051",
    },
}

COMMON_EXPORTS = {
    "CORE_PEER_TLS_ENABLED": "true",
    "ORDERER_CA": "/opt/gopath/src/github.com/hyperledger/fabric/peer/crypto/ordererOrganizations/example.com/orderers/orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem",
}


def parse_query_payload(output):
    matches = QUERY_RESULT_RE.findall(output)
    if matches:
        return matches[-1].strip()
    for line in reversed(output.splitlines()):
        candidate = line.strip()
        if (candidate.startswith("{") and candidate.endswith("}")) or (candidate.startswith("[") and candidate.endswith("]")):
            return candidate
    return None


def sanitize_message(message):
    return str(message).replace(",", ";").replace("\n", " ").strip()[:400]


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


class PeerClient(object):
    def __init__(self, channel_name, chaincode_name, peer_indices):
        self.channel_name = channel_name
        self.chaincode_name = chaincode_name
        self.peer_indices = peer_indices

    def _exports(self, peer_index):
        values = {}
        values.update(PEER_ENVS[peer_index])
        values.update(COMMON_EXPORTS)
        parts = []
        for key, value in values.items():
            parts.append("export {key}={value}".format(key=key, value=shlex.quote(value)))
        return "; ".join(parts)

    def _run(self, peer_index, command_text):
        full_command = "{exports}; {command}".format(
            exports=self._exports(peer_index),
            command=command_text,
        )
        proc = subprocess.run(
            ["docker", "exec", "cli", "bash", "-c", full_command],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            universal_newlines=True,
        )
        return proc.returncode, proc.stdout

    def query(self, peer_index, args):
        ctor_json = json.dumps({"Args": args}, separators=(",", ":"))
        command = "peer chaincode query -C {channel} -n {cc} -c {ctor}".format(
            channel=shlex.quote(self.channel_name),
            cc=shlex.quote(self.chaincode_name),
            ctor=shlex.quote(ctor_json),
        )
        return self._run(peer_index, command)

    def verify_receipts_batch(self, request_ids):
        aggregated = {}
        for request_id in request_ids:
            aggregated[request_id] = False

        saw_ok = False
        for peer_index in self.peer_indices:
            rc, output = self.query(peer_index, ["BatchGetReceipts", json.dumps(request_ids, separators=(",", ":"))])
            if rc != 0:
                continue
            payload = parse_query_payload(output)
            if not payload:
                continue
            try:
                data = json.loads(payload)
            except ValueError:
                continue
            if not isinstance(data, dict):
                continue
            saw_ok = True
            for request_id in request_ids:
                value = data.get(request_id, False)
                if isinstance(value, bool):
                    found = value
                elif isinstance(value, str):
                    found = value.strip().lower() == "true"
                else:
                    found = bool(value)
                aggregated[request_id] = aggregated[request_id] or found

        return saw_ok, aggregated


def read_execution_csv(path):
    rows = []
    with path.open("r", encoding="utf-8", newline="") as handle:
        reader = csv.DictReader(handle)
        for row in reader:
            rows.append(row)
    return rows


def write_execution_csv(path, rows):
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(
            handle,
            fieldnames=["seq", "op_type", "target_key_or_account", "start_ts", "end_ts", "latency_ms", "cli_rc", "status", "message", "request_id"],
        )
        writer.writeheader()
        writer.writerows(rows)


def build_summary(rows, total_execution_elapsed_seconds):
    latencies = []
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
        latencies.append(float(row["latency_ms"]))

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
    overall_tps = 0.0 if total_execution_elapsed_seconds <= 0 else overall_success_ops / total_execution_elapsed_seconds

    return {
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
        "total_execution_elapsed_seconds": total_execution_elapsed_seconds,
        "tps": overall_tps,
        "confirmed_tps": overall_tps,
        "read_success_ops": read_success_ops,
        "write_submitted_ops": write_submitted_ops,
        "write_verified_success_ops": write_verified_success_ops,
        "confirm_rate": 0.0 if write_submitted_ops == 0 else write_verified_success_ops / write_submitted_ops,
        "avg_latency_ms": 0.0 if not latencies else statistics.mean(latencies),
        "p50_latency_ms": percentile(latencies, 0.50),
        "p95_latency_ms": percentile(latencies, 0.95),
    }


def parse_peer_indices(text):
    values = []
    for part in text.split(","):
        value = part.strip()
        if not value:
            continue
        idx = int(value)
        if idx not in PEER_ENVS:
            raise ValueError("unsupported peer index: {0}".format(idx))
        values.append(idx)
    if not values:
        raise ValueError("peer indices must not be empty")
    return values


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--result-dir", required=True)
    parser.add_argument("--channel-name", required=True)
    parser.add_argument("--chaincode-name", required=True)
    parser.add_argument("--peer-indices", default="0,2")
    parser.add_argument("--verify-batch-size", type=int, default=100)
    parser.add_argument("--timeout-seconds", type=int, default=300)
    parser.add_argument("--poll-interval-ms", type=int, default=1000)
    parser.add_argument("--update-execution", action="store_true")
    return parser.parse_args()


def main():
    args = parse_args()
    result_dir = Path(args.result_dir).resolve()
    execution_path = result_dir / "execution.csv"
    if not execution_path.exists():
        raise SystemExit("execution.csv not found: {0}".format(execution_path))

    peer_indices = parse_peer_indices(args.peer_indices)
    client = PeerClient(args.channel_name, args.chaincode_name, peer_indices)
    rows = read_execution_csv(execution_path)

    request_to_rows = {}
    for row in rows:
        if row.get("status") != "submitted":
            continue
        request_id = str(row.get("request_id", "")).strip()
        if not request_id:
            continue
        request_to_rows.setdefault(request_id, []).append(row)

    pending_ids = set(request_to_rows.keys())
    confirm_start = time.time()
    deadline = confirm_start + max(1, args.timeout_seconds)
    poll_interval = max(100, args.poll_interval_ms) / 1000.0

    while pending_ids and time.time() < deadline:
        current_ids = sorted(pending_ids)
        for i in range(0, len(current_ids), max(1, args.verify_batch_size)):
            chunk = current_ids[i : i + max(1, args.verify_batch_size)]
            saw_ok, found_map = client.verify_receipts_batch(chunk)
            if not saw_ok:
                continue
            for request_id in chunk:
                if found_map.get(request_id, False):
                    for row in request_to_rows.get(request_id, []):
                        row["status"] = "success"
                        row["message"] = sanitize_message("late receipt verified by recount script")
                    if request_id in pending_ids:
                        pending_ids.remove(request_id)

        if pending_ids:
            time.sleep(poll_interval)

    confirm_end = time.time()

    if args.update_execution:
        write_execution_csv(execution_path, rows)

    start_ts_values = []
    for row in rows:
        try:
            start_ts_values.append(int(row["start_ts"]))
        except Exception:
            pass

    end_ts_values = []
    for row in rows:
        try:
            end_ts_values.append(int(row["end_ts"]))
        except Exception:
            pass

    if start_ts_values and end_ts_values:
        # Only use benchmark execution window, excluding recount wait time.
        total_execution_elapsed_seconds = max(0.0, (max(end_ts_values) - min(start_ts_values)) / 1000.0)
    else:
        total_execution_elapsed_seconds = 0.0

    summary = build_summary(rows, total_execution_elapsed_seconds)
    summary["recount_timeout_seconds"] = max(1, args.timeout_seconds)
    summary["recount_poll_interval_ms"] = max(100, args.poll_interval_ms)
    summary["recount_pending_after_timeout"] = len(pending_ids)
    summary["recount_updated_execution"] = bool(args.update_execution)

    summary_json = result_dir / "recount_summary.json"
    with summary_json.open("w", encoding="utf-8") as handle:
        json.dump(summary, handle, indent=2, sort_keys=True)
        handle.write("\n")

    summary_md = result_dir / "recount_summary.md"
    with summary_md.open("w", encoding="utf-8") as handle:
        handle.write("# Recount Summary\n\n")
        for key, value in summary.items():
            handle.write("- `{0}`: {1}\n".format(key, value))

    print("Recount summary written to: {0}".format(summary_json))
    print("Pending after recount: {0}".format(len(pending_ids)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
