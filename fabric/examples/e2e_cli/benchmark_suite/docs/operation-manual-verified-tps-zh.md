# Fabric v1.0 Benchmark Suite 完整操作说明（Verified TPS 口径）

本文档给出当前 `examples/e2e_cli/benchmark_suite` 的完整操作流程与指标口径，适用于 Smallbank 与 YCSB。

## 1. 目标与口径

当前测试流程采用两阶段：

1. workload 提交阶段（低干扰）
- 读操作：query 返回成功即记 `success`
- 写操作：invoke 返回成功即记 `submitted`

2. post-verify 阶段（批量确认）
- 对 `submitted` 写操作按批调用链码 `BatchGetReceipts`
- 确认成功写回 `success`
- 多轮后仍未确认写回 `business_failure`

主吞吐指标为 `verified_tps`（与 `summary.json` 中 `tps` 同口径）：

- `verified_tps = successful_ops / workload_elapsed_seconds`
- 其中 `successful_ops` 是最终 `execution.csv` 中 `status=success` 的数量
- 分母只使用 workload 提交阶段耗时，不包含 post-verify 阶段

## 2. 前置条件

在 Linux 或等价环境中准备：

- `bash`
- `python3`
- `docker`
- `docker compose` 或 `docker-compose`

并确保 Fabric v1.0 `examples/e2e_cli` 可正常运行。

## 3. 部署步骤

工作目录：

```bash
cd examples/e2e_cli/benchmark_suite
```

赋予脚本执行权限（首次）：

```bash
chmod +x scripts/deploy/*.sh
chmod +x scripts/run/*.sh
chmod +x scripts/common/*.sh
```

### 3.1 Smallbank 部署

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
```

### 3.2 YCSB 部署

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
```

## 4. 执行步骤

### 4.1 Smallbank 运行

```bash
bash scripts/run/smallbank_load_and_run.sh \
  --generate \
  --dataset-name sb-exp-verified \
  --account-start-id 100000 \
  --account-count 1000 \
  --operation-count 10000 \
  --conflict-rate 0.8 \
  --hot-account-ratio 0.2 \
  --max-debit-fraction 0.05 \
  --parallelism 4 \
  --batch-size 10 \
  --verify-batch-size 100 \
  --verify-rounds 3 \
  --verify-sleep-ms 500
```

### 4.2 YCSB 运行（Zipfian 示例）

```bash
bash scripts/run/ycsb_load_and_run.sh \
  --generate \
  --dataset-name ycsb-zipf-verified \
  --key-start 200000 \
  --record-count 1000 \
  --operation-count 10000 \
  --distribution zipfian \
  --zipf-theta 0.99 \
  --read-ratio 0.5 \
  --update-ratio 0.3 \
  --insert-ratio 0.1 \
  --scan-ratio 0.1 \
  --parallelism 4 \
  --batch-size 10 \
  --verify-batch-size 100 \
  --verify-rounds 3 \
  --verify-sleep-ms 500
```

## 5. 关键参数说明

- `parallelism`: 客户端并发线程数
- `batch-size`: 客户端任务分块大小（不是链上批处理）
- `verify-batch-size`: 每次 `BatchGetReceipts` 请求携带的 request_id 数量
- `verify-rounds`: post-verify 最大轮数
- `verify-sleep-ms`: post-verify 轮间等待时间（毫秒）

为减少余额不足失败，建议：

- 使用更高初始余额模板（当前默认模板已提高初始 checking/savings）
- `max-debit-fraction` 使用 `0.05` 左右
- 保持 `strict_valid_workload=true`

建议先固定 `verify-*` 参数再做对比实验，保证结果可比性。

## 6. 输出文件与指标解读

结果目录：

- `results/smallbank/<run_id>/`
- `results/ycsb/<run_id>/`

关键文件：

- `execution.csv`: 每条操作结果（含 `submitted/success/business_failure/system_failure`）
- `summary.json`: 聚合指标
- `summary.md`: 人类可读摘要
- `load_metrics.json`: load/workload/verify/total 阶段耗时
- `raw.log`: 原始 invoke/query 输出
- `success_records.jsonl`: 已确认成功写操作记录

关键指标（`summary.json`）：

- `successful_ops`
- `read_success_ops`
- `write_submitted_ops`
- `write_verified_success_ops`
- `confirm_rate`
- `verified_tps`（同 `tps`）
- `load_tps`

阶段耗时（`load_metrics.json`）：

- `workload_elapsed_seconds`
- `verify_elapsed_seconds`
- `total_wall_seconds`

## 7. 复现实验建议

1. 固定链码版本、数据集参数、`parallelism`、`batch-size`、`verify-*`
2. 每组参数至少运行 3 次，记录均值与波动
3. 并列报告：
- `verified_tps`
- `confirm_rate`
- `p50_latency_ms` / `p95_latency_ms`
- `verify_elapsed_seconds`

## 8. 失败排查

- `system_failure` 偏高：优先检查网络、容器状态、CLI 连通性
- `business_failure` 偏高：检查数据集约束（如 Smallbank 余额、YCSB 插入/更新冲突）
- `confirm_rate` 偏低：适当提高 `verify-rounds` 或 `verify-sleep-ms`

## 9. 网络清理

```bash
bash scripts/deploy/network_down.sh
```

如需删除 compose 卷：

```bash
bash scripts/deploy/network_down.sh --remove-volumes
```
