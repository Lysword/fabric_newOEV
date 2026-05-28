#!/usr/bin/env python3

import argparse
import json
import random
from collections import Counter
from datetime import datetime
from pathlib import Path


SCRIPT_PATH = Path(__file__).resolve()
DATASETS_ROOT = SCRIPT_PATH.parent.parent
DEFAULT_TEMPLATE = DATASETS_ROOT / "templates" / "smallbank.default.json"
GENERATED_ROOT = DATASETS_ROOT / "generated" / "smallbank"


class AccountState(object):
    def __init__(self, account_id, name, checking, savings):
        self.account_id = account_id
        self.name = name
        self.checking = checking
        self.savings = savings


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--template", default=None)
    parser.add_argument("--output-name", required=True)
    parser.add_argument("--account-start-id", type=int)
    parser.add_argument("--account-count", type=int)
    parser.add_argument("--operation-count", type=int)
    parser.add_argument("--seed", type=int)
    parser.add_argument("--conflict-rate", type=float)
    parser.add_argument("--hot-account-ratio", type=float)
    parser.add_argument("--max-debit-fraction", type=float)
    return parser.parse_args()


def load_template(path):
    template_path = Path(path).resolve() if path else DEFAULT_TEMPLATE
    with template_path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def apply_overrides(config, args):
    for key in (
        "account_start_id",
        "account_count",
        "operation_count",
        "seed",
        "conflict_rate",
        "hot_account_ratio",
        "max_debit_fraction",
    ):
        value = getattr(args, key)
        if value is not None:
            config[key] = value
    return config


def validate_config(config):
    if int(config["account_count"]) <= 1:
        raise ValueError("account_count must be greater than 1")
    if int(config["operation_count"]) <= 0:
        raise ValueError("operation_count must be positive")
    if not 0.0 <= float(config["conflict_rate"]) <= 1.0:
        raise ValueError("conflict_rate must be between 0 and 1")
    if not 0.0 <= float(config["hot_account_ratio"]) <= 1.0:
        raise ValueError("hot_account_ratio must be between 0 and 1")
    if not 0.0 < float(config["max_debit_fraction"]) <= 1.0:
        raise ValueError("max_debit_fraction must be between 0 and 1")

    ratio_keys = (
        "balance_ratio",
        "deposit_checking_ratio",
        "transact_savings_ratio",
        "amalgamate_ratio",
        "write_check_ratio",
        "send_payment_ratio",
    )
    ratio_sum = sum(float(config[key]) for key in ratio_keys)
    if abs(ratio_sum - 1.0) > 1e-9:
        raise ValueError("smallbank ratios must sum to 1.0, got {0}".format(ratio_sum))


def account_id_for(index, start_id):
    return "acct-{0:06d}".format(start_id + index)


def build_accounts(config, rng):
    accounts = []
    count = int(config["account_count"])
    start_id = int(config["account_start_id"])
    checking_min = int(config["initial_checking_min"])
    checking_max = int(config["initial_checking_max"])
    savings_min = int(config["initial_savings_min"])
    savings_max = int(config["initial_savings_max"])
    scale = float(config["initial_balance_scale"])

    for index in range(count):
        checking = max(1, int(rng.randint(checking_min, checking_max) * scale))
        savings = max(1, int(rng.randint(savings_min, savings_max) * scale))
        account_id = account_id_for(index, start_id)
        accounts.append(
            AccountState(
                account_id=account_id,
                name="user-{0:06d}".format(start_id + index),
                checking=checking,
                savings=savings,
            )
        )
    return accounts


def build_selection_pools(config, accounts):
    account_ids = [account.account_id for account in accounts]
    hot_count = max(1, min(len(account_ids), int(round(len(account_ids) * float(config["hot_account_ratio"])))))
    hot_ids = account_ids[:hot_count]
    return account_ids, hot_ids


def choose_account(account_ids, hot_ids, rng, conflict_rate):
    pool = hot_ids if rng.random() < conflict_rate else account_ids
    return rng.choice(list(pool))


def choose_two_accounts(account_ids, hot_ids, rng, conflict_rate):
    source = choose_account(account_ids, hot_ids, rng, conflict_rate)
    dest = choose_account(account_ids, hot_ids, rng, conflict_rate)
    while dest == source:
        dest = choose_account(account_ids, hot_ids, rng, conflict_rate)
    return source, dest


def weighted_operation_types(config):
    return [
        ("Balance", float(config["balance_ratio"])),
        ("DepositChecking", float(config["deposit_checking_ratio"])),
        ("TransactSavings", float(config["transact_savings_ratio"])),
        ("Amalgamate", float(config["amalgamate_ratio"])),
        ("WriteCheck", float(config["write_check_ratio"])),
        ("SendPayment", float(config["send_payment_ratio"])),
    ]


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


def choose_operation(config, rng):
    operation_types = weighted_operation_types(config)
    names = [name for name, _weight in operation_types]
    weights = [weight for _name, weight in operation_types]
    return weighted_pick(rng, names, weights)


def max_debit_amount(balance, fraction):
    return max(1, int(balance * fraction))


def candidate_with_shadow_state(op_name, shadow, account_ids, hot_ids, config, rng):
    conflict_rate = float(config["conflict_rate"])
    max_fraction = float(config["max_debit_fraction"])

    if op_name == "Balance":
        account_id = choose_account(account_ids, hot_ids, rng, conflict_rate)
        return {"operation": op_name, "account_id": account_id}

    if op_name == "DepositChecking":
        account_id = choose_account(account_ids, hot_ids, rng, conflict_rate)
        amount = rng.randint(1, max(1, max_debit_amount(shadow[account_id].checking, max_fraction)))
        shadow[account_id].checking += amount
        return {"operation": op_name, "account_id": account_id, "amount": amount}

    if op_name == "TransactSavings":
        account_id = choose_account(account_ids, hot_ids, rng, conflict_rate)
        available = shadow[account_id].savings
        if available < 1:
            return None
        debit_cap = max_debit_amount(available, max_fraction)
        amount = -rng.randint(1, min(available, debit_cap))
        shadow[account_id].savings += amount
        return {"operation": op_name, "account_id": account_id, "amount": amount}

    if op_name == "Amalgamate":
        source_id, dest_id = choose_two_accounts(account_ids, hot_ids, rng, conflict_rate)
        total = shadow[source_id].checking + shadow[source_id].savings
        if total <= 0:
            return None
        shadow[source_id].checking = 0
        shadow[source_id].savings = 0
        shadow[dest_id].checking += total
        return {"operation": op_name, "source_account_id": source_id, "destination_account_id": dest_id}

    if op_name == "WriteCheck":
        account_id = choose_account(account_ids, hot_ids, rng, conflict_rate)
        available = shadow[account_id].checking + shadow[account_id].savings
        debit_cap = max_debit_amount(available, max_fraction)
        if available < 1 or debit_cap < 1:
            return None
        amount = rng.randint(1, min(available, debit_cap))
        shadow[account_id].checking -= amount
        return {"operation": op_name, "account_id": account_id, "amount": amount}

    if op_name == "SendPayment":
        source_id, dest_id = choose_two_accounts(account_ids, hot_ids, rng, conflict_rate)
        available = shadow[source_id].checking
        debit_cap = max_debit_amount(available, max_fraction)
        if available < 1 or debit_cap < 1:
            return None
        amount = rng.randint(1, min(available, debit_cap))
        shadow[source_id].checking -= amount
        shadow[dest_id].checking += amount
        return {
            "operation": op_name,
            "source_account_id": source_id,
            "destination_account_id": dest_id,
            "amount": amount,
        }

    raise ValueError("Unsupported operation: {0}".format(op_name))


def generate_workload(config, accounts, rng):
    account_ids, hot_ids = build_selection_pools(config, accounts)
    shadow = {
        account.account_id: AccountState(
            account_id=account.account_id,
            name=account.name,
            checking=account.checking,
            savings=account.savings,
        )
        for account in accounts
    }

    operation_count = int(config["operation_count"])
    strict_valid_workload = bool(config["strict_valid_workload"])
    workload = []

    for sequence in range(1, operation_count + 1):
        operation = choose_operation(config, rng)
        candidate = None
        for _attempt in range(100):
            snapshot = {
                account_id: AccountState(
                    account_id=state.account_id,
                    name=state.name,
                    checking=state.checking,
                    savings=state.savings,
                )
                for account_id, state in shadow.items()
            }
            candidate = candidate_with_shadow_state(operation, snapshot, account_ids, hot_ids, config, rng)
            if candidate is not None:
                shadow = snapshot
                candidate["sequence"] = sequence
                workload.append(candidate)
                break
        if candidate is None:
            if strict_valid_workload:
                raise RuntimeError("Unable to generate valid {0} operation at sequence {1}".format(operation, sequence))
            workload.append(
                {
                    "sequence": sequence,
                    "operation": "Balance",
                    "account_id": choose_account(account_ids, hot_ids, rng, float(config["conflict_rate"])),
                }
            )

    return workload


def write_jsonl(path, rows):
    with path.open("w", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            handle.write(json.dumps(row, sort_keys=True))
            handle.write("\n")


def write_manifest(path, config, output_name, accounts, workload):
    op_counts = Counter(item["operation"] for item in workload)
    manifest = {
        "benchmark": "smallbank",
        "dataset_name": output_name,
        "generated_at": datetime.utcnow().isoformat() + "Z",
        "seed": int(config["seed"]),
        "account_count": len(accounts),
        "operation_count": len(workload),
        "conflict_rate": float(config["conflict_rate"]),
        "hot_account_ratio": float(config["hot_account_ratio"]),
        "strict_valid_workload": bool(config["strict_valid_workload"]),
        "files": {
            "manifest": "manifest.json",
            "init": "init_accounts.jsonl",
            "workload": "workload.jsonl",
        },
        "operation_mix": dict(sorted(op_counts.items())),
    }
    with path.open("w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=True)
        handle.write("\n")


def main():
    args = parse_args()
    config = apply_overrides(load_template(args.template), args)
    validate_config(config)

    rng = random.Random(int(config["seed"]))
    accounts = build_accounts(config, rng)
    workload = generate_workload(config, accounts, rng)

    output_dir = GENERATED_ROOT / args.output_name
    output_dir.mkdir(parents=True, exist_ok=True)

    init_rows = [
        {
            "account_id": account.account_id,
            "name": account.name,
            "checking": account.checking,
            "savings": account.savings,
        }
        for account in accounts
    ]

    write_manifest(output_dir / "manifest.json", config, args.output_name, accounts, workload)
    write_jsonl(output_dir / "init_accounts.jsonl", init_rows)
    write_jsonl(output_dir / "workload.jsonl", workload)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
