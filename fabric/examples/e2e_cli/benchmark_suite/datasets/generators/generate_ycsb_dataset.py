#!/usr/bin/env python3

import argparse
import json
import random
import string
from collections import Counter
from datetime import datetime
from pathlib import Path


SCRIPT_PATH = Path(__file__).resolve()
DATASETS_ROOT = SCRIPT_PATH.parent.parent
DEFAULT_TEMPLATE = DATASETS_ROOT / "templates" / "ycsb.default.json"
GENERATED_ROOT = DATASETS_ROOT / "generated" / "ycsb"


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--template", default=None)
    parser.add_argument("--output-name", required=True)
    parser.add_argument("--key-start", type=int)
    parser.add_argument("--record-count", type=int)
    parser.add_argument("--operation-count", type=int)
    parser.add_argument("--seed", type=int)
    parser.add_argument("--distribution", choices=("uniform", "zipfian"))
    parser.add_argument("--zipf-theta", type=float)
    parser.add_argument("--read-ratio", type=float)
    parser.add_argument("--update-ratio", type=float)
    parser.add_argument("--insert-ratio", type=float)
    parser.add_argument("--scan-ratio", type=float)
    parser.add_argument("--scan-length-min", type=int)
    parser.add_argument("--scan-length-max", type=int)
    parser.add_argument("--value-size", type=int)
    return parser.parse_args()


def load_template(path):
    template_path = Path(path).resolve() if path else DEFAULT_TEMPLATE
    with template_path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def apply_overrides(config, args):
    for key in (
        "record_count",
        "operation_count",
        "seed",
        "key_start",
        "distribution",
        "zipf_theta",
        "read_ratio",
        "update_ratio",
        "insert_ratio",
        "scan_ratio",
        "scan_length_min",
        "scan_length_max",
        "value_size",
    ):
        value = getattr(args, key)
        if value is not None:
            config[key] = value
    return config


def validate_config(config):
    if int(config["record_count"]) <= 0:
        raise ValueError("record_count must be positive")
    if int(config["operation_count"]) <= 0:
        raise ValueError("operation_count must be positive")
    if config["distribution"] not in {"uniform", "zipfian"}:
        raise ValueError("distribution must be one of: uniform, zipfian")
    if int(config["value_size"]) <= 0:
        raise ValueError("value_size must be positive")
    if int(config["scan_length_min"]) <= 0:
        raise ValueError("scan_length_min must be positive")
    if int(config["scan_length_max"]) < int(config["scan_length_min"]):
        raise ValueError("scan_length_max must be greater than or equal to scan_length_min")
    ratio_sum = (
        float(config["read_ratio"])
        + float(config["update_ratio"])
        + float(config["insert_ratio"])
        + float(config["scan_ratio"])
    )
    if abs(ratio_sum - 1.0) > 1e-9:
        raise ValueError("ycsb ratios must sum to 1.0, got {0}".format(ratio_sum))
    if float(config["zipf_theta"]) <= 0.0:
        raise ValueError("zipf_theta must be positive")


def key_for(index, key_prefix, key_start):
    return "{0}{1:06d}".format(key_prefix, key_start + index)


def build_initial_keys(config):
    record_count = int(config["record_count"])
    key_prefix = str(config["key_prefix"])
    key_start = int(config["key_start"])
    return [key_for(index, key_prefix, key_start) for index in range(record_count)]


def random_value(rng, value_size):
    alphabet = string.ascii_lowercase + string.digits
    return "".join(rng.choice(alphabet) for _ in range(value_size))


def weighted_pick(rng, names, weights):
    total = sum(weights)
    if total <= 0:
        raise ValueError("weights must sum to a positive value")
    hit = rng.random() * total
    running = 0.0
    for name, weight in zip(names, weights):
        running += weight
        if hit <= running:
            return name
    return names[-1]


def zipfian_choice(keys, rng, zipf_theta):
    weights = [1.0 / ((index + 1) ** zipf_theta) for index in range(len(keys))]
    return weighted_pick(rng, list(keys), weights)


def choose_existing_key(keys, distribution, rng, zipf_theta):
    if distribution == "uniform":
        return rng.choice(list(keys))
    return zipfian_choice(keys, rng, zipf_theta)


def choose_operation(config, rng):
    names = ["Read", "Update", "Insert", "Scan"]
    weights = [
        float(config["read_ratio"]),
        float(config["update_ratio"]),
        float(config["insert_ratio"]),
        float(config["scan_ratio"]),
    ]
    return weighted_pick(rng, names, weights)


def choose_scan_start_index(keys, distribution, rng, zipf_theta):
    chosen_key = choose_existing_key(keys, distribution, rng, zipf_theta)
    return list(keys).index(chosen_key)


def generate_workload(config, initial_keys, rng):
    live_keys = list(initial_keys)
    workload = []
    next_insert_index = int(config["record_count"])
    key_prefix = str(config["key_prefix"])
    key_start = int(config["key_start"])
    distribution = str(config["distribution"])
    zipf_theta = float(config["zipf_theta"])
    scan_min = int(config["scan_length_min"])
    scan_max = int(config["scan_length_max"])
    value_size = int(config["value_size"])

    for sequence in range(1, int(config["operation_count"]) + 1):
        operation = choose_operation(config, rng)

        if operation == "Insert":
            key = key_for(next_insert_index, key_prefix, key_start)
            next_insert_index += 1
            live_keys.append(key)
            workload.append(
                {
                    "sequence": sequence,
                    "operation": "Insert",
                    "key": key,
                    "value": random_value(rng, value_size),
                }
            )
            continue

        key = choose_existing_key(live_keys, distribution, rng, zipf_theta)
        if operation == "Read":
            workload.append({"sequence": sequence, "operation": "Read", "key": key})
            continue

        if operation == "Update":
            workload.append(
                {
                    "sequence": sequence,
                    "operation": "Update",
                    "key": key,
                    "value": random_value(rng, value_size),
                }
            )
            continue

        if operation == "Scan":
            start_index = choose_scan_start_index(live_keys, distribution, rng, zipf_theta)
            length = rng.randint(scan_min, scan_max)
            start_key = live_keys[start_index]
            workload.append(
                {
                    "sequence": sequence,
                    "operation": "Scan",
                    "start_key": start_key,
                    "length": min(length, len(live_keys) - start_index),
                }
            )
            continue

        raise ValueError("Unsupported operation: {0}".format(operation))

    return workload


def write_jsonl(path, rows):
    with path.open("w", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            handle.write(json.dumps(row, sort_keys=True))
            handle.write("\n")


def write_manifest(path, config, output_name, init_count, workload):
    manifest = {
        "benchmark": "ycsb",
        "dataset_name": output_name,
        "generated_at": datetime.utcnow().isoformat() + "Z",
        "seed": int(config["seed"]),
        "record_count": init_count,
        "operation_count": len(workload),
        "distribution": str(config["distribution"]),
        "zipf_theta": float(config["zipf_theta"]),
        "files": {
            "manifest": "manifest.json",
            "init": "init_records.jsonl",
            "workload": "workload.jsonl",
        },
        "operation_mix": dict(sorted(Counter(item["operation"] for item in workload).items())),
    }
    with path.open("w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=True)
        handle.write("\n")


def main():
    args = parse_args()
    config = apply_overrides(load_template(args.template), args)
    validate_config(config)

    rng = random.Random(int(config["seed"]))
    initial_keys = build_initial_keys(config)
    output_dir = GENERATED_ROOT / args.output_name
    output_dir.mkdir(parents=True, exist_ok=True)

    init_rows = [
        {
            "key": key,
            "value": random_value(rng, int(config["value_size"])),
        }
        for key in initial_keys
    ]
    workload = generate_workload(config, initial_keys, rng)

    write_manifest(output_dir / "manifest.json", config, args.output_name, len(init_rows), workload)
    write_jsonl(output_dir / "init_records.jsonl", init_rows)
    write_jsonl(output_dir / "workload.jsonl", workload)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
