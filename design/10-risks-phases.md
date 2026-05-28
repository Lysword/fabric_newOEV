# 10 — 风险、规避方案与分阶段落地计划

> 上级目录：[00-index.md](00-index.md) | 前置：[09-ycsb.md](09-ycsb.md)

---

## 10.1 链上数据与兼容性

### Intent/Plan 存放位置

| 方案 | 存放位置 | 优点 | 缺点 | 结论 |
|------|---------|------|------|------|
| A | `ChaincodeProposalPayload.Input.Args`（现有） | 无需修改任何格式 | orderer/peer 需解析 | **推荐（Phase 1）** |
| B | `ChaincodeAction.response.payload`（合约返回值） | 合约自带，签名保护 | 需修改合约，破坏现有行为 | Phase 2 可选 |
| C | 新增 proto 字段 | 结构清晰 | 需重新生成 pb，兼容性风险 | 暂不采用 |
| D | `BlockMetadata[ORDERER]`（BatchSchedule 已在此）| 现有槽位 | 不覆盖 block 签名 | **BatchSchedule 使用** |

**最终方案**：
- **Intent 信息**：不显式存储，在 orderer 和 peer commit 阶段直接从 `ChaincodeProposalPayload.Input.Args` 解析
- **BatchSchedule**：存入 `block.Metadata.Metadata[ORDERER]`，通过 `WriteBlock()` 第三个参数传入

### 对区块哈希和签名的影响

| 变更 | 影响 |
|------|------|
| BatchSchedule 写入 `BlockMetadata[ORDERER]` | `BlockMetadata` 不在 `data_hash` 中，不影响块哈希链 |
| orderer 签名覆盖范围 | 签名覆盖 `block.Header.Bytes()`，不包含 metadata，BatchSchedule 不受签名保护 |
| ESCC 签名范围 | 签名覆盖 `header + payload + ccid + response + simResult + events`，不涉及 BatchSchedule |
| 背书验证（VSCC） | 沿用原有逻辑，不受影响 |

---

## 10.2 风险点和规避方案

| 风险 | 描述 | 规避方案 |
|------|------|---------|
| **系统链码被误处理** | cscc/lscc 等被误判为 benchmark | `IsBenchmarkChaincode()` 精确匹配名称；`IsSysCC()` 检查优先 |
| **MVCC 与新排序冲突** | 普通 tx 仍走 MVCC，可能与 benchmark tx 产生隐式依赖 | benchmark tx 走重放路径，普通 tx 仍走 MVCC；两类 tx 互不影响更新时序 |
| **提交并发写 stateDB 不安全** | Phase 3 同 batch 并行时多线程写同一 key | 同 batch 内无冲突（图染色保证），但需在 ReplayEngine 维护 batch 内临时写缓存 |
| **BlockMetadata 不一致** | peer 无法解析 BatchSchedule → panic | `ParseBatchSchedule` 返回 false 时降级到原有 MVCC 路径，日志 warning |
| **orderer 解析 tx payload 失败** | 格式错误或非 benchmark tx | tx 无法解析时跳过（视为 non-benchmark），不影响其他 tx 的冲突分析 |
| **背书阶段 simResult 与重放结果不一致** | 背书时 chaincode 读到 version V，提交时状态已变 | 这正是新架构要解决的问题：**跳过 MVCC，用重放结果覆盖旧 rwset 写值** |
| **Scan 幻读问题（YCSB）** | Scan 读取的 key 范围在排序后发生插入 | Scan 与同范围内 Insert/Update 在图中连边，强制不同 batch → 串行执行 |
| **背书后 tx args 被篡改** | 中间人修改 args → 重放结果不符预期 | args 在 ChaincodeProposalPayload 中，被 endorser 签名保护；peer 提交前 VSCC 验证签名 |
| **图染色 NP-Hard，orderer 超时** | batch 过大时贪心染色耗时 | 贪心算法 O(V+E) 线性，对正常 batch 大小（100-1000 tx）毫秒级完成；设置超时降级 |
| **Proto 字段未向后兼容** | 旧 peer 不认识新 BatchSchedule 格式 | BatchSchedule 写在 metadata[ORDERER]，旧 peer 只是忽略此字段，不会 crash |

---

## 10.3 分阶段落地计划

### Phase 1：最小闭环（估计 1-2 周）

**目标**：smallbank benchmark 可跑通，orderer 产生 BatchSchedule，peer 按 batch 串行重放

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P1-1 | 新建 `core/bench/types.go` | 定义 TxIntent, BatchSchedule, BatchEntry, KeyInterval, HypercubeSet, ReplayOp |
| P1-2 | 新建 `orderer/bench/extractor.go` | IsBenchmarkChaincode, extractIntentsFromBlock, extractIntentFromEnvBytes |
| P1-3 | 新建 `orderer/bench/extractor_smallbank.go` | ExtractSmallBankIntent（仅 Amalgamate/SendPayment/DepositChecking/TransactSavings/WriteCheck）|
| P1-4 | 新建 `orderer/bench/graph.go` | ConflictGraph + greedyGraphColoring + buildPointHypercube |
| P1-5 | 新建 `orderer/bench/analyzer.go` | AnalyzeBatchAndSchedule（调用 extractor + graph）|
| P1-6 | 修改 `orderer/solo/consensus.go` | main() 中插入 AnalyzeBatchAndSchedule，传给 WriteBlock |
| P1-7 | 新建 `core/bench/replay_smallbank.go` | replaySmallBank（各操作重放逻辑）|
| P1-8 | 新建 `core/bench/commit.go` | commitWithBatchSchedule（串行 batch，重放 benchmark tx）|
| P1-9 | 修改 `core/ledger/kvledger/kv_ledger.go` | Commit() 检测 BatchSchedule，调用 commitWithBatchSchedule |

**验收条件**：
- `smallbank_load_and_run.sh` 正常运行，无 MVCC 失败
- BlockMetadata[ORDERER] 中可解析出 BatchSchedule
- 有冲突的 tx 被分配到不同 batch

---

### Phase 2：增强 YCSB（估计 1 周）

**目标**：支持 ycsb read/update/insert；支持 scan 的 key 范围冲突检测

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P2-1 | 新建 `orderer/bench/extractor_ycsb.go` | ExtractYCSBIntent（Read/Update/Insert/Scan）|
| P2-2 | 修改 `orderer/bench/graph.go` | `intersects()` 支持范围区间与精确点混合相交判断 |
| P2-3 | 新建 `core/bench/replay_ycsb.go` | replayYCSB（Insert/Update 直接写值，Read/Scan 无写）|
| P2-4 | 修改 `orderer/bench/extractor.go` | 调用 ExtractYCSBIntent |

---

### Phase 3：同 batch 并行提交（估计 1-2 周）

**目标**：同 batch 内多线程处理，提升并发吞吐量

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P3-1 | 修改 `core/bench/commit.go` | batch 内用 goroutine 并行重放；增加 intra-batch write buffer 防止同 batch 内读写竞争 |
| P3-2 | 新增性能统计 | 记录各 batch 大小、重放时间、batch 数量等指标 |

---

### Phase 4：完善兼容性和稳定性（估计 1 周）

**目标**：配置开关、完善错误处理、降级路径、文档化

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P4-1 | `sampleconfig/core.yaml` / viper | 添加 `bench.intent.enabled` 开关 |
| P4-2 | `core/bench/commit.go` | 解析失败时完整降级到原 MVCC 路径 |
| P4-3 | 测试 | 验证系统链码（lscc deploy, cscc join）在新架构下仍正常工作 |
| P4-4 | 测试 | 验证混合 block（benchmark tx + 普通 tx）正确处理 |

---

## 10.4 关键函数和文件修改清单（完整）

### 需要修改的现有文件

| 文件 | 修改函数 | 修改描述 |
|------|---------|---------|
| `orderer/solo/consensus.go` | `chain.main()` | 在 CreateNextBlock 后插入 AnalyzeBatchAndSchedule 调用 |
| `core/ledger/kvledger/kv_ledger.go` | `kvLedger.Commit()` | 检测 BatchSchedule，分流到新路径 |

### 需要新建的文件

| 新建文件 | 包 | 内容 |
|---------|-----|------|
| `core/bench/types.go` | `bench` | TxIntent, BatchSchedule, BatchEntry, KeyInterval, HypercubeSet, ReplayOp |
| `core/bench/commit.go` | `bench` | commitWithBatchSchedule, ReplayEngine, findTxInBlock, extractInvocationFromEnvBytes |
| `core/bench/replay_smallbank.go` | `bench` | SmallBank 各操作的重放实现 |
| `core/bench/replay_ycsb.go` | `bench` | YCSB 各操作的重放实现（Phase 2）|
| `core/bench/statedb_interface.go` | `bench` | StateDBInterface 接口定义 |
| `orderer/bench/extractor.go` | `bench` | IsBenchmarkChaincode(), extractIntentsFromBlock, 通用 Envelope 解析 |
| `orderer/bench/extractor_smallbank.go` | `bench` | ExtractSmallBankIntent |
| `orderer/bench/extractor_ycsb.go` | `bench` | ExtractYCSBIntent（Phase 2）|
| `orderer/bench/graph.go` | `bench` | ConflictGraph, buildConflictGraph, greedyGraphColoring, intersects |
| `orderer/bench/analyzer.go` | `bench` | AnalyzeBatchAndSchedule, buildBatchSchedule |

---

*返回：[目录](00-index.md) | 前一篇：[09-ycsb.md](09-ycsb.md) | 下一篇：[11-open-questions.md](11-open-questions.md)*
