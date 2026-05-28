# 05 — Orderer 阶段修改方案

> 上级目录：[00-index.md](00-index.md) | 前置：[04-data-structures.md](04-data-structures.md)

---

## 5.1 修改文件

- **修改**：`orderer/solo/consensus.go` — `chain.main()` 函数
- **新建**：`orderer/bench/analyzer.go` — 分析入口
- **新建**：`orderer/bench/extractor.go` — Intent 提取（通用部分）
- **新建**：`orderer/bench/extractor_smallbank.go` — SmallBank 专用提取器
- **新建**：`orderer/bench/extractor_ycsb.go` — YCSB 专用提取器（Phase 2）
- **新建**：`orderer/bench/graph.go` — 冲突图 + 染色

---

## 5.2 consensus.go 改动

**原代码**（`orderer/solo/consensus.go` 关键位置）：
```go
// 原代码
block := ch.support.CreateNextBlock(batch)
ch.support.WriteBlock(block, committers[i], nil)
```

**改造后**：
```go
// 改造后
block := ch.support.CreateNextBlock(batch)
// [新增] 提取 intent，构建冲突图，生成 BatchSchedule
batchScheduleBytes := bench.AnalyzeBatchAndSchedule(block)
ch.support.WriteBlock(block, committers[i], batchScheduleBytes)
```

---

## 5.3 analyzer.go — 主入口

**新建文件**：`orderer/bench/analyzer.go`

```go
package bench

import (
    "encoding/json"
    cb "github.com/hyperledger/fabric/protos/common"
)

// AnalyzeBatchAndSchedule 分析 block 内所有 tx，生成批次调度字节
// 若无 benchmark tx 则返回 nil（orderer 保持原有行为）
func AnalyzeBatchAndSchedule(block *cb.Block) []byte {
    intents := extractIntentsFromBlock(block)
    if len(intents) == 0 {
        return nil
    }
    graph := buildConflictGraph(intents)
    coloring := greedyGraphColoring(graph)
    schedule := buildBatchSchedule(block.Header.Number, intents, coloring)
    data, err := json.Marshal(schedule)
    if err != nil {
        return nil  // 序列化失败，降级为原路径
    }
    return data
}
```

---

## 5.4 extractor.go — Envelope 解析

**新建文件**：`orderer/bench/extractor.go`

```go
package bench

import (
    "github.com/golang/protobuf/proto"
    cb "github.com/hyperledger/fabric/protos/common"
    pb "github.com/hyperledger/fabric/protos/peer"
    "github.com/hyperledger/fabric/protos/utils"
)

// extractIntentsFromBlock 遍历 block 内所有 envelope，提取 benchmark tx 的 intent
func extractIntentsFromBlock(block *cb.Block) []*TxIntent {
    var intents []*TxIntent
    for _, envBytes := range block.Data.Data {
        intent := extractIntentFromEnvBytes(envBytes)
        if intent != nil {
            intents = append(intents, intent)
        }
    }
    return intents
}

func extractIntentFromEnvBytes(envBytes []byte) *TxIntent {
    env, err := utils.GetEnvelopeFromBlock(envBytes)
    if err != nil { return nil }
    payload, err := utils.GetPayload(env)
    if err != nil { return nil }
    chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
    if err != nil { return nil }

    // 只处理背书交易
    if cb.HeaderType(chdr.Type) != cb.HeaderType_ENDORSER_TRANSACTION {
        return nil
    }

    hdrExt, err := utils.GetChaincodeHeaderExtension(payload.Header)
    if err != nil { return nil }
    ccName := hdrExt.ChaincodeId.Name

    if !IsBenchmarkChaincode(ccName) {
        return nil
    }

    tx, err := utils.GetTransaction(payload.Data)
    if err != nil { return nil }
    cap, err := utils.GetChaincodeActionPayload(tx.Actions[0].Payload)
    if err != nil { return nil }
    cpp, err := utils.GetChaincodeProposalPayload(cap.ChaincodeProposalPayload)
    if err != nil { return nil }

    cis := &pb.ChaincodeInvocationSpec{}
    if err = proto.Unmarshal(cpp.Input, cis); err != nil { return nil }

    rawArgs := cis.ChaincodeSpec.Input.Args
    if len(rawArgs) < 1 { return nil }
    funcName := string(rawArgs[0])
    strArgs := argsToStrings(rawArgs[1:])

    switch ccName {
    case BenchmarkSmallBank:
        return ExtractSmallBankIntent(chdr.TxId, chdr.ChannelId, funcName, strArgs)
    case BenchmarkYCSB:
        return ExtractYCSBIntent(chdr.TxId, chdr.ChannelId, funcName, strArgs)
    }
    return nil
}

func argsToStrings(raw [][]byte) []string {
    result := make([]string, len(raw))
    for i, b := range raw {
        result[i] = string(b)
    }
    return result
}

// IsBenchmarkChaincode 判断链码是否为 benchmark 目标
func IsBenchmarkChaincode(name string) bool {
    return name == BenchmarkSmallBank || name == BenchmarkYCSB
}

const (
    BenchmarkSmallBank = "smallbank"
    BenchmarkYCSB      = "ycsb"
)
```

---

## 5.5 buildBatchSchedule — 从染色结果生成 Schedule

```go
// 在 analyzer.go 或 graph.go 中

func buildBatchSchedule(blockNum uint64, intents []*TxIntent, coloring ColorAssignment) *BatchSchedule {
    batches := make(map[int][]string)
    for _, intent := range intents {
        batchID := coloring.TxToColor[intent.TxID]
        batches[batchID] = append(batches[batchID], intent.TxID)
    }

    // 按 batchID 排序生成有序列表
    entries := make([]BatchEntry, 0, coloring.NumColors)
    for i := 0; i < coloring.NumColors; i++ {
        if txIDs, ok := batches[i]; ok {
            entries = append(entries, BatchEntry{BatchID: i, TxIDs: txIDs})
        }
    }

    return &BatchSchedule{
        BlockNumber: blockNum,
        Batches:     entries,
        Version:     "v1",
    }
}
```

---

## 5.6 非 benchmark tx 的处理

- 非 benchmark tx（系统链码、普通链码）不在 ConflictGraph 中
- 它们不参与图染色，不出现在 BatchSchedule 的任何 batch 中
- Peer commit 阶段检测到 tx 不在任何 batch 中时，走原有 MVCC 路径

**`extractIntentFromEnvBytes` 返回 nil 时**：该 tx 被静默跳过，等效于原路径。

---

*返回：[目录](00-index.md) | 前一篇：[04-data-structures.md](04-data-structures.md) | 下一篇：[06-commit-changes.md](06-commit-changes.md)*
