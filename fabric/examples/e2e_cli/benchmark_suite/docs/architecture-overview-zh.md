# Fabric 基准测试套件总体架构说明

## 1. 文档目的

本文档从总体层面说明当前 `benchmark_suite` 的设计目标、目录结构、组件职责、执行流程和结果产出方式。

适合以下场景：

- 你自己复盘整个测试方案
- 在新窗口中快速恢复上下文
- 向他人解释这套 `smallbank` / `ycsb` 基准测试是如何组织的
- 在后续实验中继续扩展参数、链码或统计逻辑

## 2. 方案定位

这套基准测试代码面向 `Hyperledger Fabric v1.0` 的 `examples/e2e_cli` 环境，目标是提供两套相互独立、但组织方式一致的实验流程：

- `Smallbank` 基准测试
- `YCSB` 基准测试

两者共享同一套部署框架、结果记录逻辑和数据集工作流，但：

- 链码相互独立
- 配置相互独立
- 数据集相互独立
- 执行脚本相互独立
- 结果目录相互独立

这意味着你可以只运行其中一个基准，也可以分别部署并分别做实验，不要求把两套 workload 混在一起。

## 3. 总体设计目标

本方案的设计目标不是只把交易“发出去”，而是尽量让实验结果具备论文实验所需的可解释性。

核心目标包括：

- 能自动启动网络并部署链码
- 能自动生成可调参数的数据集
- 能自动导入初始状态并执行 workload
- 能把吞吐、成功数、失败数、延迟等指标持久化到本地文件
- 能区分“业务失败”和“系统失败”
- 能利用 Fabric 的链上语义信息，而不是只依赖客户端返回码

## 4. 总体架构图

可以把整个测试流程理解为下面 6 层：

```text
Linux/Fabric v1.0 实验环境
        |
        v
部署层 scripts/deploy/*.sh
        |
        v
链码层 chaincode/smallbank + chaincode/ycsb
        |
        v
数据层 datasets/templates + datasets/generators + datasets/generated
        |
        v
执行层 scripts/run/*.sh + scripts/common/run_benchmark.py
        |
        v
结果层 results/<benchmark>/<run_id>/*
```

更细一点看，运行时的组件关系如下：

```text
用户
  -> deploy 脚本
     -> e2e_cli 网络
     -> peer chaincode install / instantiate

用户
  -> run 脚本
     -> 数据集生成器
     -> run_benchmark.py
        -> docker exec cli -> peer chaincode invoke/query
        -> 基准链码
        -> receipt 轮询
        -> 状态回读校验
        -> execution.csv / summary.json / summary.md / raw.log
```

## 5. 目录级架构

当前统一根目录是：

```text
examples/e2e_cli/benchmark_suite/
```

其下按功能分层：

- `chaincode/`
  - 存放 `smallbank` 和 `ycsb` 的链码实现与单测
- `scripts/deploy/`
  - 负责启动网络并部署链码
- `scripts/run/`
  - 负责生成数据集、导入数据并执行 workload
- `scripts/common/`
  - 公共工具逻辑，包括 Docker/Fabric 调用、结果汇总、并发执行器
- `datasets/templates/`
  - 数据集默认参数模板
- `datasets/generators/`
  - 数据集生成脚本
- `datasets/generated/`
  - 实际生成出的数据集
- `configs/`
  - 每个基准的默认运行配置
- `results/`
  - 每次实验的持久化输出
- `docs/`
  - 使用说明、资产清单、Linux 执行清单，以及本文档

## 6. 运行流程全景

一次完整实验通常分成 5 个阶段。

### 阶段 1：准备环境

依赖 `examples/e2e_cli` 原有的 Fabric v1.0 环境，并通过修改后的 `docker-compose-cli.yaml` 把 `benchmark_suite` 挂载进 `cli` 容器。

### 阶段 2：启动网络并部署链码

使用：

- `scripts/deploy/smallbank_up_and_deploy.sh`
- `scripts/deploy/ycsb_up_and_deploy.sh`

职责包括：

- 检查 Docker 和 compose
- 启动 `e2e_cli` 网络
- 安装链码到 Org1/Org2 peer
- 实例化链码
- 做一次健康检查 `Ping`

### 阶段 3：生成数据集

使用：

- `datasets/generators/generate_smallbank_dataset.py`
- `datasets/generators/generate_ycsb_dataset.py`

Smallbank 支持调节：

- 账户起始编号
- 账户数
- 操作数
- 冲突率
- 热点账户比例
- 最大扣款比例
- 操作类型比例

YCSB 支持调节：

- key 起始编号
- 初始记录数
- 操作数
- `uniform` / `zipfian` 分布
- `zipf_theta`
- `read/update/insert/scan` 比例
- scan 长度范围
- value 大小

### 阶段 4：导入初始状态并执行 workload

使用：

- `scripts/run/smallbank_load_and_run.sh`
- `scripts/run/ycsb_load_and_run.sh`

执行流程是：

1. 可选地先生成数据集
2. 读取 `init_*.jsonl`
3. 将初始账户或记录写入链上
4. 读取 `workload.jsonl`
5. 并发执行 workload
6. 轮询 receipt
7. 回读链上状态做语义校验
8. 生成本地结果文件

### 阶段 5：汇总结果

结果落在：

- `results/smallbank/<run_id>/`
- `results/ycsb/<run_id>/`

关键文件包括：

- `run_manifest.json`
- `execution.csv`
- `summary.json`
- `summary.md`
- `raw.log`
- `load_metrics.json`

## 7. 为什么这里强调“语义成功”

这套方案没有把“客户端命令退出码为 0”直接等同于“业务成功”。

对写操作而言，成功判定是三段式的：

1. Fabric invoke/query 传输成功
2. 链上 receipt 被观察到
3. 回读链上状态后，结果符合预期

因此这里的 `TPS` 更接近“语义成功 TPS”，而不是“提交尝试 TPS”。

这对论文实验很重要，因为：

- 可以减少把异常提交误算为成功吞吐的风险
- 可以更清楚地区分业务失败与系统失败
- 可以用链上状态验证 benchmark 结果是否真的生效

## 8. Smallbank 与 YCSB 的职责边界

### Smallbank

目标是模拟银行类账户事务。

主要 workload 操作包括：

- `Balance`
- `DepositChecking`
- `TransactSavings`
- `Amalgamate`
- `WriteCheck`
- `SendPayment`

其中数据集生成器维护影子状态，尽量避免实验过程中出现“余额不足”等非预期业务失败。

### YCSB

目标是模拟通用 KV 读写扫描负载。

主要 workload 操作包括：

- `Read`
- `Update`
- `Insert`
- `Scan`

其中：

- `distribution=uniform` 表示等概率选 key
- `distribution=zipfian` 表示存在热点 key

这满足大多数论文里对 YCSB 负载模型的基本要求。

## 9. 数据流

从数据角度看，一次实验的数据流可以抽象为：

```text
模板参数
  -> 生成器脚本
  -> manifest.json + init_*.jsonl + workload.jsonl
  -> run 脚本
  -> run_benchmark.py
  -> 链上写入 / 查询 / receipt 轮询 / 状态校验
  -> execution.csv + summary.json + summary.md + raw.log
```

其中：

- `manifest.json` 记录数据集参数和操作分布
- `init_*.jsonl` 描述初始装载数据
- `workload.jsonl` 描述正式 workload
- `execution.csv` 记录每条操作的逐条结果
- `summary.json` / `summary.md` 记录聚合指标

## 10. 指标体系

这套测试流程重点输出以下指标：

- `attempted_ops`
- `successful_ops`
- `failed_ops`
- `business_failures`
- `system_failures`
- `success_rate`
- `elapsed_seconds`
- `tps`
- `avg_latency_ms`
- `p50_latency_ms`
- `p95_latency_ms`
- `load_elapsed_seconds`
- `load_tps`

这里要特别注意：

- `tps` 只针对正式 workload 阶段
- `load_tps` 针对初始数据导入阶段
- 时间是在客户端脚本中按毫秒记录的

## 11. 重复实验策略

因为 load 阶段拒绝覆盖已有业务状态，所以重复实验不能简单地在同一账本上反复导入同一批数据。

可行策略只有两类：

- 使用全新网络重新实验
- 使用不重叠的账户区间或 key 区间

对应主要控制参数：

- Smallbank：`--account-start-id`
- YCSB：`--key-start`

## 12. 新窗口恢复上下文建议

如果你后续在新窗口中继续工作，建议优先阅读以下文档：

1. `docs/architecture-overview-zh.md`
2. `docs/workflow-details-zh.md`
3. `docs/linux-run-checklist.md`
4. `docs/benchmark-assets.md`

如果要继续实验，建议同时查看：

- `configs/smallbank/default.env`
- `configs/ycsb/default.env`
- `datasets/templates/smallbank.default.json`
- `datasets/templates/ycsb.default.json`

## 13. 一句话总结

这套 `benchmark_suite` 的本质是：

在 Fabric v1.0 的 `e2e_cli` 环境中，用统一目录和统一脚本组织两套独立基准测试，并通过“数据集生成 + 自动部署 + receipt 轮询 + 语义校验 + 本地结果持久化”的方式，把实验流程固定下来。
