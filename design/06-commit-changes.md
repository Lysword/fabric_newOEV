# 06 — 验证与提交阶段修改方案

> 上级目录：[00-index.md](00-index.md) | 前置：[05-orderer-changes.md](05-orderer-changes.md)

---

## 6.1 修改文件

- **修改**：`core/ledger/kvledger/kv_ledger.go` — `Commit()` 函数
- **新建**：`core/bench/commit.go` — `commitWithBatchSchedule` 主逻辑
- **新建**：`core/bench/replay_smallbank.go` — SmallBank 重放引擎
- **新建**：`core/bench/replay_ycsb.go` — YCSB 重放引擎（Phase 2）
- **新建**：`core/bench/statedb_interface.go` — 接口定义（见 [04-data-structures.md](04-data-structures.md)）

---

## 6.2 kv_ledger.go 改造

**改造位置**：`core/ledger/kvledger/kv_ledger.go:211`

```go
func (l *kvLedger) Commit(block *common.Block) error {
    // [新增] 尝试解析批次调度信息
    schedule, hasBatchSchedule := bench.ParseBatchSchedule(block)

    if hasBatchSchedule {
        // 新路径：batch-aware 提交
        return l.commitWithBatchSchedule(block, schedule)
    }

    // 原路径：MVCC 验证 + 提交（保持完全不变）
    err = l.txtmgmt.ValidateAndPrepare(block, true)
    ...
    l.blockStore.AddBlock(block)
    l.txtmgmt.Commit()
    ...
}
```

---

## 6.3 commitWithBatchSchedule 实现

**新建文件**：`core/bench/commit.go`

```go
package bench

// commitWithBatchSchedule 按 batch 顺序重放 benchmark tx，非 benchmark tx 走原 MVCC 路径
func (l *kvLedger) commitWithBatchSchedule(
    block *common.Block,
    schedule *BatchSchedule,
) error {
    // 1. VSCC 验证已在 LedgerCommitter.Commit() 中由 txvalidator.Validate 完成，此处无需重复

    // 2. 先将 block 存入 blockStore（确保 block 落盘在状态更新之前）
    if err := l.blockStore.AddBlock(block); err != nil {
        return err
    }

    // 3. 获取底层 statedb 接口
    stateDB := l.txtmgmt.GetStateDB()
    replayEngine := NewReplayEngine(stateDB)

    // 4. 构建 benchmark txID 集合（用于判断某 txID 是否在 BatchSchedule 中）
    benchTxSet := make(map[string]bool)
    for _, entry := range schedule.Batches {
        for _, txID := range entry.TxIDs {
            benchTxSet[txID] = true
        }
    }

    // 5. 按 batch 顺序串行处理 benchmark tx
    for _, batchEntry := range schedule.Batches {
        // Phase 1: 串行（Phase 3 改为 goroutine 并行）
        for _, txID := range batchEntry.TxIDs {
            txEnvBytes := findTxInBlock(block, txID)
            if txEnvBytes == nil {
                continue
            }
            funcName, strArgs, ccName, err := extractInvocationFromEnvBytes(txEnvBytes)
            if err != nil {
                continue
            }
            writes, err := replayEngine.Execute(ccName, funcName, strArgs)
            if err != nil {
                markTxInvalid(block, txID)  // 标记此 tx 无效，继续处理其他 tx
                continue
            }
            for key, value := range writes {
                if err := stateDB.ApplyUpdate(ccName, key, value); err != nil {
                    return err
                }
            }
        }
    }

    // 6. 处理非 benchmark tx（走原有 MVCC 路径）
    if err := l.processNonBenchTxs(block, benchTxSet); err != nil {
        return err
    }

    // 7. 持久化 stateDB
    return stateDB.Commit()
}
```

---

## 6.4 ReplayEngine 接口

```go
// ReplayEngine 根据 funcName + args 重放操作，返回写集
type ReplayEngine struct {
    db StateDBInterface
}

func NewReplayEngine(db StateDBInterface) *ReplayEngine {
    return &ReplayEngine{db: db}
}

// Execute 重放操作，返回 namespace → key → value 的写集
func (e *ReplayEngine) Execute(
    ccName, funcName string,
    args []string,
) (map[string][]byte, error) {
    switch ccName {
    case BenchmarkSmallBank:
        return replaySmallBank(e.db, funcName, args)
    case BenchmarkYCSB:
        return replayYCSB(e.db, funcName, args)
    default:
        return nil, fmt.Errorf("unsupported chaincode for replay: %s", ccName)
    }
}
```

---

## 6.5 辅助函数

```go
// findTxInBlock 在 block 中找到指定 txID 的 Envelope 字节
func findTxInBlock(block *common.Block, txID string) []byte {
    for _, envBytes := range block.Data.Data {
        env, err := utils.GetEnvelopeFromBlock(envBytes)
        if err != nil { continue }
        payload, err := utils.GetPayload(env)
        if err != nil { continue }
        chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
        if err != nil { continue }
        if chdr.TxId == txID { return envBytes }
    }
    return nil
}

// extractInvocationFromEnvBytes 从 Envelope 字节中提取合约调用信息
func extractInvocationFromEnvBytes(envBytes []byte) (funcName string, args []string, ccName string, err error) {
    env, err := utils.GetEnvelopeFromBlock(envBytes)
    if err != nil { return }
    payload, err := utils.GetPayload(env)
    if err != nil { return }
    hdrExt, err := utils.GetChaincodeHeaderExtension(payload.Header)
    if err != nil { return }
    ccName = hdrExt.ChaincodeId.Name

    tx, err := utils.GetTransaction(payload.Data)
    if err != nil { return }
    cap, err := utils.GetChaincodeActionPayload(tx.Actions[0].Payload)
    if err != nil { return }
    cpp, err := utils.GetChaincodeProposalPayload(cap.ChaincodeProposalPayload)
    if err != nil { return }
    cis := &pb.ChaincodeInvocationSpec{}
    if err = proto.Unmarshal(cpp.Input, cis); err != nil { return }

    rawArgs := cis.ChaincodeSpec.Input.Args
    if len(rawArgs) < 1 { err = fmt.Errorf("empty args"); return }
    funcName = string(rawArgs[0])
    for _, a := range rawArgs[1:] {
        args = append(args, string(a))
    }
    return
}
```

---

## 6.6 MVCC 处理策略

| 情况 | 处理方式 |
|------|---------|
| benchmark tx（在 BatchSchedule 中） | **跳过 MVCC**，直接重放计算 fresh value |
| 普通 tx（非 benchmark，不在 BatchSchedule 中） | **保留 MVCC**，走原有 `validateKVRead` 路径 |
| 系统链码 tx | **保留原路径**，MVCC 正常 |
| `ParseBatchSchedule` 失败 | **降级**，整个 block 走原 MVCC 路径 |

---

## 6.7 背书阶段修改点（Phase 1 不需要）

**Phase 1 背书阶段无需修改**。原因：
- smallbank/ycsb 合约执行仍生成合法 rwset，ESCC 可正常签名
- Orderer 和 Peer commit 阶段从 `ChaincodeProposalPayload.Input.Args` 中提取 intent，不依赖背书结果
- VSCC 背书策略验证仍正常进行

**Phase 2+ 可选优化**：修改 `core/endorser/endorser.go:simulateProposal()`，对 benchmark cc 跳过真实执行，生成 mock rwset，提升背书 TPS。

---

*返回：[目录](00-index.md) | 前一篇：[05-orderer-changes.md](05-orderer-changes.md) | 下一篇：[07-conflict-detection.md](07-conflict-detection.md)*
