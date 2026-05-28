# 02 — 目标架构设计

> 上级目录：[00-index.md](00-index.md) | 前置：[01-current-arch.md](01-current-arch.md)

---

## 2.1 新架构整体流程

```
Client Proposal
  │
  ▼
Peer Endorsement / Intent Extraction          [改造点 A — Phase 1 暂不改]
  ├── 检测 chaincode 名称 → smallbank/ycsb?
  ├── YES → IntentExtractor.Extract(funcName, args)
  │          └── 返回 readKeys, writeKeys, replayOp
  ├── NO  → 原有 Fabric 路径（含 MVCC 读写集生成）
  └── 返回 ProposalResponse（intent 信息通过 TxArgs 携带，无需额外字段）
  │
  ▼
Client 组装 SignedTransaction → Orderer
  │
  ▼
Orderer Receive TX                             [改造点 B — 入口修改]
  │
  ▼
Orderer Build Block (BlockCutter)
  │
  ▼
Orderer Analyze Hypercube Conflicts            [改造点 C — 新增分析模块]
  ├── 遍历 block 内所有 Envelope
  ├── 解析 ChaincodeInvocationSpec.Input.Args 获取 funcName + args
  ├── 识别 smallbank/ycsb → ExtractIntent → 得到 readKeys, writeKeys
  ├── 为每笔 tx 构建 HypercubeSet（KV 场景为 key 区间）
  ├── 构建冲突无向图（WA ∩ RB ≠ ∅ → 有边）
  └── 贪心图染色 → BatchSchedule {batch0:[tx1,tx5], batch1:[tx2,tx3], ...}
  │
  ▼
Orderer Output Colored Batches
  ├── 序列化 BatchSchedule → bytes
  └── WriteBlock(block, committers, batchScheduleBytes)
      └── block.Metadata.Metadata[ORDERER] = BatchSchedule        [数据流通道]
  │
  ▼
Peer Receive Block
  │
  ▼
Peer Validate (VSCC, 沿用原路径)               [无改动，背书策略验证]
  │
  ▼
Peer Commit with Batch Order                   [改造点 D — 提交改造]
  ├── 读取 block.Metadata.Metadata[ORDERER] → 解析 BatchSchedule
  ├── 有 BatchSchedule → 新路径（benchmark tx）
  │   ├── for each batch in order (串行):
  │   │   └── for each txID in batch (Phase 3 可并行):
  │   │       ├── 识别 tx 为 smallbank/ycsb
  │   │       ├── 从 tx 提取 funcName + args (ChaincodeProposalPayload.Input)
  │   │       ├── 执行 ReplayEngine.Execute(funcName, args, currentStateDB)
  │   │       │   └── 返回 writeSet: map[key]value
  │   │       └── 直接写入 stateDB（跳过 MVCC）
  │   └── 非 benchmark tx → 原有 MVCC 路径
  └── 无 BatchSchedule → 原有 MVCC 路径（全部 tx 走原路径）
  │
  ▼
Commit State DB → World State 更新完成
```

---

## 2.2 新旧架构对比

| 维度 | 现有 EOV | 新架构 |
|------|---------|--------|
| 背书阶段 | 真实执行合约，生成读写集 | Phase 1 不变；Phase 2+ 可改为 intent 提取 |
| Orderer 排序 | 仅排序，不分析内容 | 分析 block 内 tx 冲突，生成 BatchSchedule |
| Block 元数据 | `metadata[ORDERER]` = nil | `metadata[ORDERER]` = BatchSchedule JSON |
| MVCC 验证 | 所有 tx 走 MVCC，高冲突导致大量失败 | benchmark tx 跳过 MVCC，走重放路径 |
| 提交顺序 | 顺序处理，高冲突 tx 标记失败 | 按 batch 顺序串行，同 batch 可并行 |
| 系统链码 | 原路径 | 不变（精确名称隔离） |

---

## 2.3 改造点 B：orderer/solo/consensus.go 修改位置

**原代码**（`orderer/solo/consensus.go:98-101`）：
```go
block := ch.support.CreateNextBlock(batch)
ch.support.WriteBlock(block, committers[i], nil)
```

**改造后**：
```go
block := ch.support.CreateNextBlock(batch)
batchScheduleBytes := analyzeBatchAndSchedule(block)   // [新增]
ch.support.WriteBlock(block, committers[i], batchScheduleBytes)
```

`analyzeBatchAndSchedule` 定义见 [05-orderer-changes.md](05-orderer-changes.md)。

---

## 2.4 改造点 D：kv_ledger.go 修改位置

**原代码**（`core/ledger/kvledger/kv_ledger.go:211`）：
```go
func (l *kvLedger) Commit(block *common.Block) error {
    err = l.txtmgmt.ValidateAndPrepare(block, true)
    ...
    l.blockStore.AddBlock(block)
    l.txtmgmt.Commit()
    ...
}
```

**改造后**：
```go
func (l *kvLedger) Commit(block *common.Block) error {
    schedule, hasBatchSchedule := bench.ParseBatchSchedule(block)
    if hasBatchSchedule {
        return l.commitWithBatchSchedule(block, schedule)  // [新路径]
    }
    // 原路径
    err = l.txtmgmt.ValidateAndPrepare(block, true)
    ...
    l.blockStore.AddBlock(block)
    l.txtmgmt.Commit()
    ...
}
```

详细实现见 [06-commit-changes.md](06-commit-changes.md)。

---

## 2.5 核心设计决策说明

### BatchSchedule 为何放在 metadata[ORDERER]？

- `BlockMetadata` 不在 `data_hash` 范围内 → 不影响块哈希链
- `metadata[ORDERER]` 在 Solo 模式下当前为 nil → 可直接复用，无冲突
- 通过 `WriteBlock()` 第三个参数 `encodedMetadataValue` 注入 → 最小侵入

### 为何不新增 proto 字段？

- 新增 proto 字段需要重新生成 pb 文件，风险高
- 现有 `ChaincodeProposalPayload.Input.Args` 天然携带 funcName + args
- orderer 和 peer 都可以独立解析，不需要额外字段

### 为何背书阶段 Phase 1 不改？

- 背书阶段仍执行合约 → ESCC 签名内容不变 → VSCC 验证路径无需修改
- 降低 Phase 1 风险；只有提交路径变化
- 系统链码背书完全不受影响

---

*返回：[目录](00-index.md) | 前一篇：[01-current-arch.md](01-current-arch.md) | 下一篇：[03-compatibility.md](03-compatibility.md)*
