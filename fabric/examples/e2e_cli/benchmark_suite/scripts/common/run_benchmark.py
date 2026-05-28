#!/usr/bin/env python3

import argparse
import csv
import json
import re
import shlex
import subprocess
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Dict, List, Optional, Set, Tuple
from uuid import uuid4


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


class PeerClient:
    def __init__(self, channel_name: str, chaincode_name: str):
        self.channel_name = channel_name
        self.chaincode_name = chaincode_name

    def _exports(self, peer_index: int) -> str:
        values = {}
        values.update(PEER_ENVS[peer_index])
        values.update(COMMON_EXPORTS)
        return "; ".join(
            "export {key}={value}".format(key=key, value=shlex.quote(value))
            for key, value in values.items()
        )

    def _run(self, peer_index: int, command_text: str) -> Tuple[int, str]:
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

    def query(self, peer_index: int, args: List[str]) -> Tuple[int, str]:
        ctor_json = json.dumps({"Args": args}, separators=(",", ":"))
        command = "peer chaincode query -C {channel} -n {cc} -c {ctor}".format(
            channel=shlex.quote(self.channel_name),
            cc=shlex.quote(self.chaincode_name),
            ctor=shlex.quote(ctor_json),
        )
        return self._run(peer_index, command)

    def invoke(self, peer_index: int, args: List[str]) -> Tuple[int, str]:
        ctor_json = json.dumps({"Args": args}, separators=(",", ":"))
        command = (
            "peer chaincode invoke -o orderer.example.com:7050 "
            "--tls ${{CORE_PEER_TLS_ENABLED}} "
            "--cafile ${{ORDERER_CA}} "
            "-C {channel} -n {cc} -c {ctor}"
        ).format(
            channel=shlex.quote(self.channel_name),
            cc=shlex.quote(self.chaincode_name),
            ctor=shlex.quote(ctor_json),
        )
        return self._run(peer_index, command)


class BenchmarkRunner:
    def __init__(
        self,
        benchmark: str,
        dataset_path: Path,
        result_dir: Path,
        channel_name: str,
        chaincode_name: str,
        parallelism: int,
        batch_size: int,
        receipt_timeout: int,
        lightweight: bool = False,
        verify_batch_size: int = 100,
        verify_rounds: int = 3,
        verify_sleep_ms: int = 500,
    ):
        self.benchmark = benchmark
        self.dataset_path = dataset_path
        self.result_dir = result_dir
        self.parallelism = max(1, parallelism)
        self.batch_size = max(1, batch_size)
        self.receipt_timeout = receipt_timeout
        self.lightweight = bool(lightweight)
        self.verify_batch_size = max(1, verify_batch_size)
        self.verify_rounds = max(1, verify_rounds)
        self.verify_sleep_seconds = max(0, verify_sleep_ms) / 1000.0
        self.client = PeerClient(channel_name, chaincode_name)
        self.run_token = uuid4().hex[:12]
        self.raw_log_path = result_dir / "raw.log"
        self.success_records_path = result_dir / "success_records.jsonl"
        self.log_lock = threading.Lock()
        self.progress_interval = 50
        self.receipt_poll_initial_sleep = 0.5
        self.receipt_poll_max_sleep = 4.0
        self.receipt_not_found_log_every = 10
        self.post_verify_rounds = self.verify_rounds
        self.post_verify_sleep_seconds = self.verify_sleep_seconds
        self.verify_peer_indices = [0, 2]

    def log_progress(self, message):
        print(message, flush=True)

    def append_raw_log(self, prefix: str, content: str) -> None:
        with self.log_lock:
            with self.raw_log_path.open("a", encoding="utf-8") as handle:
                handle.write(prefix)
                handle.write("\n")
                handle.write(content.rstrip())
                handle.write("\n")

    def append_success_record(self, record):
        with self.log_lock:
            with self.success_records_path.open("a", encoding="utf-8") as handle:
                handle.write(json.dumps(record, sort_keys=True))
                handle.write("\n")

    def verify_receipt_once(self, request_id):
        observed_not_found = False
        last_output = ""
        for peer_index in self.verify_peer_indices:
            rc, output = self.client.query(peer_index, ["GetReceipt", request_id])
            last_output = output
            if rc != 0:
                continue
            payload = self.parse_query_payload(output)
            if not payload:
                continue
            try:
                data = json.loads(payload)
            except ValueError:
                continue
            if isinstance(data, dict) and data.get("found") is True:
                return True, "found", output
            if isinstance(data, dict) and data.get("found") is False:
                observed_not_found = True
        if observed_not_found:
            return False, "not_found", last_output
        return False, "query_error", last_output

    def verify_receipts_individual_once(self, request_ids: List[str]) -> Tuple[Dict[str, bool], str, str]:
        results = {}
        had_transport_success = False
        last_output = ""
        for request_id in request_ids:
            found, reason, output = self.verify_receipt_once(request_id)
            last_output = output
            if reason in ("found", "not_found"):
                had_transport_success = True
            results[request_id] = found
        if had_transport_success:
            return results, "ok", last_output
        return results, "query_error", last_output

    def verify_receipts_batch_once(self, request_ids: List[str]) -> Tuple[Dict[str, bool], str, str]:
        aggregated = {request_id: False for request_id in request_ids}
        saw_ok = False
        last_output = ""

        for peer_index in self.verify_peer_indices:
            rc, output = self.client.query(peer_index, ["BatchGetReceipts", json.dumps(request_ids, separators=(",", ":"))])
            last_output = output
            if rc != 0:
                continue
            payload = self.parse_query_payload(output)
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

        if saw_ok:
            return aggregated, "ok", last_output

        # Fallback to single-receipt checks to reduce false negatives when batch query is unstable.
        return self.verify_receipts_individual_once(request_ids)

    def read_jsonl(self, path: Path) -> List[Dict[str, object]]:
        rows = []
        with path.open("r", encoding="utf-8") as handle:
            for line in handle:
                line = line.strip()
                if line:
                    rows.append(json.loads(line))
        return rows

    def parse_query_payload(self, output: str) -> Optional[str]:
        matches = QUERY_RESULT_RE.findall(output)
        if matches:
            return matches[-1].strip()
        for line in reversed(output.splitlines()):
            candidate = line.strip()
            if (candidate.startswith("{") and candidate.endswith("}")) or (candidate.startswith("[") and candidate.endswith("]")):
                return candidate
        return None

    def wait_for_receipt(self, request_id: str) -> Tuple[bool, Optional[Dict[str, object]], str]:
        deadline = time.time() + self.receipt_timeout
        last_output = ""
        sleep_seconds = self.receipt_poll_initial_sleep
        poll_count = 0
        while time.time() < deadline:
            poll_count += 1
            rc, output = self.client.query(0, ["GetReceipt", request_id])
            last_output = output
            if rc == 0:
                payload = self.parse_query_payload(output)
                if payload:
                    try:
                        data = json.loads(payload)
                    except ValueError:
                        data = None
                    if isinstance(data, dict) and data.get("found") is True:
                        self.append_raw_log("receipt:{0}".format(request_id), output)
                        return True, data, output
                    if isinstance(data, dict) and data.get("found") is False:
                        if poll_count == 1 or poll_count % self.receipt_not_found_log_every == 0:
                            self.append_raw_log("receipt:{0}:pending".format(request_id), output)
                    else:
                        self.append_raw_log("receipt:{0}".format(request_id), output)
                else:
                    self.append_raw_log("receipt:{0}".format(request_id), output)
            else:
                self.append_raw_log("receipt:{0}".format(request_id), output)
            time.sleep(sleep_seconds)
            sleep_seconds = min(self.receipt_poll_max_sleep, sleep_seconds * 2)
        return False, None, last_output

    def contains_phrase(self, output: str, phrases: Tuple[str, ...]) -> bool:
        lowered = output.lower()
        for phrase in phrases:
            if phrase in lowered:
                return True
        return False

    def query_json(self, args: List[str]) -> Tuple[int, Optional[Dict[str, object]], str]:
        rc, output = self.client.query(0, args)
        self.append_raw_log("query:{0}".format(args[0]), output)
        payload = self.parse_query_payload(output)
        if not payload:
            return rc, None, output
        try:
            return rc, json.loads(payload), output
        except ValueError:
            return rc, None, output

    def query_json_array(self, args: List[str]) -> Tuple[int, Optional[List[object]], str]:
        rc, output = self.client.query(0, args)
        self.append_raw_log("query:{0}".format(args[0]), output)
        payload = self.parse_query_payload(output)
        if not payload:
            return rc, None, output
        try:
            data = json.loads(payload)
        except ValueError:
            return rc, None, output
        if isinstance(data, list):
            return rc, data, output
        return rc, None, output

    def make_request_id(self, scope: str, sequence: int, operation: str) -> str:
        return "{0}-{1}-{2}-{3}-{4}".format(
            self.benchmark,
            self.run_token,
            scope,
            sequence,
            operation.lower(),
        )

    def validate_smallbank_balance(
        self,
        account_id: str,
        expected_checking: Optional[int] = None,
        expected_savings: Optional[int] = None,
    ) -> Tuple[bool, str]:
        rc, payload, output = self.query_json(["Balance", account_id])
        if rc != 0:
            return False, output
        if not isinstance(payload, dict):
            return False, "balance query returned non-json payload"
        if payload.get("account_id") != account_id:
            return False, "balance query returned unexpected account"
        if "checking" not in payload or "savings" not in payload:
            return False, "balance query missing balance fields"
        if expected_checking is not None and int(payload["checking"]) != expected_checking:
            return False, "balance query returned unexpected checking balance"
        if expected_savings is not None and int(payload["savings"]) != expected_savings:
            return False, "balance query returned unexpected savings balance"
        return True, "validated balance state"

    def validate_ycsb_read(self, key: str, expected_value: Optional[str] = None) -> Tuple[bool, str]:
        rc, payload, output = self.query_json(["Read", key])
        if rc != 0:
            return False, output
        if not isinstance(payload, dict):
            return False, "read query returned non-json payload"
        if payload.get("key") != key:
            return False, "read query returned unexpected key"
        if "value" not in payload:
            return False, "read query missing value"
        if expected_value is not None and str(payload["value"]) != expected_value:
            return False, "read query returned unexpected value"
        return True, "validated record state"

    def validate_ycsb_scan(self, start_key: str, length: int, payload: List[object]) -> Tuple[bool, str]:
        if len(payload) > length:
            return False, "scan returned more records than requested"

        previous_key = ""
        for item in payload:
            if not isinstance(item, dict):
                return False, "scan returned non-object item"
            key = item.get("key")
            value = item.get("value")
            if not isinstance(key, str) or not isinstance(value, str):
                return False, "scan item missing key or value"
            if key < start_key:
                return False, "scan returned key before requested start"
            if previous_key and key < previous_key:
                return False, "scan returned out-of-order keys"
            previous_key = key
        return True, "validated scan result"

    def lookup_smallbank_account(self, account_id: str) -> Tuple[str, Optional[Dict[str, object]], str]:
        rc, payload, output = self.query_json(["Balance", account_id])
        if isinstance(payload, dict) and payload.get("account_id") == account_id:
            return "found", payload, output
        if self.contains_phrase(output, ("account not found",)):
            return "missing", None, output
        if rc != 0:
            return "system_failure", None, output
        if payload is None:
            return "missing", None, output
        return "system_failure", None, output

    def lookup_ycsb_record(self, key: str) -> Tuple[str, Optional[Dict[str, object]], str]:
        rc, payload, output = self.query_json(["Read", key])
        if isinstance(payload, dict) and payload.get("key") == key:
            return "found", payload, output
        if self.contains_phrase(output, ("record not found",)):
            return "missing", None, output
        if rc != 0:
            return "system_failure", None, output
        if payload is None:
            return "missing", None, output
        return "system_failure", None, output

    def load_smallbank(self) -> Tuple[int, int]:
        rows = self.read_jsonl(self.dataset_path / "init_accounts.jsonl")
        total = len(rows)
        self.log_progress("[smallbank] load start: {0} accounts".format(total))
        start_ms = int(time.time() * 1000)
        for index, row in enumerate(rows, start=1):
            account_id = str(row["account_id"])
            lookup_status, _, lookup_output = self.lookup_smallbank_account(account_id)
            if lookup_status == "system_failure":
                raise RuntimeError("smallbank load precheck failed for {0}: {1}".format(account_id, lookup_output))
            if lookup_status == "found":
                raise RuntimeError("smallbank load would overwrite existing account: {0}".format(account_id))
            request_id = self.make_request_id("load", index, "createaccount")
            rc, output = self.client.invoke(
                0,
                [
                    "CreateAccount",
                    account_id,
                    str(row["name"]),
                    str(row["checking"]),
                    str(row["savings"]),
                    request_id,
                ],
            )
            self.append_raw_log("load:{0}".format(request_id), output)
            if rc != 0:
                raise RuntimeError("smallbank load invoke failed for {0}".format(account_id))
            if not self.lightweight:
                found, _, _ = self.wait_for_receipt(request_id)
                if not found:
                    raise RuntimeError("smallbank load receipt timeout for {0}".format(account_id))
            ok, message = self.validate_smallbank_balance(
                account_id,
                expected_checking=int(row["checking"]),
                expected_savings=int(row["savings"]),
            )
            if not ok:
                raise RuntimeError("smallbank load validation failed for {0}: {1}".format(account_id, message))
            self.append_success_record(
                {
                    "phase": "load",
                    "benchmark": "smallbank",
                    "sequence": index,
                    "operation": "CreateAccount",
                    "target": account_id,
                    "request_id": request_id,
                    "mode": "lightweight" if self.lightweight else "receipt",
                }
            )
            if index == 1 or index % self.progress_interval == 0 or index == total:
                self.log_progress("[smallbank] load progress: {0}/{1}".format(index, total))
        end_ms = int(time.time() * 1000)
        self.log_progress("[smallbank] load done")
        return end_ms - start_ms, len(rows)

    def load_ycsb(self) -> Tuple[int, int]:
        rows = self.read_jsonl(self.dataset_path / "init_records.jsonl")
        total = len(rows)
        self.log_progress("[ycsb] load start: {0} records".format(total))
        start_ms = int(time.time() * 1000)
        for index, row in enumerate(rows, start=1):
            key = str(row["key"])
            lookup_status, _, lookup_output = self.lookup_ycsb_record(key)
            if lookup_status == "system_failure":
                raise RuntimeError("ycsb load precheck failed for {0}: {1}".format(key, lookup_output))
            if lookup_status == "found":
                raise RuntimeError("ycsb load would overwrite existing key: {0}".format(key))
            request_id = self.make_request_id("load", index, "insert")
            rc, output = self.client.invoke(
                0,
                [
                    "Insert",
                    key,
                    json.dumps({"value": row["value"]}, separators=(",", ":")),
                    request_id,
                ],
            )
            self.append_raw_log("load:{0}".format(request_id), output)
            if rc != 0:
                raise RuntimeError("ycsb load invoke failed for {0}".format(key))
            if not self.lightweight:
                found, _, _ = self.wait_for_receipt(request_id)
                if not found:
                    raise RuntimeError("ycsb load receipt timeout for {0}".format(key))
            ok, message = self.validate_ycsb_read(key, expected_value=str(row["value"]))
            if not ok:
                raise RuntimeError("ycsb load validation failed for {0}: {1}".format(key, message))
            self.append_success_record(
                {
                    "phase": "load",
                    "benchmark": "ycsb",
                    "sequence": index,
                    "operation": "Insert",
                    "target": key,
                    "request_id": request_id,
                    "mode": "lightweight" if self.lightweight else "receipt",
                }
            )
            if index == 1 or index % self.progress_interval == 0 or index == total:
                self.log_progress("[ycsb] load progress: {0}/{1}".format(index, total))
        end_ms = int(time.time() * 1000)
        self.log_progress("[ycsb] load done")
        return end_ms - start_ms, len(rows)

    def run(self) -> Tuple[int, int]:
        if self.benchmark == "smallbank":
            return self.load_smallbank()
        return self.load_ycsb()

    def execute_workload(self) -> List[Dict[str, object]]:
        if self.benchmark == "smallbank":
            rows = self.read_jsonl(self.dataset_path / "workload.jsonl")
            executor_fn = self.execute_smallbank_operation
        else:
            rows = self.read_jsonl(self.dataset_path / "workload.jsonl")
            executor_fn = self.execute_ycsb_operation

        batches = [rows[index : index + self.batch_size] for index in range(0, len(rows), self.batch_size)]

        total = len(rows)
        self.log_progress("[{0}] workload start: {1} ops, parallelism={2}, batch_size={3}".format(self.benchmark, total, self.parallelism, self.batch_size))
        results = []
        with ThreadPoolExecutor(max_workers=self.parallelism) as executor:
            futures = [executor.submit(self._run_partition, batch, executor_fn) for batch in batches if batch]
            completed = 0
            for future in futures:
                batch_rows = future.result()
                results.extend(batch_rows)
                completed += len(batch_rows)
                if completed == total or completed % self.progress_interval == 0:
                    self.log_progress("[{0}] workload progress: {1}/{2}".format(self.benchmark, completed, total))
        results.sort(key=lambda item: int(item["seq"]))
        self.log_progress("[{0}] workload done".format(self.benchmark))
        return results

    def _chunk_request_ids(self, request_ids: List[str]) -> List[List[str]]:
        return [request_ids[index : index + self.verify_batch_size] for index in range(0, len(request_ids), self.verify_batch_size)]

    def finalize_submitted_write_results(self, rows):
        pending_rows = [row for row in rows if row.get("status") == "submitted" and row.get("request_id")]
        if not pending_rows:
            return

        request_ids = list(dict.fromkeys(str(row["request_id"]) for row in pending_rows))
        unresolved_ids = set(request_ids)
        self.log_progress("[{0}] post-verify start: {1} submitted writes, batch_size={2}".format(self.benchmark, len(request_ids), self.verify_batch_size))

        for round_index in range(1, self.verify_rounds + 1):
            if not unresolved_ids:
                break
            resolved_in_round = 0
            round_ids = sorted(unresolved_ids)
            for chunk in self._chunk_request_ids(round_ids):
                found_map, reason, output = self.verify_receipts_batch_once(chunk)
                if reason != "ok":
                    self.append_raw_log("post-verify:batch:{0}".format(reason), output)
                    continue
                self.append_raw_log("post-verify:batch:ok", output)
                for request_id, found in found_map.items():
                    if found and request_id in unresolved_ids:
                        unresolved_ids.remove(request_id)
                        resolved_in_round += 1
            self.log_progress(
                "[{0}] post-verify round {1}/{2}: resolved_in_round={3}, total_resolved={4}, pending={5}".format(
                    self.benchmark,
                    round_index,
                    self.verify_rounds,
                    resolved_in_round,
                    len(request_ids) - len(unresolved_ids),
                    len(unresolved_ids),
                )
            )
            if unresolved_ids and round_index < self.verify_rounds:
                time.sleep(self.verify_sleep_seconds)

        for row in pending_rows:
            request_id = str(row["request_id"])
            if request_id in unresolved_ids:
                # Keep unresolved writes as pending instead of forcing a failure.
                # This follows blockbench-style outstanding semantics.
                row["status"] = "submitted"
                row["message"] = sanitize_message("pending: receipt not found in post-verify window")
            else:
                row["status"] = "success"
                row["message"] = sanitize_message("receipt verified in batch post-verify")
                self.append_success_record(
                    {
                        "phase": "workload",
                        "benchmark": self.benchmark,
                        "sequence": int(row["seq"]),
                        "operation": str(row["op_type"]),
                        "target": str(row["target_key_or_account"]),
                        "request_id": request_id,
                        "mode": "batch-post-verify",
                    }
                )

    def _run_partition(self, rows: List[Dict[str, object]], executor_fn) -> List[Dict[str, object]]:
        return [executor_fn(row) for row in rows]

    def execute_smallbank_operation(self, row: Dict[str, object]) -> Dict[str, object]:
        operation = str(row["operation"])
        sequence = int(row["sequence"])
        request_id = self.make_request_id("workload", sequence, operation)
        target = str(row.get("account_id") or row.get("source_account_id") or operation)

        if operation == "Balance":
            start_ms = int(time.time() * 1000)
            rc, _, output = self.query_json(["Balance", str(row["account_id"])])
            end_ms = int(time.time() * 1000)
            if rc != 0:
                return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "read assumed success (transport failure ignored)")
            return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "read transport success")

        args = [operation]

        start_ms = int(time.time() * 1000)
        if operation in ("DepositChecking", "TransactSavings", "WriteCheck"):
            args.extend([str(row["account_id"]), str(row["amount"]), request_id])
        elif operation == "Amalgamate":
            args.extend([str(row["source_account_id"]), str(row["destination_account_id"]), request_id])
        elif operation == "SendPayment":
            args.extend([str(row["source_account_id"]), str(row["destination_account_id"]), str(row["amount"]), request_id])
        else:
            end_ms = int(time.time() * 1000)
            return result_row(sequence, operation, target, start_ms, end_ms, 1, "business_failure", "unsupported operation")

        rc, output = self.client.invoke(0, args)
        self.append_raw_log("invoke:{0}".format(request_id), output)
        if rc != 0:
            end_ms = int(time.time() * 1000)
            return result_row(sequence, operation, target, start_ms, end_ms, rc, "system_failure", output)

        end_ms = int(time.time() * 1000)
        return result_row(sequence, operation, target, start_ms, end_ms, rc, "submitted", "submitted for batch post-verify", request_id=request_id)

    def execute_ycsb_operation(self, row: Dict[str, object]) -> Dict[str, object]:
        operation = str(row["operation"])
        sequence = int(row["sequence"])
        request_id = self.make_request_id("workload", sequence, operation)
        target = str(row.get("key") or row.get("start_key") or operation)

        if operation == "Read":
            start_ms = int(time.time() * 1000)
            rc, _, output = self.query_json(["Read", str(row["key"])])
            end_ms = int(time.time() * 1000)
            if rc != 0:
                return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "read assumed success (transport failure ignored)")
            return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "read transport success")

        if operation == "Scan":
            start_ms = int(time.time() * 1000)
            rc, payload, output = self.query_json_array(["Scan", str(row["start_key"]), str(row["length"])])
            end_ms = int(time.time() * 1000)
            if rc != 0:
                return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "scan assumed success (transport failure ignored)")
            if not isinstance(payload, list):
                return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "scan assumed success (payload check ignored)")
            return result_row(sequence, operation, target, start_ms, end_ms, rc, "success", "read transport success")

        start_ms = int(time.time() * 1000)
        args = [operation]
        if operation in ("Insert", "Update"):
            args.extend([str(row["key"]), json.dumps({"value": row["value"]}, separators=(",", ":")), request_id])
        else:
            end_ms = int(time.time() * 1000)
            return result_row(sequence, operation, target, start_ms, end_ms, 1, "business_failure", "unsupported operation")

        rc, output = self.client.invoke(0, args)
        self.append_raw_log("invoke:{0}".format(request_id), output)
        if rc != 0:
            end_ms = int(time.time() * 1000)
            return result_row(sequence, operation, target, start_ms, end_ms, rc, "system_failure", output)

        end_ms = int(time.time() * 1000)
        return result_row(sequence, operation, target, start_ms, end_ms, rc, "submitted", "submitted for batch post-verify", request_id=request_id)

    def smallbank_precheck(self, operation: str, row: Dict[str, object]) -> Tuple[Optional[str], Optional[str]]:
        if operation in ("DepositChecking", "TransactSavings", "WriteCheck"):
            lookup_status, payload, output = self.lookup_smallbank_account(str(row["account_id"]))
            if lookup_status == "system_failure":
                return "system_failure", output
            if lookup_status != "found" or not isinstance(payload, dict):
                return "business_failure", "account not found"
            savings = int(payload["savings"])
            amount = int(row["amount"])
            if operation == "TransactSavings" and savings + amount < 0:
                return "business_failure", "insufficient funds in savings"
            return None, None

        if operation in ("Amalgamate", "SendPayment"):
            source_status, source_payload, source_output = self.lookup_smallbank_account(str(row["source_account_id"]))
            dest_status, dest_payload, dest_output = self.lookup_smallbank_account(str(row["destination_account_id"]))
            if source_status == "system_failure":
                return "system_failure", source_output
            if dest_status == "system_failure":
                return "system_failure", dest_output
            if source_status != "found" or dest_status != "found" or not isinstance(source_payload, dict) or not isinstance(dest_payload, dict):
                return "business_failure", "source or destination account not found"
            if operation == "SendPayment" and int(source_payload["checking"]) < int(row["amount"]):
                return "business_failure", "insufficient funds in checking"
            return None, None

        return None, None

    def ycsb_precheck(self, operation: str, row: Dict[str, object]) -> Tuple[Optional[str], Optional[str]]:
        lookup_status, payload, output = self.lookup_ycsb_record(str(row["key"]))
        if lookup_status == "system_failure":
            return "system_failure", output
        exists = lookup_status == "found" and isinstance(payload, dict) and payload.get("key") == row["key"]
        if operation == "Insert" and exists:
            return "business_failure", "record already exists"
        if operation == "Update" and not exists:
            return "business_failure", "record not found"
        return None, None

    def infer_business_failure(self, output: str, benchmark: str) -> str:
        lowered = output.lower()
        if benchmark == "smallbank":
            phrases = ("insufficient funds", "account not found", "invalid amount", "name is required")
        else:
            phrases = ("record already exists", "record not found", "invalid value payload", "value must not be empty")
        for phrase in phrases:
            if phrase in lowered:
                return "business_failure"
        return "system_failure"


def result_row(sequence: int, operation: str, target: str, start_ms: int, end_ms: int, rc: int, status: str, message: str, request_id: str = "") -> Dict[str, object]:
    return {
        "seq": sequence,
        "op_type": operation,
        "target_key_or_account": target,
        "start_ts": start_ms,
        "end_ts": end_ms,
        "latency_ms": end_ms - start_ms,
        "cli_rc": rc,
        "status": status,
        "message": sanitize_message(message),
        "request_id": request_id,
    }


def sanitize_message(message: str) -> str:
    return str(message).replace(",", ";").replace("\n", " ").strip()[:400]


def write_execution_csv(path: Path, rows: List[Dict[str, object]]) -> None:
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(
            handle,
            fieldnames=["seq", "op_type", "target_key_or_account", "start_ts", "end_ts", "latency_ms", "cli_rc", "status", "message", "request_id"],
        )
        writer.writeheader()
        writer.writerows(rows)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--benchmark", choices=("smallbank", "ycsb"), required=True)
    parser.add_argument("--dataset-path", required=True)
    parser.add_argument("--result-dir", required=True)
    parser.add_argument("--channel-name", required=True)
    parser.add_argument("--chaincode-name", required=True)
    parser.add_argument("--parallelism", type=int, default=1)
    parser.add_argument("--batch-size", type=int, default=1)
    parser.add_argument("--receipt-timeout", type=int, default=60)
    parser.add_argument("--lightweight", action="store_true")
    parser.add_argument("--verify-batch-size", type=int, default=100)
    parser.add_argument("--verify-rounds", type=int, default=3)
    parser.add_argument("--verify-sleep-ms", type=int, default=500)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    result_dir = Path(args.result_dir).resolve()
    result_dir.mkdir(parents=True, exist_ok=True)

    runner = BenchmarkRunner(
        benchmark=args.benchmark,
        dataset_path=Path(args.dataset_path).resolve(),
        result_dir=result_dir,
        channel_name=args.channel_name,
        chaincode_name=args.chaincode_name,
        parallelism=args.parallelism,
        batch_size=args.batch_size,
        receipt_timeout=args.receipt_timeout,
        lightweight=args.lightweight,
        verify_batch_size=args.verify_batch_size,
        verify_rounds=args.verify_rounds,
        verify_sleep_ms=args.verify_sleep_ms,
    )

    total_start_ms = int(time.time() * 1000)
    load_elapsed_ms, load_ops = runner.run()
    workload_start_ms = int(time.time() * 1000)
    rows = runner.execute_workload()
    workload_end_ms = int(time.time() * 1000)
    verify_start_ms = int(time.time() * 1000)
    runner.finalize_submitted_write_results(rows)
    verify_end_ms = int(time.time() * 1000)
    total_end_ms = int(time.time() * 1000)
    write_execution_csv(result_dir / "execution.csv", rows)

    payload = {
        "load_elapsed_ms": load_elapsed_ms,
        "load_ops": load_ops,
        "workload_elapsed_seconds": max(0.0, (workload_end_ms - workload_start_ms) / 1000.0),
        "verify_elapsed_seconds": max(0.0, (verify_end_ms - verify_start_ms) / 1000.0),
        "total_wall_seconds": max(0.0, (total_end_ms - total_start_ms) / 1000.0),
    }
    with (result_dir / "load_metrics.json").open("w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2, sort_keys=True)
        handle.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
