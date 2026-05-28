# 04 — 数据结构设计

> 上级目录：[00-index.md](00-index.md) | 前置：[03-compatibility.md](03-compatibility.md)

---

## 4.1 TxIntent / RWIntent

**新建文件**：`core/bench/types.go`

```go
package bench

// BenchmarkType 枚举支持的 benchmark 类型
type BenchmarkType string

const (
    BenchSmallBank BenchmarkType = "smallbank"
    BenchYCSB      BenchmarkType = "ycsb"
)

// KeyInterval 表示一个 key 区间（精确 key 时 Start==End，IsRange=false）
type KeyInterval struct {
    Start   string
    End     string
    IsRange bool   // true 表示范围查询 [Start, End)
}

// HypercubeSet 表示单笔 tx 的读写超立方体集合（KV 场景简化为 key 区间集）
type HypercubeSet struct {
    ReadIntervals  []KeyInterval
    WriteIntervals []KeyInterval
}

// ReplayOp 记录提交阶段重放所需的操作信息
type ReplayOp struct {
    Benchmark BenchmarkType
    Function  string
    Args      []string   // 原始 chaincode 调用参数
}

// TxIntent 单笔交易的读写意图（由 orderer 从 tx 参数中提取）
type TxIntent struct {
    TxID          string
    ChannelID     string
    ChaincodeName string
    Benchmark     BenchmarkType
    FunctionName  string
    Args          []string
    ReadKeys      []string
    WriteKeys     []string
    Hypercube     HypercubeSet
    ReplayPlan    ReplayOp
}
```

**字段说明**：
- `ReadKeys` / `WriteKeys`：精确 key 列表（smallbank 场景）
- `Hypercube.ReadIntervals` / `WriteIntervals`：通用区间表示（ycsb scan 场景）
- `ReplayPlan`：提交阶段用于重放的操作描述，包含原始参数

---

## 4.2 BatchSchedule

**新建文件**：`core/bench/schedule.go`

```go
package bench

import (
    "encoding/json"
    "github.com/golang/protobuf/proto"
    cb "github.com/hyperledger/fabric/protos/common"
)

// BatchEntry 单个批次内的交易列表
type BatchEntry struct {
    BatchID int
    TxIDs   []string
}

// BatchSchedule orderer 产生的批次调度结果，写入 BlockMetadata[ORDERER]
type BatchSchedule struct {
    BlockNumber uint64
    Batches     []BatchEntry  // 有序批次列表，提交时严格按此顺序
    Version     string        // schema 版本，用于兼容性检查，如 "v1"
}

// ParseBatchSchedule 从 block metadata 解析批次调度信息
func ParseBatchSchedule(block *cb.Block) (*BatchSchedule, bool) {
    if len(block.Metadata.Metadata) <= int(cb.BlockMetadataIndex_ORDERER) {
        return nil, false
    }
    ordererMeta := &cb.Metadata{}
    if err := proto.Unmarshal(
        block.Metadata.Metadata[cb.BlockMetadataIndex_ORDERER],
        ordererMeta,
    ); err != nil {
        return nil, false
    }
    if len(ordererMeta.Value) == 0 {
        return nil, false
    }
    var schedule BatchSchedule
    if err := json.Unmarshal(ordererMeta.Value, &schedule); err != nil {
        return nil, false
    }
    if schedule.Version != "v1" {
        return nil, false
    }
    return &schedule, true
}

// BlockHasBatchSchedule 快速判断 block 是否携带批次调度信息
func BlockHasBatchSchedule(block *cb.Block) bool {
    if len(block.Metadata.Metadata) <= int(cb.BlockMetadataIndex_ORDERER) {
        return false
    }
    return len(block.Metadata.Metadata[cb.BlockMetadataIndex_ORDERER]) > 0
}
```

**序列化方案**：Phase 1 使用 `encoding/json`（调试方便），Phase 3+ 可改为 protobuf（减少体积）。

---

## 4.3 ConflictGraph（内部结构，不进入 block）

**新建文件**：`orderer/bench/graph.go`

```go
package bench

// ConflictGraph 事务冲突无向图（仅在 orderer 内存中使用，不序列化）
type ConflictGraph struct {
    Nodes []string            // txID 列表（按插入顺序，保证确定性）
    Edges map[string][]string // adjacency: txID → []conflicting txID
}

// ColorAssignment 图染色结果（batchID = color）
type ColorAssignment struct {
    TxToColor map[string]int  // txID → batchID（颜色编号，从 0 开始）
    NumColors int
}
```

---

## 4.4 StateDBInterface

**新建文件**：`core/bench/statedb_interface.go`

```go
package bench

// StateDBInterface 抽象重放引擎对底层 statedb 的依赖
// 适配 core/ledger/kvledger/txmgmt/statedb.VersionedDB
type StateDBInterface interface {
    // GetState 读取 namespace 下指定 key 的当前值
    GetState(namespace string, key string) ([]byte, error)
    // ApplyUpdate 写入单个 key（在 batch commit 之前的临时写入）
    ApplyUpdate(namespace string, key string, value []byte) error
}
```

---

## 4.5 数据流总结

```
[Orderer 内部]
  block.Data.Data[i]
    → extractCCInvocation() → (ccName, funcName, args)
    → ExtractSmallBankIntent / ExtractYCSBIntent → TxIntent
    → buildConflictGraph([]*TxIntent) → ConflictGraph
    → greedyGraphColoring(ConflictGraph) → ColorAssignment
    → buildBatchSchedule() → BatchSchedule
    → json.Marshal(BatchSchedule) → []byte

[Block metadata]
  block.Metadata.Metadata[ORDERER] = {Value: BatchSchedule bytes}

[Peer Commit]
  ParseBatchSchedule(block) → *BatchSchedule
    → for each BatchEntry in Batches:
        → findTxInBlock(block, txID) → envBytes
        → extractInvocationFromEnv(envBytes) → (funcName, args, ccName)
        → ReplayEngine.Execute(ccName, funcName, args) → map[key]value
        → StateDB.ApplyUpdate(ns, key, value)
```

---

*返回：[目录](00-index.md) | 前一篇：[03-compatibility.md](03-compatibility.md) | 下一篇：[05-orderer-changes.md](05-orderer-changes.md)*
