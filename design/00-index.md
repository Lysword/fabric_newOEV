# KV 模型架构改造方案 — 目录索引

> **项目**：Hyperledger Fabric v1.0 | KV 模型 | Smallbank / YCSB  
> **目标**：背书阶段提取读写意图 → 排序阶段超立方体冲突检测 → 提交阶段批次重放  
> **约束**：只改 KV 模型；系统链码不受影响；快速落地优先

---

## 文档导航

| 文件 | 内容摘要 | 优先阅读 |
|------|---------|---------|
| [01-current-arch.md](01-current-arch.md) | 现有 EOV 架构、关键函数路径、MVCC 逻辑、Block 结构 | ★★★ 必读 |
| [02-target-arch.md](02-target-arch.md) | 新架构整体流程图（改造点 A/B/C/D）、新旧对比 | ★★★ 必读 |
| [03-compatibility.md](03-compatibility.md) | 合约分流规则、兼容矩阵、混合 Block 处理、降级开关 | ★★ 实现前读 |
| [04-data-structures.md](04-data-structures.md) | `TxIntent` / `BatchSchedule` / `ConflictGraph` Go 结构定义 | ★★★ 编码时参考 |
| [05-orderer-changes.md](05-orderer-changes.md) | orderer 修改点、intent 提取、`analyzeBatchAndSchedule` 实现 | ★★★ Phase 1 核心 |
| [06-commit-changes.md](06-commit-changes.md) | `kv_ledger.Commit()` 改造、`commitWithBatchSchedule`、重放引擎 | ★★★ Phase 1 核心 |
| [07-conflict-detection.md](07-conflict-detection.md) | 冲突规则、图构建伪代码、贪心染色伪代码、BatchSchedule 生成 | ★★ 算法参考 |
| [08-smallbank.md](08-smallbank.md) | SmallBank 各操作读写分析、intent 提取代码、重放引擎代码 | ★★★ Phase 1 实现 |
| [09-ycsb.md](09-ycsb.md) | YCSB 各操作分析、范围查询、intent 提取代码、重放引擎代码 | ★★ Phase 2 实现 |
| [10-risks-phases.md](10-risks-phases.md) | 风险与规避、Phase 1-4 落地计划、文件修改清单 | ★★ 实施前读 |
| [11-open-questions.md](11-open-questions.md) | 待确认问题、附录代码片段（Envelope 解析、BatchSchedule 读写）| ★ 确认后再读 |

---

## 快速参考：改造点汇总

```
改造点 A  [背书阶段]   ← Phase 1 暂不改动（仍执行合约）
改造点 B  [orderer]   ← orderer/solo/consensus.go:main() 插入 analyzeBatchAndSchedule
改造点 C  [orderer]   ← orderer/bench/*.go（新建）提取 intent → 冲突图 → BatchSchedule
改造点 D  [commit]    ← core/ledger/kvledger/kv_ledger.go:Commit() 按 batch 重放
```

## 快速参考：Phase 1 最小改动文件清单

| 操作 | 文件 |
|------|------|
| 修改 | `orderer/solo/consensus.go` |
| 修改 | `core/ledger/kvledger/kv_ledger.go` |
| 新建 | `core/bench/types.go` |
| 新建 | `core/bench/commit.go` |
| 新建 | `core/bench/replay_smallbank.go` |
| 新建 | `core/bench/statedb_interface.go` |
| 新建 | `orderer/bench/extractor.go` |
| 新建 | `orderer/bench/graph.go` |
| 新建 | `orderer/bench/analyzer.go` |

## 关键设计决策

1. **BatchSchedule 传输通道**：`block.Metadata.Metadata[ORDERER]`（不在 data_hash 中，安全）
2. **Intent 来源**：`ChaincodeProposalPayload.Input.Args`（不需要新增 proto 字段）
3. **写值依赖**：SmallBank 全部依赖当前状态 → 提交时重放；YCSB Insert/Update 可直接写
4. **系统链码保护**：`IsBenchmarkChaincode()` 精确名称匹配，其余走原路径
5. **MVCC 策略**：benchmark tx 跳过 MVCC；普通 tx 保留原 MVCC 路径

---

*文档版本：v1.1 | 2026-05-28 | 基于本地源码分析*
