# Linux 系统运行与测试说明

## 1. 文档用途

本文档面向实际在 Linux 环境中运行 `Hyperledger Fabric v1.0` 基准测试的场景，目标是用中文说明：

- 需要准备哪些文件
- 需要满足哪些前置条件
- 如何在 Linux 上启动网络并部署链码
- 如何分别运行 `smallbank` 和 `ycsb`
- 如何查看结果文件
- 如何做重复实验

本文档只整理 `examples/e2e_cli/benchmark_suite` 目录下现成文档中的信息，不再依赖代码阅读。

## 2. 需要拷贝到 Linux 的文件

至少需要把下面两部分内容拷贝到 Linux 实验机上的 Fabric 仓库对应位置：

- `examples/e2e_cli/docker-compose-cli.yaml`
- `examples/e2e_cli/benchmark_suite/`

推荐保持原有目录层级不变，放置为：

```text
<fabric-repo>/
  examples/
    e2e_cli/
      docker-compose-cli.yaml
      benchmark_suite/
```

这样可以保证 `cli` 容器中的挂载路径仍然正确。

## 3. Linux 端前置条件

在 Linux 系统中，需要确保下列工具可用：

- `bash`
- `python3`
- `docker`
- `docker compose` 或 `docker-compose`

此外，Linux 机器上的 Fabric 仓库应当已经具备可用的 `examples/e2e_cli` 基础环境，至少要包含：

- `generateArtifacts.sh`
- `base/docker-compose-base.yaml`
- 原有的证书、channel 和 docker 相关支持文件

## 4. 进入工作目录

在 Linux 上进入：

```bash
cd examples/e2e_cli/benchmark_suite
```

建议先给脚本增加执行权限：

```bash
chmod +x scripts/deploy/*.sh
chmod +x scripts/run/*.sh
chmod +x scripts/common/*.sh
```

如果后面需要关闭网络，也是在这个目录下执行关闭脚本。

## 5. 目录结构如何理解

`benchmark_suite` 主要包含以下几类内容：

- `chaincode/`
  - 两个基准的链码实现
- `scripts/deploy/`
  - 启动网络与部署链码
- `scripts/run/`
  - 生成数据集、导入数据、执行 workload
- `scripts/common/`
  - 公共脚本与结果汇总逻辑
- `datasets/templates/`
  - 默认数据集参数模板
- `datasets/generators/`
  - 数据集生成脚本
- `datasets/generated/`
  - 生成出来的数据集
- `configs/`
  - 默认运行参数
- `results/`
  - 实验结果输出目录
- `docs/`
  - 使用说明和中文文档

## 6. 运行 Smallbank

### 6.1 启动网络并部署链码

在 `examples/e2e_cli/benchmark_suite` 目录下执行：

```bash
bash scripts/deploy/smallbank_up_and_deploy.sh
```

这个脚本会负责：

- 启动 `e2e_cli` 网络
- 安装 `smallbank` 链码
- 实例化链码
- 做一次健康检查

### 6.2 生成数据集并执行测试

最简单的命令：

```bash
bash scripts/run/smallbank_load_and_run.sh --generate
```

这条命令会：

1. 生成 Smallbank 数据集
2. 导入初始账户数据
3. 执行正式 workload
4. 输出结果文件

### 6.3 自定义 Smallbank 参数

示例：

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

主要参数含义：

- `--dataset-name`
  - 数据集名称
- `--account-start-id`
  - 账户编号起始值
- `--account-count`
  - 初始账户数
- `--operation-count`
  - workload 操作数
- `--conflict-rate`
  - 冲突倾向
- `--hot-account-ratio`
  - 热点账户比例
- `--max-debit-fraction`
  - 单次扣款上限比例
- `--parallelism`
  - 客户端并发线程数
- `--batch-size`
  - 客户端任务分块大小

### 6.4 复用已有 Smallbank 数据集

如果数据集已经生成过，可以直接指定数据集目录：

```bash
bash scripts/run/smallbank_load_and_run.sh --dataset-path datasets/generated/smallbank/<dataset_name>
```

### 6.5 关闭当前网络

如果你已经完成 Smallbank 实验，或者想重新从干净网络开始，可以执行：

```bash
bash scripts/deploy/network_down.sh
```

如果还希望一并删除 compose 管理的 Docker 卷，可以执行：

```bash
bash scripts/deploy/network_down.sh --remove-volumes
```

## 7. 运行 YCSB

### 7.1 启动网络并部署链码

```bash
bash scripts/deploy/ycsb_up_and_deploy.sh
```

### 7.2 生成数据集并执行测试

```bash
bash scripts/run/ycsb_load_and_run.sh --generate
```

### 7.3 Uniform 分布示例

```bash
bash scripts/run/ycsb_load_and_run.sh \
  --generate \
  --dataset-name ycsb-uniform-exp \
  --key-start 1 \
  --record-count 1000 \
  --operation-count 10000 \
  --distribution uniform \
  --read-ratio 0.5 \
  --update-ratio 0.3 \
  --insert-ratio 0.1 \
  --scan-ratio 0.1 \
  --parallelism 4 \
  --batch-size 10
```

### 7.4 Zipfian 分布示例

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

### 7.5 主要 YCSB 参数

- `--dataset-name`
  - 数据集名称
- `--key-start`
  - key 起始编号
- `--record-count`
  - 初始记录数
- `--operation-count`
  - workload 操作数
- `--distribution`
  - `uniform` 或 `zipfian`
- `--zipf-theta`
  - Zipfian 热点强度参数
- `--read-ratio`
  - 读比例
- `--update-ratio`
  - 更新比例
- `--insert-ratio`
  - 插入比例
- `--scan-ratio`
  - 扫描比例
- `--parallelism`
  - 客户端并发线程数
- `--batch-size`
  - 客户端任务分块大小

### 7.6 复用已有 YCSB 数据集

```bash
bash scripts/run/ycsb_load_and_run.sh --dataset-path datasets/generated/ycsb/<dataset_name>
```

### 7.7 YCSB 实验后关闭网络

```bash
bash scripts/deploy/network_down.sh
```

## 8. 结果文件在哪里看

### 8.1 Smallbank 结果目录

```text
examples/e2e_cli/benchmark_suite/results/smallbank/<run_id>/
```

### 8.2 YCSB 结果目录

```text
examples/e2e_cli/benchmark_suite/results/ycsb/<run_id>/
```

### 8.3 每次运行的关键结果文件

- `run_manifest.json`
- `execution.csv`
- `summary.json`
- `summary.md`
- `raw.log`
- `load_metrics.json`

## 9. 如何理解这些结果

### 9.1 `execution.csv`

记录每一条操作的执行结果、状态和延迟。

### 9.2 `summary.json`

适合后续脚本读取和批量分析。

### 9.3 `summary.md`

适合人工直接查看实验摘要。

### 9.4 `raw.log`

适合排查问题，保存原始命令输出。

### 9.5 `load_metrics.json`

记录 load 阶段的耗时和导入操作数。

## 10. 重点指标怎么看

测试重点关注：

- `successful_ops`
- `failed_ops`
- `business_failures`
- `system_failures`
- `success_rate`
- `tps`
- `avg_latency_ms`
- `p50_latency_ms`
- `p95_latency_ms`
- `load_tps`

这里要注意：

- `tps` 是正式 workload 阶段的语义成功 TPS
- `load_tps` 是初始数据导入阶段的吞吐

## 11. 为什么这里的成功率和 TPS 更严格

现有文档说明，这套流程不是只根据客户端退出码判断成功，而是要求：

1. invoke/query 正常返回
2. 写操作被观察到 receipt
3. 回读链上状态后语义校验通过

只有这样，操作才会被统计为 `success`。

因此这套结果更适合做论文或实验分析，因为它更接近“真正成功生效的吞吐”。

## 12. 如何做重复实验

load 阶段默认拒绝覆盖已有状态，因此重复实验不能简单使用同一批初始数据再次导入。

有两种常见做法：

- 重启全新网络，再跑一次
- 使用新的账户范围或 key 范围

主要控制参数：

- Smallbank：`--account-start-id`
- YCSB：`--key-start`

例如：

- Smallbank 第一次用 `--account-start-id 1`
- 第二次改成 `--account-start-id 100001`

或者：

- YCSB 第一次用 `--key-start 1`
- 第二次改成 `--key-start 200000`

如果你想从一套全新的网络重新开始，也可以先执行：

```bash
bash scripts/deploy/network_down.sh
```

然后重新执行部署脚本。

## 13. 启动超时如何处理

如果在启动网络时看到类似下面的报错：

```text
An HTTP request took too long to complete.
Read timed out. (read timeout=60)
```

通常说明 Docker Compose 客户端等待 Docker 守护进程响应超时，而不是链码本身有问题。

当前脚本已经默认提高了 compose 客户端超时时间，用于兼容较慢的 Linux 环境。
另外，部署脚本中的链码健康检查现在也会自动重试，并且会先检查实例化所在的 Org2 peer0，再检查后续实际使用的 Org1 peer0，而不是在实例化后只查一次。

如果你的机器仍然偏慢，可以在运行部署脚本前手工设置：

```bash
export COMPOSE_HTTP_TIMEOUT=300
export DOCKER_CLIENT_TIMEOUT=300
```

然后重新执行：

```bash
bash scripts/deploy/network_down.sh
bash scripts/deploy/smallbank_up_and_deploy.sh
```

或：

```bash
bash scripts/deploy/network_down.sh
bash scripts/deploy/ycsb_up_and_deploy.sh
```

## 14. 最推荐的运行顺序

如果你第一次在 Linux 上做实验，建议按下面顺序执行：

### Smallbank

```bash
cd examples/e2e_cli/benchmark_suite
chmod +x scripts/deploy/*.sh scripts/run/*.sh scripts/common/*.sh
bash scripts/deploy/smallbank_up_and_deploy.sh
bash scripts/run/smallbank_load_and_run.sh --generate
bash scripts/deploy/network_down.sh
```

### YCSB

```bash
cd examples/e2e_cli/benchmark_suite
chmod +x scripts/deploy/*.sh scripts/run/*.sh scripts/common/*.sh
bash scripts/deploy/ycsb_up_and_deploy.sh
bash scripts/run/ycsb_load_and_run.sh --generate
bash scripts/deploy/network_down.sh
```

## 15. 建议配合阅读的文档

如果后续还要继续实验或写论文，建议再配合阅读：

- `docs/architecture-overview-zh.md`
- `docs/workflow-details-zh.md`
- `docs/experiment-record-template-zh.md`
- `docs/benchmark-assets.md`

## 16. 一句话总结

在 Linux 系统中，这套流程的核心就是：

先把 `benchmark_suite` 放回 `examples/e2e_cli/` 原有结构里，再通过 `deploy` 脚本启动网络和部署链码，最后用 `run` 脚本生成数据、导入初始状态、执行 workload，并从 `results/` 目录中读取吞吐、成功率和延迟等结果。
