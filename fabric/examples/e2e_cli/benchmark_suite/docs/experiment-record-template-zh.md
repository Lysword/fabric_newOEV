# Fabric 基准测试实验记录模板

## 1. 文档目的

本文档是一个可复用的实验记录模板，用于在每次运行 `smallbank` 或 `ycsb` 基准测试后，快速记录：

- 本次实验的目标
- 使用的参数
- 运行环境
- 结果文件路径
- 吞吐、成功率、延迟等关键指标
- 异常现象与原因分析
- 后续调整方向

建议做法是：

- 保留本模板文件不变，作为母版
- 每做一次正式实验，就复制一份并按实验名称重命名

建议命名格式：

```text
docs/experiment-record-<日期>-<benchmark>-<主题>.md
```

例如：

```text
docs/experiment-record-2026-05-25-smallbank-high-conflict.md
docs/experiment-record-2026-05-25-ycsb-zipf-theta-099.md
```

## 2. 填写原则

- 尽量记录“真实执行时使用的参数”，不要只写计划参数
- 每条结论尽量对应一组结果文件
- 如果中途改了数据集参数、脚本参数或网络环境，要明确写出来
- 如果实验失败，也要保留失败记录，方便后续排查

## 3. 模板正文

下面内容建议直接复制到新的实验记录文件中使用。

---

# 实验记录：<实验标题>

## 1. 基本信息

- 实验日期：
- 记录人：
- 基准类型：`smallbank` / `ycsb`
- 实验目的：
- 是否正式实验：`是` / `否`
- 对应代码目录：`examples/e2e_cli/benchmark_suite`

## 2. 环境信息

- 操作系统：
- Fabric 版本：`v1.0`
- Docker 版本：
- Docker Compose 版本：
- Python 版本：
- CPU / 核心数：
- 内存：
- 是否为最终实验机：`是` / `否`
- 备注：

## 3. 部署信息

- Channel 名称：
- Chaincode 名称：
- Chaincode 版本：
- 部署脚本：
- 部署命令：

示例：

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
```

或：

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
```

## 4. 数据集信息

- 数据集名称：
- 数据集目录：
- 是否本次新生成：`是` / `否`
- 生成脚本：
- 生成命令：
- `manifest.json` 路径：
- 初始数据文件路径：
- workload 文件路径：

### 4.1 Smallbank 参数

仅在 `smallbank` 实验时填写：

- `account_start_id`：
- `account_count`：
- `operation_count`：
- `seed`：
- `conflict_rate`：
- `hot_account_ratio`：
- `max_debit_fraction`：
- `strict_valid_workload`：
- `balance_ratio`：
- `deposit_checking_ratio`：
- `transact_savings_ratio`：
- `amalgamate_ratio`：
- `write_check_ratio`：
- `send_payment_ratio`：

### 4.2 YCSB 参数

仅在 `ycsb` 实验时填写：

- `key_prefix`：
- `key_start`：
- `record_count`：
- `operation_count`：
- `seed`：
- `distribution`：`uniform` / `zipfian`
- `zipf_theta`：
- `read_ratio`：
- `update_ratio`：
- `insert_ratio`：
- `scan_ratio`：
- `scan_length_min`：
- `scan_length_max`：
- `value_size`：

## 5. 运行参数

- 运行脚本：
- 运行命令：
- `parallelism`：
- `batch_size`：
- 是否使用 `--generate`：
- 是否复用已有数据集：
- 本次是否使用新的账户范围或 key 范围：

Smallbank 典型示例：

```bash
bash scripts/run/smallbank_load_and_run.sh \
  --generate \
  --dataset-name sb-exp-1 \
  --account-start-id 100000 \
  --account-count 1000 \
  --operation-count 10000 \
  --conflict-rate 0.8 \
  --hot-account-ratio 0.2 \
  --max-debit-fraction 0.15 \
  --parallelism 4 \
  --batch-size 10
```

YCSB 典型示例：

```bash
bash scripts/run/ycsb_load_and_run.sh \
  --generate \
  --dataset-name ycsb-zipf-exp \
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
  --batch-size 10
```

## 6. 结果文件

- `run_id`：
- 结果目录：
- `run_manifest.json`：
- `execution.csv`：
- `summary.json`：
- `summary.md`：
- `raw.log`：
- `load_metrics.json`：

## 7. 核心结果

从 `summary.json` 或 `summary.md` 中填写：

- `attempted_ops`：
- `successful_ops`：
- `failed_ops`：
- `business_failures`：
- `system_failures`：
- `success_rate`：
- `elapsed_seconds`：
- `tps`：
- `avg_latency_ms`：
- `p50_latency_ms`：
- `p95_latency_ms`：
- `load_elapsed_seconds`：
- `load_tps`：

## 8. 实验现象记录

本节记录实验中观察到的现象，不急着下结论，先把事实写清楚。

建议记录：

- 是否顺利完成部署
- load 阶段是否顺利
- 是否出现账户或 key 已存在
- 是否出现业务失败
- 是否出现系统失败
- 是否出现明显性能波动
- `raw.log` 中是否有异常输出

可直接填写：

- 现象 1：
- 现象 2：
- 现象 3：

## 9. 原因分析

本节写你对现象的解释，建议把“看到的事实”和“推测的原因”分开。

建议从以下角度分析：

- 是否由热点冲突导致
- 是否由并发度变化导致
- 是否由数据集规模变化导致
- 是否由 Zipfian 分布导致热点集中
- 是否由链码业务逻辑导致业务失败
- 是否由环境不稳定导致系统失败

可直接填写：

- 分析 1：
- 分析 2：
- 分析 3：

## 10. 本次结论

请用尽量简洁、明确的话总结本次实验。

可按下面格式填写：

- 结论 1：
- 结论 2：
- 结论 3：

示例：

- 当 `parallelism=4` 且 `conflict_rate=0.8` 时，Smallbank 的 `tps` 明显低于低冲突场景。
- 在 `distribution=zipfian`、`zipf_theta=0.99` 的 YCSB 场景中，热点 key 导致延迟尾部上升。
- 本次实验没有出现额外业务失败，说明数据集生成器基本达到了“避免余额不足”的目标。

## 11. 后续动作

写明下一步具体要做什么，避免只写“后续再看”。

建议写法：

- 下一组实验准备怎么改参数
- 是否要更换账户范围或 key 范围
- 是否要重启全新网络
- 是否要回查 `raw.log`
- 是否要调整图表或统计脚本

可直接填写：

- 动作 1：
- 动作 2：
- 动作 3：

## 12. 附加备注

- 备注 1：
- 备注 2：

---

## 4. 建议配套做法

为了让后续复盘更轻松，建议每次正式实验至少保留以下三样东西：

- 一份实验记录文档
- 一份对应的结果目录
- 一条能直接复现的命令

推荐把三者建立明确对应关系：

- 实验记录文档里写清 `run_id`
- `run_id` 对应 `results/<benchmark>/<run_id>/`
- 记录文档里保留完整命令行

这样后续无论是写论文、重新画图，还是在新窗口继续工作，都能快速找回上下文。
