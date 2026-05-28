# 01 — 现有架构分析

> 上级目录：[00-index.md](00-index.md)

---

## 1.1 架构总体流程（现有 EOV）

```
Client
  │── SignedProposal ──►  Peer Endorser
                              │ ProcessProposal()
                              │  └── simulateProposal()
                              │       └── callChaincode() → ExecuteChaincode()
                              │           ├── 真实执行合约 (读DB/写DB via TxSimulator)
                              │           └── GetTxSimulationResults() → rwset
                              │  └── endorseProposal() → ESCC 签名 rwset
                              └── ProposalResponse (含 rwset)
  │── SignedTransaction ──►  Orderer
                              │ Enqueue(Envelope)
                              │  └── BlockCutter().Ordered(msg)  → 当达到批量/超时
                              │  └── CreateNextBlock(batch)       → 组装 Block
                              │  └── WriteBlock(block, nil)       → 落盘 + 签名 metadata
  │── Block (via Deliver) ──►  Peer Committer
                              │ LedgerCommitter.Commit(block)
                              │  ├── txvalidator.Validate(block)   → VSCC 背书策略验证
                              │  │    └── 设置 TRANSACTIONS_FILTER
                              │  └── kvLedger.Commit(block)
                              │       ├── txtmgmt.ValidateAndPrepare(block, true)
                              │       │    └── ValidateAndPrepareBatch() → MVCC 验证
                              │       │         └── validateKVRead(): 检查版本是否过期
                              │       ├── blockStore.AddBlock(block)
                              │       └── txtmgmt.Commit()  → 写 LevelDB WorldState
```

---

## 1.2 关键函数与文件位置

| 组件 | 文件 | 关键函数 |
|------|------|---------|
| 背书入口 | `core/endorser/endorser.go` | `ProcessProposal()`, `simulateProposal()`, `callChaincode()`, `endorseProposal()` |
| TxSimulator | `core/ledger/` (接口) | `NewTxSimulator()`, `GetTxSimulationResults()` |
| 读写集构建 | `core/ledger/kvledger/txmgmt/rwsetutil/rwset_builder.go` | `AddToReadSet()`, `AddToWriteSet()`, `AddToRangeQuerySet()` |
| ESCC | `core/scc/escc/` | `EndorserOneValidSignature.Invoke()` |
| Orderer Solo 排序 | `orderer/solo/consensus.go` | `chain.main()`, `Enqueue()` |
| Block 创建 | `orderer/multichain/chainsupport.go` | `CreateNextBlock()`, `WriteBlock()` |
| Block 写元数据 | `orderer/multichain/chainsupport.go:274` | `WriteBlock()` → `block.Metadata.Metadata[ORDERER] = encodedMetadataValue` |
| Peer 验证入口 | `core/committer/committer_impl.go` | `LedgerCommitter.Commit()` |
| 交易格式验证 | `core/committer/txvalidator/validator.go` | `txValidator.Validate()`, `VSCCValidateTx()` |
| MVCC 验证 | `core/ledger/kvledger/txmgmt/validator/statebasedval/state_based_validator.go` | `ValidateAndPrepareBatch()`, `validateKVRead()`, `validateTx()` |
| 提交入口 | `core/ledger/kvledger/kv_ledger.go:211` | `kvLedger.Commit()` |
| 系统链码判断 | `core/scc/importsysccs.go` | `IsSysCC()`, `IsSysCCAndNotInvokableExternal()` |
| SmallBank 合约 | `examples/e2e_cli/benchmark_suite/chaincode/smallbank/smallbank.go` | `Invoke()` + 各操作 |
| YCSB 合约 | `examples/e2e_cli/benchmark_suite/chaincode/ycsb/ycsb.go` | `Invoke()` + 各操作 |

---

## 1.3 Block 数据结构

```
Block
├── Header: {number, previous_hash, data_hash}   ← data_hash 仅覆盖 BlockData
├── Data: {data: [][]byte}                        ← 每个 []byte 是一个 Envelope
└── Metadata: {metadata: [][]byte}               ← 4 个槽位（非 hash 覆盖范围！）
    ├── [0] SIGNATURES      ← orderer 对 block header 的签名
    ├── [1] LAST_CONFIG     ← 最后配置块索引
    ├── [2] TRANSACTIONS_FILTER ← peer 验证结果位图
    └── [3] ORDERER         ← orderer 自定义元数据（Solo 目前传 nil）
```

> **重要**：`BlockMetadata` 不被包含在 `data_hash` 中，因此修改 `metadata[ORDERER]` 不会破坏 block 哈希链完整性，但也不受 orderer 签名保护（研究原型阶段可接受）。

**源码依据**：`orderer/multichain/chainsupport.go:274`
```go
if encodedMetadataValue != nil {
    block.Metadata.Metadata[ORDERER] = utils.MarshalOrPanic(
        &cb.Metadata{Value: encodedMetadataValue})
}
```

---

## 1.4 MVCC 验证逻辑

`validateKVRead()` 核心逻辑（`state_based_validator.go:194`）：
1. 若 `updates`（当前 block 已处理 tx 的写集）中已有该 key → 冲突
2. 从 statedb 查询 key 的已提交版本 `committedVersion`
3. 对比 rwset 中记录的 `kvRead.Version` 与 `committedVersion`
4. 不一致 → `MVCC_READ_CONFLICT` → tx 被标记无效

**高冲突场景的根本问题**：多笔交易并发读取同一账户，其中一笔提交后版本递增，其他笔因版本过时全部失败。这是本改造要解决的核心问题。

---

## 1.5 系统链码列表

系统链码（`core/scc/importsysccs.go`）：`cscc`, `lscc`, `escc`, `vscc`, `qscc`。  
`IsSysCC(name)` 判断依据为名字精确匹配，不影响用户链码。

**保护原则**：系统链码必须始终走原有 MVCC 路径，不得被 benchmark 逻辑误拦截。

---

## 1.6 ESCC 签名覆盖范围

`endorser.go` 中 `endorseProposal()` 调用 ESCC，签名内容为：

```
header + payload + ccid + response(resBytes) + simResult + events + visibility
```

签名**不覆盖** BlockMetadata，因此 BatchSchedule 写入 metadata 不影响 ESCC 签名验证。

---

*返回：[目录](00-index.md) | 下一篇：[02-target-arch.md](02-target-arch.md)*
