# KV 模型下读写意图提取 + 超立方体冲突检测 + 批次重放架构改造方案

> **文档说明**：本文档基于 Hyperledger Fabric v1.0 源码分析与毕设开题报告，面向 KV 模型（smallbank / ycsb）设计可快速落地的架构改造方案。仅涉及 KV 模型，不涉及关系型模型。

---

## 一、项目现有架构理解

### 1.1 架构总体流程（现有 EOV）

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

### 1.2 关键函数与文件位置

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

### 1.3 Block 数据结构

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

### 1.4 MVCC 验证逻辑

`validateKVRead()` 核心逻辑（`state_based_validator.go:194`）：
1. 若 `updates`（当前 block 已处理 tx 的写集）中已有该 key → 冲突
2. 从 statedb 查询 key 的已提交版本 `committedVersion`
3. 对比 rwset 中记录的 `kvRead.Version` 与 `committedVersion`
4. 不一致 → `MVCC_READ_CONFLICT` → tx 被标记无效

高冲突场景下，大量 tx 因版本过时而失败，这是本课题要解决的核心问题。

### 1.5 系统链码列表

系统链码（`core/scc/importsysccs.go`）：`cscc`, `lscc`, `escc`, `vscc`, `qscc`。  
`IsSysCC(name)` 判断依据为名字精确匹配，不影响用户链码。

---

## 二、目标架构设计

### 2.1 新架构整体流程

```
Client Proposal
  │
  ▼
Peer Endorsement / Intent Extraction          [改造点 A]
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
Orderer Receive TX                             [改造点 B]
  │
  ▼
Orderer Build Block (BlockCutter)
  │
  ▼
Orderer Analyze Hypercube Conflicts            [改造点 C]
  ├── 遍历 block 内所有 Envelope
  ├── 解析 ChaincodeInvocationSpec.Input.Args 获取 funcName + args
  ├── 识别 smallbank/ycsb → ExtractIntent → 得到 readKeys, writeKeys
  ├── 为每笔 tx 构建 HypercubeSet
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
Peer Commit with Batch Order                   [改造点 D]
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

## 三、新旧流程兼容策略

### 3.1 合约分流规则

通过 `chaincode_id.Name` 字段识别合约类型：

```go
const (
    BenchmarkSmallBank = "smallbank"
    BenchmarkYCSB      = "ycsb"
)

func IsBenchmarkChaincode(name string) bool {
    return name == BenchmarkSmallBank || name == BenchmarkYCSB
}
```

分流时机：
- **Orderer 侧**：`CreateNextBlock` 后，遍历 tx 时检查 chaincode 名称
- **Peer commit 侧**：读取 BatchSchedule 后，对每笔 tx 检查 chaincode 名称

### 3.2 兼容矩阵

| TX 类型 | 背书阶段 | Orderer 分析 | Commit 路径 |
|---------|---------|-------------|------------|
| smallbank / ycsb | 原有路径（仍执行合约，生成 rwset） | 提取 intent，参与染色 | 批次重放，跳过 MVCC |
| 系统链码（cscc/lscc/escc/vscc/qscc） | 原有路径 | 跳过 intent 提取，标记为 `non-benchmark` | 原有 MVCC 路径 |
| 普通用户链码（非 benchmark） | 原有路径 | 跳过 intent 提取，标记为 `non-benchmark` | 原有 MVCC 路径 |

### 3.3 混合 Block 处理

同一 block 内可能同时包含 benchmark tx 和普通 tx：
- Orderer 只对 benchmark tx 构建 hypercube，普通 tx 视为不冲突（可放任意 batch）
- Peer commit：有 BatchSchedule 时，benchmark tx 走重放路径，普通 tx 走原 MVCC 路径
- 普通 tx 不在 BatchSchedule 中 → 按原 txsFilter 处理

### 3.4 降级开关

在 `orderer/localconfig/` 或通过 viper 配置添加环境变量：
```
FABRIC_BENCH_INTENT_ENABLED=true/false
```
若为 false，orderer 不执行 intent 分析，WriteBlock 传 nil，peer 走原路径。

---

## 四、数据结构设计

### 4.1 TxIntent / RWIntent

```go
// core/bench/intent.go

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

### 4.2 BatchSchedule

```go
// core/bench/schedule.go

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

// BlockHasBatchSchedule 判断 block 是否携带批次调度信息
func BlockHasBatchSchedule(block *common.Block) bool {
    if len(block.Metadata.Metadata) <= int(common.BlockMetadataIndex_ORDERER) {
        return false
    }
    data := block.Metadata.Metadata[common.BlockMetadataIndex_ORDERER]
    return len(data) > 0
}
```

**序列化方案**：采用 `encoding/json` 简单序列化（Phase 1），后续可改为 protobuf。

### 4.3 ConflictGraph（内部结构，不进入 block）

```go
// orderer/bench/graph.go

type ConflictGraph struct {
    Nodes []string            // txID 列表
    Edges map[string][]string // adjacency: txID → []conflicting txID
}

// 图染色结果
type ColorAssignment struct {
    TxToColor map[string]int  // txID → batchID（颜色编号）
    NumColors int
}
```

---

## 五、背书阶段修改点

### 5.1 修改策略

**Phase 1 背书阶段无需修改**。原因：
- smallbank/ycsb 合约执行仍生成合法 rwset，ESCC 可正常签名
- Orderer 和 Peer commit 阶段从 **ChaincodeProposalPayload.Input.Args** 中提取 intent，不依赖背书结果
- VSCC 背书策略验证仍正常进行（对 rwset 签名验证）

> **关键洞察**：Orderer 收到 Envelope 后，可以通过以下路径完整还原合约调用参数：
> ```
> Envelope → Payload → Transaction → TransactionAction[0] → ChaincodeActionPayload
>   → chaincode_proposal_payload (bytes) → ChaincodeProposalPayload → Input (ChaincodeInput)
>   → Args[0] = functionName bytes, Args[1..] = function arguments
> ```
> 这个解析路径不需要背书阶段做任何修改。

### 5.2 背书阶段拦截点（可选优化，Phase 2+）

若需要在背书阶段跳过真实执行（提升背书性能），修改 `core/endorser/endorser.go` 的 `simulateProposal()` 函数：

```
simulateProposal()
  └── 检查 cid.Name 是否为 benchmark
      ├── YES → IntentExtractor.Extract(funcName, args)
      │         生成仅含 key 信息（无版本）的 mock rwset
      │         跳过真实 ExecuteChaincode 调用
      └── NO  → 原有逻辑（ExecuteChaincode + GetTxSimulationResults）
```

**Phase 1 暂不实施**，保留原有 simulateProposal 以维持 VSCC 兼容性。

---

## 六、Orderer 阶段修改点

### 6.1 修改文件

- `orderer/solo/consensus.go`：在 `main()` 函数中，`CreateNextBlock` 之后、`WriteBlock` 之前插入分析逻辑

### 6.2 核心改动位置

**原代码**（`orderer/solo/consensus.go:98-101`）：
```go
block := ch.support.CreateNextBlock(batch)
ch.support.WriteBlock(block, committers[i], nil)
```

**改造后**：
```go
block := ch.support.CreateNextBlock(batch)
// [新增] 提取 intent，构建冲突图，生成 BatchSchedule
batchScheduleBytes := analyzeBatchAndSchedule(block)
ch.support.WriteBlock(block, committers[i], batchScheduleBytes)
```

### 6.3 新增函数：analyzeBatchAndSchedule

**新建文件**：`orderer/bench/analyzer.go`

```go
// analyzeBatchAndSchedule 分析 block 内所有 tx，生成批次调度字节
func analyzeBatchAndSchedule(block *cb.Block) []byte {
    intents := extractIntentsFromBlock(block)
    if len(intents) == 0 {
        return nil  // 无 benchmark tx，不附加调度信息
    }
    graph := buildConflictGraph(intents)
    coloring := greedyGraphColoring(graph)
    schedule := buildBatchSchedule(block.Header.Number, intents, coloring)
    data, _ := json.Marshal(schedule)
    return data
}
```

### 6.4 Intent 提取（从 Envelope 解析）

```go
// extractIntentsFromBlock 遍历 block 内所有 envelope，提取 benchmark tx 的 intent
func extractIntentsFromBlock(block *cb.Block) []*TxIntent {
    var intents []*TxIntent
    for _, envBytes := range block.Data.Data {
        env, _ := utils.GetEnvelopeFromBlock(envBytes)
        payload, _ := utils.GetPayload(env)
        chdr, _ := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
        if common.HeaderType(chdr.Type) != common.HeaderType_ENDORSER_TRANSACTION {
            continue
        }
        tx, _ := utils.GetTransaction(payload.Data)
        cap, _ := utils.GetChaincodeActionPayload(tx.Actions[0].Payload)
        cpp, _ := utils.GetChaincodeProposalPayload(cap.ChaincodeProposalPayload)
        cis := &peer.ChaincodeInvocationSpec{}
        proto.Unmarshal(cpp.Input, cis)

        ccName := chdr.Extension // 通过 GetChaincodeHeaderExtension 获取
        hdrExt, _ := utils.GetChaincodeHeaderExtension(payload.Header)
        ccName = hdrExt.ChaincodeId.Name

        if !IsBenchmarkChaincode(ccName) {
            continue
        }
        args := cis.ChaincodeSpec.Input.Args
        funcName := string(args[0])
        strArgs := argsToStrings(args[1:])

        var intent *TxIntent
        switch ccName {
        case BenchmarkSmallBank:
            intent = ExtractSmallBankIntent(chdr.TxId, chdr.ChannelId, funcName, strArgs)
        case BenchmarkYCSB:
            intent = ExtractYCSBIntent(chdr.TxId, chdr.ChannelId, funcName, strArgs)
        }
        if intent != nil {
            intents = append(intents, intent)
        }
    }
    return intents
}
```

### 6.5 Block 内非 benchmark tx 的处理

- 非 benchmark tx（系统链码、普通链码）不在 ConflictGraph 中
- 它们不参与图染色，在 BatchSchedule 中用一个特殊字段标记为 `non-batch`
- Peer commit 阶段检测到 tx 不在任何 batch 中时，走原有 MVCC 路径

---

## 七、验证与提交阶段修改点

### 7.1 修改文件

- `core/ledger/kvledger/kv_ledger.go`：修改 `Commit()` 函数
- 新增文件 `core/bench/replay.go`：实现重放引擎

### 7.2 修改 kvLedger.Commit()

**原代码**（`kv_ledger.go:211`）：
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
    // 尝试解析批次调度信息
    schedule, hasBatchSchedule := bench.ParseBatchSchedule(block)

    if hasBatchSchedule {
        // 新路径：batch-aware 提交
        return l.commitWithBatchSchedule(block, schedule)
    }
    // 原路径：MVCC 验证 + 提交
    err = l.txtmgmt.ValidateAndPrepare(block, true)
    ...
    l.blockStore.AddBlock(block)
    l.txtmgmt.Commit()
    ...
}
```

### 7.3 新增：commitWithBatchSchedule()

```go
func (l *kvLedger) commitWithBatchSchedule(block *common.Block, schedule *bench.BatchSchedule) error {
    // 1. 先进行 VSCC 验证（沿用原有逻辑：签名、背书策略）
    //    通过 committer 的 validator.Validate(block) 已经完成，此处无需重复
    //    （txvalidator.Validate 在 LedgerCommitter.Commit 中已调用）

    // 2. 收集 block 内非 benchmark tx 的 rwset（走原有 MVCC 路径处理）
    nonBenchUpdates, err := l.prepareNonBenchTxUpdates(block, schedule)
    if err != nil {
        return err
    }

    // 3. 先将 block 存入 blockStore
    if err := l.blockStore.AddBlock(block); err != nil {
        return err
    }

    // 4. 按 batch 顺序重放 benchmark tx
    stateDB := l.txtmgmt.GetStateDB() // 获取底层 statedb 接口
    replayEngine := bench.NewReplayEngine(stateDB)

    for _, batchEntry := range schedule.Batches {
        // Phase 1: 串行处理每个批次（Phase 3 改为并行）
        for _, txID := range batchEntry.TxIDs {
            txEnvBytes := findTxInBlock(block, txID)
            if txEnvBytes == nil {
                continue
            }
            funcName, strArgs, ccName, err := extractInvocationFromEnv(txEnvBytes)
            if err != nil {
                continue
            }
            writes, err := replayEngine.Execute(ccName, funcName, strArgs)
            if err != nil {
                // 重放失败：标记 tx 无效，继续处理其他 tx
                markTxInvalid(block, txID)
                continue
            }
            // 将写结果写入 stateDB
            for key, value := range writes {
                stateDB.ApplyUpdate(ccName, key, value)
            }
        }
    }

    // 5. 提交非 benchmark tx 的更新（原有 MVCC 结果）
    applyNonBenchUpdates(stateDB, nonBenchUpdates)

    // 6. 持久化
    stateDB.Commit()
    return nil
}
```

### 7.4 MVCC 处理策略

| 情况 | 处理方式 |
|------|---------|
| benchmark tx（在 BatchSchedule 中） | **跳过 MVCC**，直接重放计算 fresh value |
| 普通 tx（非 benchmark，不在 BatchSchedule 中） | **保留 MVCC**，走原有 validateKVRead 路径 |
| 系统链码 tx | **保留原路径**，MVCC 正常 |

---

## 八、冲突检测算法设计

### 8.1 KV 场景超立方体表示

在 KV 模型中，超立方体退化为**一维 key 区间**：
- 精确 key：`[key, key]`（点区间）
- 范围查询：`[startKey, endKey)`（区间，ycsb scan 使用）

对于 smallbank，账户 key 的格式为 `account:<accountID>`，每笔 tx 涉及的 key 是有限个精确点。

### 8.2 冲突规则

```
定义：tx A 与 tx B 冲突，当且仅当：
  WA ∩ RB ≠ ∅  （A 写了 B 读的 key）
  OR
  RA ∩ WB ≠ ∅  （B 写了 A 读的 key）
  OR
  WA ∩ WB ≠ ∅  （同时写同一 key，存在数据竞争）

注：读-读不冲突（多个 tx 可同时读取同一 key）
```

对于精确 key：区间相交 = key 相等。  
对于 key 范围 `[s1, e1)` 与 `[s2, e2)` 相交 = `s1 < e2 && s2 < e1`。

### 8.3 冲突图构建（伪代码）

```
buildConflictGraph(intents []*TxIntent) -> ConflictGraph:
    graph = new ConflictGraph(nodes = [tx.TxID for tx in intents])
    
    for i = 0 to len(intents)-1:
        for j = i+1 to len(intents)-1:
            txi = intents[i]
            txj = intents[j]
            
            conflict = false
            // 检查 txi.writes ∩ txj.reads
            for wi in txi.WriteIntervals:
                for rj in txj.ReadIntervals:
                    if intersects(wi, rj): conflict = true; break
            // 检查 txj.writes ∩ txi.reads
            for wj in txj.WriteIntervals:
                for ri in txi.ReadIntervals:
                    if intersects(wj, ri): conflict = true; break
            // 检查 writes ∩ writes
            for wi in txi.WriteIntervals:
                for wj in txj.WriteIntervals:
                    if intersects(wi, wj): conflict = true; break
            
            if conflict:
                graph.AddEdge(txi.TxID, txj.TxID)
    
    return graph

// 精确 key 相交
intersects(a, b KeyInterval) -> bool:
    if !a.IsRange && !b.IsRange:
        return a.Start == b.Start
    // 范围相交判断
    aEnd = if a.IsRange then a.End else a.Start + "\x01"  // 精确 key 等价于点区间
    bEnd = if b.IsRange then b.End else b.Start + "\x01"
    return a.Start < bEnd && b.Start < aEnd
```

### 8.4 贪心图染色（伪代码）

```
greedyGraphColoring(graph ConflictGraph) -> ColorAssignment:
    color = {}         // txID → batchID
    maxColor = 0
    
    for txID in graph.Nodes:  // 按 txID 字典序或插入顺序（保证确定性）
        usedColors = {color[neighbor] for neighbor in graph.Edges[txID] if neighbor in color}
        c = 0
        while c in usedColors:
            c++
        color[txID] = c
        maxColor = max(maxColor, c)
    
    return ColorAssignment{TxToColor: color, NumColors: maxColor+1}

// 注：为保证所有 orderer 节点确定性输出相同结果，
//     遍历顺序必须固定（如按 TxID 字典序）
```

### 8.5 生成 BatchSchedule（伪代码）

```
buildBatchSchedule(blockNum uint64, intents []*TxIntent, coloring ColorAssignment) -> BatchSchedule:
    batches = {}  // batchID → []txID

    for intent in intents:
        batchID = coloring.TxToColor[intent.TxID]
        batches[batchID].append(intent.TxID)
    
    schedule = BatchSchedule{
        BlockNumber: blockNum,
        Batches: sortedByBatchID(batches),
        Version: "v1",
    }
    return schedule
```

---

## 九、SmallBank 完整实现方案

### 9.1 Key 格式分析

```
账户 key：  "account:<accountID>"
回执 key：  "receipt:<requestID>"
```

所有业务操作仅涉及 `account:` 命名空间的 key，回执 key 仅在提供 requestID 时写入。

### 9.2 各操作读写分析

| 操作 | 参数 | Read Keys | Write Keys | 依赖当前状态 | 是否可预计算写值 |
|------|------|-----------|------------|------------|----------------|
| `Balance` | accountID | `account:A` | ∅ | 否 | - |
| `CreateAccount` | A,name,checking,savings | `account:A`（存在检查）| `account:A` | 否 | **是**（直接用参数） |
| `DepositChecking` | A, amount | `account:A` | `account:A` | 是（checking += amount）| **否** |
| `TransactSavings` | A, amount | `account:A` | `account:A` | 是（savings += amount，需判断余额）| **否** |
| `WriteCheck` | A, amount | `account:A` | `account:A` | 是（checking -= amount，有条件）| **否** |
| `Amalgamate` | src, dest | `account:src`, `account:dest` | `account:src`, `account:dest` | 是（src全清→dest）| **否** |
| `SendPayment` | from, to, amount | `account:from`, `account:to` | `account:from`, `account:to` | 是（from.checking -= amount，有余额检查）| **否** |

**结论**：除 `CreateAccount` 外，所有写操作均依赖当前状态，无法在背书阶段预计算写值。提交阶段必须重放操作。

### 9.3 SmallBank Intent 提取

新建文件：`orderer/bench/extractor_smallbank.go`

```go
// ExtractSmallBankIntent 从 funcName + args 提取 SmallBank 交易的读写意图
func ExtractSmallBankIntent(txID, channelID, funcName string, args []string) *TxIntent {
    intent := &TxIntent{
        TxID:          txID,
        ChannelID:     channelID,
        ChaincodeName: BenchmarkSmallBank,
        Benchmark:     BenchSmallBank,
        FunctionName:  funcName,
        Args:          args,
        ReplayPlan:    ReplayOp{Benchmark: BenchSmallBank, Function: funcName, Args: args},
    }

    switch funcName {
    case "Balance":
        // 纯读操作，无写
        if len(args) < 1 { return nil }
        intent.ReadKeys = []string{accountKey(args[0])}
        intent.WriteKeys = nil
    case "CreateAccount":
        if len(args) < 2 { return nil }
        k := accountKey(args[0])
        intent.ReadKeys = []string{k}
        intent.WriteKeys = []string{k}
    case "DepositChecking", "TransactSavings", "WriteCheck":
        if len(args) < 1 { return nil }
        k := accountKey(args[0])
        intent.ReadKeys = []string{k}
        intent.WriteKeys = []string{k}
    case "Amalgamate":
        if len(args) < 2 { return nil }
        srcKey := accountKey(args[0])
        dstKey := accountKey(args[1])
        intent.ReadKeys = []string{srcKey, dstKey}
        intent.WriteKeys = []string{srcKey, dstKey}
    case "SendPayment":
        if len(args) < 2 { return nil }
        fromKey := accountKey(args[0])
        toKey := accountKey(args[1])
        intent.ReadKeys = []string{fromKey, toKey}
        intent.WriteKeys = []string{fromKey, toKey}
    default:
        return nil  // 未知操作，不参与 intent 分析
    }

    // 构建超立方体（KV 场景下即精确 key 集合）
    intent.Hypercube = buildPointHypercube(intent.ReadKeys, intent.WriteKeys)
    return intent
}

func accountKey(accountID string) string { return "account:" + strings.TrimSpace(accountID) }
```

### 9.4 SmallBank 重放引擎

新建文件：`core/bench/replay_smallbank.go`

```go
// replaySmallBank 在当前 stateDB 上重放 SmallBank 操作，返回写集
func replaySmallBank(stateDB StateDBInterface, funcName string, args []string) (map[string][]byte, error) {
    switch funcName {
    case "DepositChecking":
        return replayDepositChecking(stateDB, args)
    case "TransactSavings":
        return replayTransactSavings(stateDB, args)
    case "WriteCheck":
        return replayWriteCheck(stateDB, args)
    case "Amalgamate":
        return replayAmalgate(stateDB, args)
    case "SendPayment":
        return replaySendPayment(stateDB, args)
    case "CreateAccount":
        return replayCreateAccount(stateDB, args)
    default:
        return nil, fmt.Errorf("unknown function: %s", funcName)
    }
}

func replayDepositChecking(db StateDBInterface, args []string) (map[string][]byte, error) {
    accountID := args[0]
    amount, _ := strconv.ParseInt(args[1], 10, 64)
    
    account, err := getAccountFromDB(db, accountID)
    if err != nil { return nil, err }
    
    account.Checking += amount
    value, _ := json.Marshal(account)
    return map[string][]byte{accountKey(accountID): value}, nil
}

func replayAmalgate(db StateDBInterface, args []string) (map[string][]byte, error) {
    src, _ := getAccountFromDB(db, args[0])
    dst, _ := getAccountFromDB(db, args[1])
    
    transfer := src.Checking + src.Savings
    src.Checking = 0
    src.Savings = 0
    dst.Checking += transfer
    
    srcBytes, _ := json.Marshal(src)
    dstBytes, _ := json.Marshal(dst)
    return map[string][]byte{
        accountKey(args[0]): srcBytes,
        accountKey(args[1]): dstBytes,
    }, nil
}
// ... 其他操作类似
```

### 9.5 SmallBank 冲突规则

由于所有 key 都是精确点（无范围），冲突检测退化为：
- 两笔 tx 涉及**同一账户 key**，且**至少一笔有写操作** → 冲突

---

## 十、YCSB 简化实现方案

### 10.1 Key 格式分析

```
数据 key：  任意字符串（如 "user000001"）
回执 key：  "receipt:<requestID>"
```

### 10.2 各操作读写分析

| 操作 | 参数 | Read Keys | Write Keys | 写值依赖状态 | 可预计算写值 |
|------|------|-----------|------------|------------|------------|
| `Read` | key | key | ∅ | 否 | - |
| `Insert` | key, value | key（存在检查） | key | 否 | **是**（value 直接在参数中）|
| `Update` | key, value | key（存在检查） | key | 否 | **是**（value 直接在参数中）|
| `Scan` | startKey, limit | [startKey, MaxRune) 范围 | ∅ | 否 | - |

**YCSB 的优势**：Update/Insert 的 **写值直接由参数提供**，提交阶段可以直接写入，无需读取当前状态。

### 10.3 YCSB Intent 提取

新建文件：`orderer/bench/extractor_ycsb.go`

```go
func ExtractYCSBIntent(txID, channelID, funcName string, args []string) *TxIntent {
    intent := &TxIntent{
        TxID:          txID,
        ChannelID:     channelID,
        ChaincodeName: BenchmarkYCSB,
        Benchmark:     BenchYCSB,
        FunctionName:  funcName,
        Args:          args,
        ReplayPlan:    ReplayOp{Benchmark: BenchYCSB, Function: funcName, Args: args},
    }

    switch funcName {
    case "Read":
        if len(args) < 1 { return nil }
        intent.ReadKeys = []string{args[0]}
        intent.WriteKeys = nil
        intent.Hypercube = buildPointHypercube(intent.ReadKeys, nil)
    case "Insert", "Update":
        if len(args) < 1 { return nil }
        key := args[0]
        intent.ReadKeys = []string{key}
        intent.WriteKeys = []string{key}
        intent.Hypercube = buildPointHypercube(intent.ReadKeys, intent.WriteKeys)
    case "Scan":
        if len(args) < 1 { return nil }
        startKey := args[0]
        endKey := string(utf8.MaxRune)
        intent.ReadKeys = nil  // 范围读，用 ReadIntervals 表示
        intent.WriteKeys = nil
        intent.Hypercube = HypercubeSet{
            ReadIntervals: []KeyInterval{{Start: startKey, End: endKey, IsRange: true}},
        }
    default:
        return nil
    }
    return intent
}
```

### 10.4 YCSB 重放引擎

```go
func replayYCSB(stateDB StateDBInterface, funcName string, args []string) (map[string][]byte, error) {
    switch funcName {
    case "Insert", "Update":
        key := args[0]
        value, _ := normalizeValuePayload(args[1])  // 与合约保持一致的解析逻辑
        record, _ := json.Marshal(ycsbRecord{Key: key, Value: value})
        return map[string][]byte{key: record}, nil
    case "Read":
        return map[string][]byte{}, nil  // 纯读，无写
    case "Scan":
        return map[string][]byte{}, nil  // 纯读，无写
    default:
        return nil, fmt.Errorf("unknown ycsb function: %s", funcName)
    }
}
```

### 10.5 YCSB Scan 冲突检测

Scan 操作读取 `[startKey, MaxRune)` 范围内的所有 key：
- Scan 与 Update/Insert 在同一范围内 → 冲突（写操作可能改变 Scan 的结果）
- Scan 与 Scan → 不冲突（读-读）
- Update/Insert 之间同一 key → 冲突

---

## 十一、链上数据与兼容性

### 11.1 intent/plan 存放位置选择

| 方案 | 存放位置 | 优点 | 缺点 | 结论 |
|------|---------|------|------|------|
| A | ChaincodeProposalPayload.Input.Args（现有） | 无需修改任何格式 | orderer/peer 需解析 | **推荐（Phase 1）** |
| B | ChaincodeAction.response.payload（合约返回值） | 合约自带，签名保护 | 需修改合约，破坏现有行为 | Phase 2 可选 |
| C | 新增 proto 字段 | 结构清晰 | 需重新生成 pb，兼容性风险 | 暂不采用 |
| D | BlockMetadata[ORDERER]（BatchSchedule 已在此）| 现有槽位 | 不覆盖 block 签名 | **BatchSchedule 使用** |

**最终方案**：
- **Intent 信息**：不显式存储，在 orderer 和 peer commit 阶段直接从 `ChaincodeProposalPayload.Input.Args` 解析
- **BatchSchedule**：存入 `block.Metadata.Metadata[ORDERER]`，通过 `WriteBlock()` 的第三个参数传入

### 11.2 对区块哈希和签名的影响

| 变更 | 影响 |
|------|------|
| BatchSchedule 写入 `BlockMetadata[ORDERER]` | `BlockMetadata` 不在 `data_hash` 中，不影响块哈希链 |
| orderer 签名覆盖范围 | 签名覆盖 `block.Header.Bytes()`，不包含 metadata，BatchSchedule 不受签名保护 |
| ESCC 签名范围 | 签名覆盖 `header + payload + ccid + response + simResult + events`，不涉及 BatchSchedule |
| 背书验证（VSCC） | 沿用原有逻辑，不受影响 |

### 11.3 执行计划留痕

Phase 1 **不留痕**：intent 在 orderer 内存中计算，BatchSchedule 写入 metadata，重放时从 tx args 重新提取。  
原始交易 args 天然保存在区块中（`ChaincodeProposalPayload.Input`），构成审计依据。

---

## 十二、风险点和规避方案

| 风险 | 描述 | 规避方案 |
|------|------|---------|
| **系统链码被误处理** | cscc/lscc 等被误判为 benchmark | `IsBenchmarkChaincode()` 精确匹配名称；`IsSysCC()` 检查优先 |
| **MVCC 与新排序冲突** | 普通 tx 仍走 MVCC，可能与 benchmark tx 产生隐式依赖 | 混合 block 中，benchmark tx 走重放路径，普通 tx 仍走 MVCC；两类 tx 互不影响状态更新时序 |
| **提交并发写 stateDB 不安全** | Phase 3 同 batch 并行时多线程写同一 key | 同 batch 内无冲突（图染色保证），但需在 ReplayEngine 内维护 batch 内临时写缓存，确保同 batch 内读操作看到最新中间状态 |
| **BlockMetadata 不一致** | peer 无法解析 BatchSchedule → panic | 解析失败时降级到原有 MVCC 路径，日志 warning |
| **orderer 解析 tx payload 失败** | 格式错误或非 benchmark tx | tx 无法解析时，跳过该 tx（视为 non-benchmark），不影响其他 tx 的冲突分析 |
| **背书阶段 simResult 与重放结果不一致** | 背书时 chaincode 读到 version V，提交时状态已变 | 这正是新架构要解决的问题：**跳过 MVCC，用重放结果覆盖旧 rwset 写值** |
| **Scan 幻读问题（YCSB）** | Scan 读取的 key 范围在排序后发生插入 | Scan 与同范围内 Insert/Update 在图中连边，强制不同 batch → 串行执行 |
| **背书后 tx args 被篡改** | 中间人修改 args → 重放结果不符预期 | args 在 ChaincodeProposalPayload 中，被 endorser 签名保护；peer 提交前会验证签名（VSCC） |
| **图染色 NP-Hard，orderer 超时** | batch 过大时贪心染色耗时 | 贪心算法 O(V+E) 线性，对正常 batch 大小（100-1000 tx）毫秒级完成；设置超时降级 |
| **Proto 字段未向后兼容** | 旧 peer 不认识新 BatchSchedule 格式 | BatchSchedule 写在 metadata[ORDERER]，旧 peer 只是忽略此字段，不会 crash |

---

## 十三、分阶段落地计划

### Phase 1：最小闭环（估计 1-2 周）

**目标**：smallbank benchmark 可跑通，orderer 产生 BatchSchedule，peer 按 batch 串行重放

**改动清单**：

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P1-1 | 新建 `core/bench/types.go` | 定义 TxIntent, BatchSchedule, BatchEntry, KeyInterval, HypercubeSet, ReplayOp |
| P1-2 | 新建 `orderer/bench/extractor.go` | 实现 ExtractSmallBankIntent（仅 Amalgamate/SendPayment/DepositChecking/TransactSavings/WriteCheck）|
| P1-3 | 新建 `orderer/bench/graph.go` | 实现 ConflictGraph + greedyGraphColoring |
| P1-4 | 新建 `orderer/bench/analyzer.go` | 实现 analyzeBatchAndSchedule（调用 extractor + graph）|
| P1-5 | 修改 `orderer/solo/consensus.go` | main() 中插入 analyzeBatchAndSchedule，传给 WriteBlock |
| P1-6 | 新建 `core/bench/replay_smallbank.go` | 实现 replaySmallBank（各操作重放逻辑）|
| P1-7 | 修改 `core/ledger/kvledger/kv_ledger.go` | Commit() 检测 BatchSchedule，调用 commitWithBatchSchedule |
| P1-8 | 新建 `core/bench/commit.go` | 实现 commitWithBatchSchedule（串行 batch，重放 benchmark tx）|

**验收条件**：
- `smallbank_load_and_run.sh` 正常运行，无 MVCC 失败
- BlockMetadata[ORDERER] 中可解析出 BatchSchedule
- 有冲突的 tx 被分配到不同 batch

### Phase 2：增强 YCSB（估计 1 周）

**目标**：支持 ycsb read/update/insert；支持 scan 的 key 范围冲突检测

**改动清单**：

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P2-1 | 新建 `orderer/bench/extractor_ycsb.go` | ExtractYCSBIntent（Read/Update/Insert/Scan）|
| P2-2 | 修改 `orderer/bench/graph.go` | intersects() 支持范围区间 |
| P2-3 | 新建 `core/bench/replay_ycsb.go` | replayYCSB（Insert/Update 直接写值，Read/Scan 无写）|
| P2-4 | 修改 `orderer/bench/analyzer.go` | 调用 ExtractYCSBIntent |

### Phase 3：同 batch 并行提交（估计 1-2 周）

**目标**：同 batch 内多线程处理，提升并发吞吐量

**改动清单**：

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P3-1 | 修改 `core/bench/commit.go` | batch 内用 goroutine 并行重放；增加 intra-batch write buffer 防止同 batch 内读写竞争 |
| P3-2 | 新增性能统计 | 记录各 batch 大小、重放时间、batch 数量等指标 |

### Phase 4：完善兼容性和稳定性（估计 1 周）

**目标**：配置开关、完善错误处理、降级路径、文档化

**改动清单**：

| 编号 | 文件/模块 | 改动内容 |
|------|----------|---------|
| P4-1 | `sampleconfig/core.yaml` / viper | 添加 `bench.intent.enabled` 开关 |
| P4-2 | `core/bench/commit.go` | 解析失败时完整降级到原 MVCC 路径 |
| P4-3 | 测试 | 验证系统链码（lscc deploy, cscc join）在新架构下仍正常工作 |
| P4-4 | 测试 | 验证混合 block（benchmark tx + 普通 tx）正确处理 |

---

## 十四、关键函数和文件修改清单（完整）

### 需要修改的现有文件

| 文件 | 修改函数 | 修改描述 |
|------|---------|---------|
| `orderer/solo/consensus.go` | `chain.main()` | 在 CreateNextBlock 后插入 analyzeBatchAndSchedule 调用 |
| `core/ledger/kvledger/kv_ledger.go` | `kvLedger.Commit()` | 检测 BatchSchedule，分流到新路径 |

### 需要新建的文件

| 新建文件 | 内容 |
|---------|------|
| `core/bench/types.go` | TxIntent, BatchSchedule, BatchEntry, KeyInterval, HypercubeSet 数据结构定义 |
| `core/bench/commit.go` | commitWithBatchSchedule, ReplayEngine, parseNonBenchTxUpdates |
| `core/bench/replay_smallbank.go` | SmallBank 各操作的重放实现 |
| `core/bench/replay_ycsb.go` | YCSB 各操作的重放实现（Phase 2）|
| `core/bench/statedb_interface.go` | StateDBInterface 接口定义，适配 statedb.VersionedDB |
| `orderer/bench/extractor.go` | IsBenchmarkChaincode(), ExtractSmallBankIntent, ExtractYCSBIntent |
| `orderer/bench/graph.go` | ConflictGraph, buildConflictGraph, greedyGraphColoring, intersects |
| `orderer/bench/analyzer.go` | analyzeBatchAndSchedule（组合 extractor + graph + schedule 生成）|

---

## 十五、待确认问题

在开始编码前，需要与导师/需求方确认以下问题：

1. **背书阶段是否需要跳过真实执行**？  
   当前方案（Phase 1）仍然执行合约，只是提交时跳过 MVCC 改为重放。若需要背书阶段也不真实执行（提升背书 TPS），需要额外修改 `simulateProposal()`，可能影响 ESCC 签名内容，有兼容性风险。

2. **非 benchmark tx（普通链码）如何处理写集与新路径写集的时序**？  
   当 block 内同时存在 benchmark tx 和普通 tx 时，两类 tx 更新同一 key 的行为需要明确定义。当前方案：批次写入（benchmark tx）在非批次写入（普通 MVCC tx）之前执行，两类 tx 视为独立命名空间不冲突。如果它们确实访问同一 key，行为未定义。需要确认是否允许混合 block 中跨类型 key 访问。

3. **orderer 在多 orderer 场景（Kafka）下 BatchSchedule 的一致性**？  
   本文档基于 Solo orderer。Kafka 场景下，BatchSchedule 由接收交付的 orderer 节点计算。由于图染色算法是确定性的，不同 orderer 节点对同一 batch 应产生相同结果，但需要验证。

4. **是否需要保留原有 rwset 的 MVCC 验证作为 double-check**？  
   完全跳过 MVCC 后，如果重放计算出错（如合约逻辑 bug），没有版本检查作为保护。是否需要在 debug 模式下保留 MVCC 作为正确性验证？

5. **benchmark 测试 chaincode 名称是否固定为 "smallbank" / "ycsb"**？  
   若测试脚本部署时使用不同名称，需要配置映射关系。建议通过环境变量或配置文件指定 benchmark chaincode 名称列表。

---

## 附录：原型实现快速参考

### A. 解析 Envelope 获取 chaincode 调用参数（orderer 侧）

```go
import (
    "github.com/hyperledger/fabric/protos/utils"
    pb "github.com/hyperledger/fabric/protos/peer"
    "github.com/golang/protobuf/proto"
)

func extractCCInvocation(envBytes []byte) (ccName, funcName string, args []string, err error) {
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
    if len(rawArgs) < 1 { return }
    funcName = string(rawArgs[0])
    for _, a := range rawArgs[1:] {
        args = append(args, string(a))
    }
    return
}
```

### B. 读写 BatchSchedule（peer 侧）

```go
// 写（orderer WriteBlock 第三个参数）
scheduleBytes, _ := json.Marshal(schedule)
ch.support.WriteBlock(block, committers, scheduleBytes)

// 读（peer Commit 阶段）
func parseBatchSchedule(block *common.Block) (*BatchSchedule, bool) {
    if len(block.Metadata.Metadata) <= int(common.BlockMetadataIndex_ORDERER) {
        return nil, false
    }
    ordererMeta := &common.Metadata{}
    if err := proto.Unmarshal(block.Metadata.Metadata[common.BlockMetadataIndex_ORDERER], ordererMeta); err != nil {
        return nil, false
    }
    if len(ordererMeta.Value) == 0 { return nil, false }
    var schedule BatchSchedule
    if err := json.Unmarshal(ordererMeta.Value, &schedule); err != nil {
        return nil, false
    }
    if schedule.Version != "v1" { return nil, false }
    return &schedule, true
}
```

### C. 从 block 中找到指定 txID 的 Envelope 字节

```go
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
```

---

*文档版本：v1.0 | 生成日期：2026-05-28 | 仅基于本地源码上下文分析，未检索外部资源*
