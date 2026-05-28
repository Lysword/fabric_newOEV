# Fabric 基准测试流程细节说明

## 1. 文档用途

本文档按“真正做实验时的操作顺序”细化说明整个流程，重点回答以下问题：

- 每一步应该执行什么
- 每一步背后调用了哪些脚本
- 数据集是怎么生成和导入的
- 成功率、吞吐、延迟是怎么统计出来的
- 如果后续要改参数、重跑实验、扩展链码，应该从哪里入手

如果你先想看全局，再看细节，建议先读：

- `docs/architecture-overview-zh.md`

## 2. 一次实验的标准步骤

标准顺序如下：

1. 把需要的文件拷到 Linux 实验机
2. 确认 `examples/e2e_cli` 的 Fabric v1.0 环境可用
3. 进入 `examples/e2e_cli/benchmark_suite`
4. 执行部署脚本
5. 执行运行脚本
6. 查看结果目录
7. 调整参数并重复实验

## 3. 第一步：拷贝哪些文件

最少需要拷贝：

- `examples/e2e_cli/docker-compose-cli.yaml`
- `examples/e2e_cli/benchmark_suite/`

原因是新的 `docker-compose-cli.yaml` 增加了下面这条挂载：

```text
./benchmark_suite:/opt/gopath/src/github.com/hyperledger/fabric/examples/e2e_cli/benchmark_suite
```

没有这条挂载，`cli` 容器内部就看不到新增的 benchmark 链码和脚本。

## 4. 第二步：Linux 侧前置条件

需要具备：

- `bash`
- `python3`
- `docker`
- `docker compose` 或 `docker-compose`

并且实验机上的 Fabric 仓库中，应当有可用的 `examples/e2e_cli` 基础内容，例如：

- `generateArtifacts.sh`
- `base/docker-compose-base.yaml`
- 原始脚本依赖的证书和 channel 生成逻辑

## 5. 第三步：目录与入口脚本

进入目录：

```bash
cd examples/e2e_cli/benchmark_suite
```

建议先赋执行权限：

```bash
chmod +x scripts/deploy/*.sh
chmod +x scripts/run/*.sh
chmod +x scripts/common/*.sh
```

这里有 4 个主要入口脚本：

- `scripts/deploy/smallbank_up_and_deploy.sh`
- `scripts/run/smallbank_load_and_run.sh`
- `scripts/deploy/ycsb_up_and_deploy.sh`
- `scripts/run/ycsb_load_and_run.sh`

你可以把它们理解为：

- `deploy/*` 负责“网络启动 + 链码部署”
- `run/*` 负责“数据生成 + 数据导入 + workload 执行 + 指标输出”

## 6. 第四步：部署阶段到底做了什么

### 6.1 Smallbank 部署

命令：

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
```

这个脚本会：

1. 加载 `configs/smallbank/default.env`
2. 检查 Docker 与 compose
3. 检查网络是否已启动
4. 如未启动，则启动 `e2e_cli` 网络
5. 在 Org1 peer0 和 Org2 peer0 上安装 `smallbank` 链码
6. 在目标 channel 上实例化链码
7. 调用一次 `Ping` 做健康检查

### 6.2 YCSB 部署

命令：

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
```

流程与 Smallbank 相同，只是链码路径和链码名不同。

### 6.3 公共底层逻辑

部署脚本底层主要依赖：

- `scripts/common/lib.sh`
- `scripts/common/peer_env.sh`

它们负责：

- 识别 `docker compose` 还是 `docker-compose`
- 设置不同 peer 的环境变量
- 执行 `peer chaincode install`
- 执行 `peer chaincode instantiate`
- 执行 `peer chaincode query`
- 判断链码是否已安装、是否已实例化

也就是说，部署脚本已经具备一定幂等性，不是每次都强制重复安装和实例化。

## 7. 第五步：Smallbank 的数据集生成与执行

### 7.1 一条命令跑通

```bash
bash scripts/run/smallbank_load_and_run.sh --generate
```

如果加上 `--generate`，脚本会先生成数据集，再执行导入和 workload。

### 7.2 Smallbank 数据集生成器的输入

生成器文件：

- `datasets/generators/generate_smallbank_dataset.py`

默认模板：

- `datasets/templates/smallbank.default.json`

默认模板里可以控制：

- `account_start_id`
- `account_count`
- `operation_count`
- `seed`
- `hot_account_ratio`
- `conflict_rate`
- `initial_checking_min`
- `initial_checking_max`
- `initial_savings_min`
- `initial_savings_max`
- `initial_balance_scale`
- `max_debit_fraction`
- `strict_valid_workload`
- 各类操作比例

### 7.3 Smallbank 为什么尽量不会出现余额不足

生成器维护了一个影子状态 `shadow state`，在生成 workload 时会先模拟执行，再决定是否把该操作写入数据集。

这会带来两个效果：

- `TransactSavings` 会尽量避免生成超出 savings 余额的扣款
- `SendPayment` 会尽量避免生成超出 checking 余额的转账

所以在正常默认配置下，Smallbank 的 workload 目标是“尽量没有额外业务失败”，更适合专注测试 Fabric 的执行性能与冲突影响。

### 7.4 Smallbank run 脚本做了什么

`scripts/run/smallbank_load_and_run.sh` 主要流程是：

1. 加载 `configs/smallbank/default.env`
2. 可选调用 `generate_smallbank_dataset.py`
3. 读取：
   - `init_accounts.jsonl`
   - `workload.jsonl`
4. 创建 `run_id`
5. 调用 `scripts/common/run_benchmark.py`
6. 调用 `scripts/common/metrics.sh` 汇总指标
7. 写出结果文件

### 7.5 Smallbank load 阶段

load 阶段会逐条读取 `init_accounts.jsonl`，对每个账户执行：

- 先查询链上是否已存在该账户
- 若已存在，则拒绝覆盖并直接停止
- 若不存在，则调用 `CreateAccount`
- 等待 receipt
- 回读 `Balance` 验证 checking/savings 是否与初始值一致

这意味着它不是“盲目导入”，而是带有导入前检查和导入后验证。

### 7.6 Smallbank workload 阶段

正式 workload 主要可能包含：

- `Balance`
- `DepositChecking`
- `TransactSavings`
- `Amalgamate`
- `WriteCheck`
- `SendPayment`

其中：

- `Balance` 是查询型操作
- 其余通常作为写操作处理
- 写操作需要等待 receipt 并在之后回读状态做验证

## 8. 第六步：YCSB 的数据集生成与执行

### 8.1 一条命令跑通

```bash
bash scripts/run/ycsb_load_and_run.sh --generate
```

### 8.2 YCSB 数据集生成器的输入

生成器文件：

- `datasets/generators/generate_ycsb_dataset.py`

默认模板：

- `datasets/templates/ycsb.default.json`

可控制的核心参数：

- `key_prefix`
- `key_start`
- `record_count`
- `operation_count`
- `seed`
- `distribution`
- `zipf_theta`
- `read_ratio`
- `update_ratio`
- `insert_ratio`
- `scan_ratio`
- `scan_length_min`
- `scan_length_max`
- `value_size`

### 8.3 YCSB 分布控制是什么意思

`distribution` 目前支持两种模式：

- `uniform`
- `zipfian`

区别是：

- `uniform`：现有 key 被等概率访问
- `zipfian`：一部分 key 会成为热点，访问更集中

`zipf_theta` 越高，热点倾向通常越明显。

### 8.4 YCSB 操作比例如何控制

通过下面 4 个比例参数控制：

- `read_ratio`
- `update_ratio`
- `insert_ratio`
- `scan_ratio`

这 4 个比例必须加起来等于 `1.0`。

例如，如果想做读多写少的场景，可以把 `read_ratio` 调高；如果想模拟更明显的更新压力，可以提高 `update_ratio`。

### 8.5 YCSB run 脚本做了什么

`scripts/run/ycsb_load_and_run.sh` 的流程与 Smallbank 基本一致：

1. 加载默认配置
2. 可选生成数据集
3. 校验数据集文件是否存在
4. 创建 `run_id`
5. 调用 `run_benchmark.py`
6. 读取 `load_metrics.json`
7. 调用 `metrics.sh` 输出汇总

### 8.6 YCSB load 阶段

会逐条读取 `init_records.jsonl`，对每条记录执行：

- 查询链上是否已存在该 key
- 若存在，则拒绝覆盖
- 若不存在，则调用 `Insert`
- 等待 receipt
- 回读 `Read` 验证 value 是否正确

### 8.7 YCSB workload 阶段

正式 workload 可能包含：

- `Read`
- `Update`
- `Insert`
- `Scan`

其中：

- `Read`、`Scan` 是查询型操作
- `Update`、`Insert` 是写操作
- 对写操作，依然会走 receipt + 回读校验

## 9. 第七步：`run_benchmark.py` 的核心机制

这个文件是整个实验流程中最关键的执行器。

路径：

- `scripts/common/run_benchmark.py`

它负责：

- 与 `cli` 容器内的 `peer chaincode invoke/query` 通信
- 控制并发线程数 `parallelism`
- 按批次分配 workload `batch_size`
- 轮询 `GetReceipt`
- 区分 `success`、`business_failure`、`system_failure`
- 对 Smallbank / YCSB 分别做后置语义校验
- 写出 `execution.csv`
- 写出 `load_metrics.json`

### 9.1 并发模型

`parallelism` 表示工作线程数。

`batch_size` 表示每个工作线程一次顺序处理多少条 workload 记录后，再交回线程池调度。

它不是 Fabric 的链上批处理概念，只是客户端侧的任务分块参数。

### 9.2 receipt 机制

写操作会额外带一个 `request_id`。

链码在写成功后，会把一条 receipt 记录写到链上。客户端随后不断调用 `GetReceipt` 轮询，直到：

- 找到 receipt
- 或超过超时时间

这样做的原因是：

- 能把“提交命令发出去了”和“链上语义确实已生效”分开
- 能尽量减少只靠 CLI 输出文本判断成功的误差

### 9.3 为什么还要做回读校验

因为找到 receipt 只说明链码记录了这次写操作，不代表最终状态一定符合预期。

所以执行器还会：

- Smallbank：回读 `Balance`
- YCSB：回读 `Read`
- YCSB `Scan`：检查返回顺序、长度和 key 范围

只有这些检查都通过，才会把这条操作记为 `success`。

## 10. 第八步：结果文件如何理解

### 10.1 `execution.csv`

这是逐条操作明细，记录：

- `seq`
- `op_type`
- `target_key_or_account`
- `start_ts`
- `end_ts`
- `latency_ms`
- `cli_rc`
- `status`
- `message`

其中 `status` 主要有：

- `success`
- `business_failure`
- `system_failure`

### 10.2 `summary.json`

这是机器可读的聚合结果，通常最适合后续写脚本批量分析。

### 10.3 `summary.md`

这是给人直接阅读的结果摘要。

### 10.4 `raw.log`

保存原始调用输出，便于排查问题，例如：

- 某个 invoke 的实际 CLI 返回内容
- receipt 查询输出
- query 的原始响应

### 10.5 `run_manifest.json`

记录这次实验运行的元信息，例如：

- benchmark 名称
- dataset 路径
- channel 名称
- chaincode 名称
- run_id
- parallelism
- batch_size
- load_elapsed_ms

### 10.6 `load_metrics.json`

记录 load 阶段自身的时间和操作数，供后续汇总 `load_tps` 使用。

## 11. 第九步：吞吐、成功率、时间是怎么算的

结果汇总由：

- `scripts/common/metrics.sh`

负责。

它会读取 `execution.csv`，计算：

- 尝试次数
- 成功次数
- 失败次数
- 业务失败数
- 系统失败数
- 成功率
- 平均延迟
- P50 延迟
- P95 延迟
- 正式 workload 的 `tps`
- load 阶段的 `load_tps`

时间来源是客户端脚本在每条操作开始和结束时记录的毫秒时间戳。

因此这里统计的是“客户端视角的端到端时间”，而不是 peer 内部某个单独阶段的时间。

## 12. 第十步：如何做重复实验

这是实验里非常重要的一点。

load 阶段默认拒绝覆盖已有账户或已有 key，所以重复实验时有两种可行办法：

### 方案 A：重启全新网络

适合：

- 想在完全干净的账本上做对照实验
- 不想考虑历史状态影响

### 方案 B：换一个新的账户/key 范围

适合：

- 不想每次都重启网络
- 想快速连续跑多组参数

对应关键参数：

- Smallbank：`--account-start-id`
- YCSB：`--key-start`

例如：

- 第一次 Smallbank 用 `--account-start-id 1`
- 第二次用 `--account-start-id 100001`

这样可以避免导入阶段检测到“账户已存在”。

## 13. 第十一步：建议的实验调整入口

如果你后面要调整实验，通常优先改下面这些地方。

### 13.1 改数据规模

- Smallbank：`account_count`、`operation_count`
- YCSB：`record_count`、`operation_count`

### 13.2 改热点和冲突

- Smallbank：`conflict_rate`、`hot_account_ratio`
- YCSB：`distribution`、`zipf_theta`

### 13.3 改读写比例

- Smallbank：模板中的各操作 ratio
- YCSB：`read_ratio`、`update_ratio`、`insert_ratio`、`scan_ratio`

### 13.4 改客户端施压强度

- `parallelism`
- `batch_size`

### 13.5 改链码名、channel 名、版本

- `configs/smallbank/default.env`
- `configs/ycsb/default.env`

或者在脚本命令行中直接覆盖。

## 14. 第十二步：如果新窗口继续工作，先读哪些文件

建议顺序：

1. `docs/architecture-overview-zh.md`
2. `docs/workflow-details-zh.md`
3. `docs/linux-run-checklist.md`
4. `docs/benchmark-assets.md`
5. `configs/smallbank/default.env`
6. `configs/ycsb/default.env`
7. `datasets/templates/smallbank.default.json`
8. `datasets/templates/ycsb.default.json`

如果要继续改代码，再继续看：

- `scripts/common/run_benchmark.py`
- `scripts/common/lib.sh`
- `chaincode/smallbank/smallbank.go`
- `chaincode/ycsb/ycsb.go`

## 15. 一句话落地建议

真正做实验时，不要直接改运行脚本本体，优先通过模板参数、命令行参数和配置文件来调整 workload；只有在你需要新增统计口径、链码行为或语义校验逻辑时，再去改公共脚本和链码实现。
