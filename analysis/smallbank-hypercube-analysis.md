# SmallBank 超立方体冲突检测与确定性批次调度分析

本文档从源码层面剖析 Phase 1 实现的四个核心环节：

1. SmallBank 操作如何生成读写超立方体
2. 如何基于超立方体构建冲突图
3. 执行计划如何存储并随 Block 传递
4. 如何生成确定性多批次排序

---

## 1. SmallBank 操作 → 读写超立方体

### 1.1 数据模型

SmallBank 的 stateDB 中，每个账户对应一条 JSON 记录，key 格式为：

```
account:<accountID>
```

`sbAccountKey()` 实现（`orderer/bench/extractor_smallbank.go:69`）：

```go
func sbAccountKey(accountID string) string {
    return "account:" + strings.TrimSpace(accountID)
}
```

链码侧的 `accountKey()` 函数格式完全一致，因此 orderer 提取的 key 与 stateDB 中实际存储的 key 保证对齐。

### 1.2 各操作的读写键集

`ExtractSmallBankIntent`（`orderer/bench/extractor_smallbank.go:13`）根据函数名映射读写集：

| 操作 | 参数 | ReadKeys | WriteKeys | 说明 |
|------|------|----------|-----------|------|
| `Balance(A)` | A | [account:A] | [] | 只读，不修改任何状态 |
| `CreateAccount(A,name,chk,sav)` | A,... | [account:A] | [account:A] | 需要读（防重复创建）再写 |
| `DepositChecking(A,amount)` | A,amount | [account:A] | [account:A] | 读余额 → 加额 → 写回 |
| `TransactSavings(A,amount)` | A,amount | [account:A] | [account:A] | 读余额 → 加减 → 写回 |
| `WriteCheck(A,amount)` | A,amount | [account:A] | [account:A] | 读余额 → 扣款（带透支惩罚） → 写回 |
| `Amalgamate(src,dst)` | src,dst | [account:src, account:dst] | [account:src, account:dst] | 读两个账户 → 合并余额 → 写两个账户 |
| `SendPayment(from,to,amount)` | from,to,amount | [account:from, account:to] | [account:from, account:to] | 读两个账户 → 转账 → 写两个账户 |

**注意**：`Balance` 的 WriteKeys 为 nil，因此它不会与任何操作产生 WW 冲突，仅可能被写操作的 WR 规则标记冲突。

### 1.3 点区间超立方体的构建

KV 模型中"超立方体"退化为一维 **key 区间集合**。对于精确 key，区间退化为点（`IsRange=false`）。

`buildPointHypercube`（`orderer/bench/graph.go:122`）：

```go
func buildPointHypercube(readKeys, writeKeys []string) bench.HypercubeSet {
    h := bench.HypercubeSet{}
    for _, k := range readKeys {
        h.ReadIntervals = append(h.ReadIntervals, bench.KeyInterval{
            Start: k, End: k, IsRange: false,
        })
    }
    for _, k := range writeKeys {
        h.WriteIntervals = append(h.WriteIntervals, bench.KeyInterval{
            Start: k, End: k, IsRange: false,
        })
    }
    return h
}
```

`HypercubeSet`（`core/bench/types.go:29`）的结构：

```go
type HypercubeSet struct {
    ReadIntervals  []KeyInterval
    WriteIntervals []KeyInterval
}

type KeyInterval struct {
    Start   string
    End     string
    IsRange bool  // false = 精确 key，true = 范围区间（Phase 2 YCSB 使用）
}
```

**示例**：`SendPayment("Alice", "Bob", 100)` 生成的超立方体：

```
ReadIntervals:  [{Start:"account:Alice", End:"account:Alice", IsRange:false},
                 {Start:"account:Bob",   End:"account:Bob",   IsRange:false}]
WriteIntervals: [{Start:"account:Alice", End:"account:Alice", IsRange:false},
                 {Start:"account:Bob",   End:"account:Bob",   IsRange:false}]
```

---

## 2. 冲突图的构建

### 2.1 数据结构

冲突图（`orderer/bench/graph.go:13`）是一个内存中的无向图，仅在 orderer 侧使用，不序列化：

```go
type ConflictGraph struct {
    Nodes []string          // 所有 benchmark txID
    Edges map[string][]string  // txID → 与之冲突的 txID 列表
}
```

### 2.2 冲突判定规则

`txsConflict`（`orderer/bench/graph.go:45`）实现三条经典数据库冲突规则：

```
规则 WR：tx_a 写了某 key，tx_b 读了同一 key → 冲突（写后读）
规则 RW：tx_b 写了某 key，tx_a 读了同一 key → 冲突（读后写）
规则 WW：tx_a 和 tx_b 都写了同一 key         → 冲突（写写）
```

在代码中对应：
- `WA ∩ RB`：遍历 a 的 WriteIntervals，检查是否与 b 的 ReadIntervals 相交
- `WA ∩ WB`：遍历 a 的 WriteIntervals，检查是否与 b 的 WriteIntervals 相交（WW）
- `WB ∩ RA`：遍历 b 的 WriteIntervals，检查是否与 a 的 ReadIntervals 相交

### 2.3 区间相交判定

`intersects`（`orderer/bench/graph.go:72`）处理点区间与范围区间两种情形：

```go
func intersects(a, b bench.KeyInterval) bool {
    // 两个都是精确 key：直接比较字符串
    if !a.IsRange && !b.IsRange {
        return a.Start == b.Start
    }
    // 有范围区间：将精确 key 扩展为半开区间 [key, key+"\x00")
    // 使用标准半开区间相交条件：a.Start < b.End && b.Start < a.End
    aEnd := a.End
    if !a.IsRange {
        aEnd = a.Start + "\x00"
    }
    bEnd := b.End
    if !b.IsRange {
        bEnd = b.Start + "\x00"
    }
    return a.Start < bEnd && b.Start < aEnd
}
```

**关键设计**：`"\x00"` 是 ASCII 中排在所有可打印字符之后的最小不可见字符，因此 `"account:Alice"` 扩展后的开区间端点 `"account:Alice\x00"` 能够精确匹配所有以 `"account:Alice"` 开头但不超出的 key，在字典序比较中行为正确。

### 2.4 图的构建过程

`buildConflictGraph`（`orderer/bench/graph.go:25`）使用 O(n²) 枚举：

```go
for i := 0; i < len(intents); i++ {
    for j := i + 1; j < len(intents); j++ {
        if txsConflict(intents[i], intents[j]) {
            // 无向图：双向添加边
            graph.Edges[a] = append(graph.Edges[a], b)
            graph.Edges[b] = append(graph.Edges[b], a)
        }
    }
}
```

**示例**：一个 Block 中有 4 笔交易：

```
tx1: DepositChecking("Alice")    → W={account:Alice}
tx2: WriteCheck("Alice")         → W={account:Alice}  ← 与 tx1 WW 冲突
tx3: SendPayment("Bob", "Carol") → W={account:Bob, account:Carol}
tx4: Balance("Alice")            → R={account:Alice}  ← 与 tx1/tx2 WR 冲突
```

冲突图边集：
```
tx1 — tx2  (WW: 都写 account:Alice)
tx1 — tx4  (WR: tx1 写 Alice，tx4 读 Alice)
tx2 — tx4  (WR: tx2 写 Alice，tx4 读 Alice)
```
tx3 与其余任何 tx 无冲突（键空间完全不同）。

---

## 3. 执行计划的存储与传递

### 3.1 执行计划字段

`TxIntent`（`core/bench/types.go:42`）中的 `ReplayPlan` 字段记录提交阶段重放所需的全部信息：

```go
type ReplayOp struct {
    Benchmark BenchmarkType  // "smallbank" 或 "ycsb"
    Function  string          // 原始函数名，如 "SendPayment"
    Args      []string        // 原始参数，如 ["Alice", "Bob", "100"]
}
```

在 `ExtractSmallBankIntent` 中，`ReplayPlan` 与 `ReadKeys`/`WriteKeys` 同时设置：

```go
intent.ReplayPlan = bench.ReplayOp{
    Benchmark: bench.BenchSmallBank,
    Function:  funcName,
    Args:      args,
}
```

**设计考量**：`ReplayPlan` 直接保存原始函数名和参数，而非预计算的写集。这意味着提交阶段的重放引擎（`replay_smallbank.go`）会基于提交时的实际 stateDB 状态**重新执行业务逻辑**，而不是盲目应用 orderer 侧预先计算的结果。这保证了在多批次顺序执行时，后续批次能读取到前序批次的最新写入（read-your-own-writes 语义）。

### 3.2 BatchSchedule 的 JSON 结构

`BatchSchedule`（`core/bench/types.go:62`）序列化为 JSON 后嵌入 `BlockMetadata`：

```json
{
  "block_number": 42,
  "version": "v1",
  "batches": [
    {"batch_id": 0, "tx_ids": ["tx1", "tx2", "tx4"]},
    {"batch_id": 1, "tx_ids": ["tx3"]}
  ]
}
```

`BatchSchedule` 中**不包含** `ReplayPlan`——提交阶段通过 txID 在原始 `block.Data.Data` 中查找 Envelope，再从 Envelope 重新提取函数调用信息（`extractInvocationFromEnvBytes`）。这避免了 BatchSchedule 体积随参数长度线性增长。

### 3.3 存储路径：BlockMetadata[ORDERER]

orderer 侧（`orderer/bench/analyzer.go:18`）将 JSON 编码的 BatchSchedule 返回给 `solo/consensus.go`：

```go
batchScheduleBytes := obench.AnalyzeBatchAndSchedule(block)
ch.support.WriteBlock(block, committers[i], batchScheduleBytes)
```

`WriteBlock` 的第三个参数 `encodedMetadataValue []byte` 最终写入：

```
block.Metadata.Metadata[BlockMetadataIndex_ORDERER]  // 索引 = 3
```

peer 侧（`core/bench/types.go:80`）在 `Commit()` 入口解析：

```go
func ParseBatchSchedule(block *cb.Block) (*BatchSchedule, bool) {
    raw := block.Metadata.Metadata[cb.BlockMetadataIndex_ORDERER]
    ordererMeta := &cb.Metadata{}
    proto.Unmarshal(raw, ordererMeta)
    // ordererMeta.Value 是 JSON 字节
    json.Unmarshal(ordererMeta.Value, &schedule)
    if schedule.Version != "v1" { return nil, false }
    return &schedule, true
}
```

**两层封装**：外层是 proto `Metadata` 消息（Fabric 原有结构），内层 `Value` 字段存放 JSON 字节。`Version: "v1"` 作为防御性版本门控，确保未来协议升级时旧 peer 能够安全降级。

---

## 4. 确定性多批次排序的生成

### 4.1 问题背景

Kafka/Raft 集群中，多个 orderer 节点对同一 Block 内容达成共识后，**各自独立**执行 `AnalyzeBatchAndSchedule`。要使所有节点生成完全相同的 BatchSchedule，图染色算法必须是**确定性的**：对于相同的输入，总产生相同的输出。

### 4.2 贪心图染色算法

`greedyGraphColoring`（`orderer/bench/graph.go:88`）：

```
算法步骤：
1. 将所有 txID 按字典序排序（关键：消除遍历顺序依赖）
2. 按排序后的顺序，逐个为每个 txID 分配最小可用颜色（batchID）
   - 扫描该 txID 的所有已染色邻居，记录它们已用的颜色
   - 找到未被邻居使用的最小非负整数颜色
3. 输出 txID → batchID 的映射
```

**代码实现**：

```go
func greedyGraphColoring(graph ConflictGraph) ColorAssignment {
    sorted := make([]string, len(graph.Nodes))
    copy(sorted, graph.Nodes)
    sort.Strings(sorted)          // 字典序排序 → 确定性

    color := make(map[string]int)
    maxColor := 0

    for _, txID := range sorted {
        usedColors := make(map[int]bool)
        for _, neighbor := range graph.Edges[txID] {
            if c, ok := color[neighbor]; ok {
                usedColors[c] = true
            }
        }
        c := 0
        for usedColors[c] { c++ }  // 找最小可用颜色
        color[txID] = c
        if c > maxColor { maxColor = c }
    }
    ...
}
```

**为什么字典序排序能保证确定性**：
- txID 由 Fabric 在背书阶段生成（基于随机 nonce 的 SHA256），不同节点对同一 tx 的 txID 完全相同
- 字典序是全序关系，排序结果唯一
- 贪心算法在固定遍历顺序下输出唯一确定

### 4.3 批次调度的构建

`buildBatchSchedule`（`orderer/bench/analyzer.go:40`）将染色结果转为有序批次列表：

```go
// 按颜色分组
batches := make(map[int][]string)
for _, intent := range intents {
    batchID := coloring.TxToColor[intent.TxID]
    batches[batchID] = append(batches[batchID], intent.TxID)
}

// 按 batchID 从小到大输出，确保批次顺序固定
for i := 0; i < coloring.NumColors; i++ {
    entries = append(entries, bench.BatchEntry{BatchID: i, TxIDs: batches[i]})
}
```

**颜色（batchID）的语义**：颜色 0 的所有 tx 互不冲突，可以在"第一轮"串行执行；颜色 1 的 tx 互不冲突，在第一轮完成后执行，此时可以安全读取颜色 0 的写入；以此类推。

### 4.4 完整执行流示例

以包含 4 笔 SmallBank tx 的 Block 为例：

```
Block 42 数据：
  tx1: DepositChecking("Alice", 100)
  tx2: WriteCheck("Alice", 50)
  tx3: SendPayment("Bob", "Carol", 200)
  tx4: Balance("Alice")
```

**orderer 侧处理**（`AnalyzeBatchAndSchedule`）：

```
Step 1 - 提取意图:
  tx1: R=[account:Alice] W=[account:Alice]
  tx2: R=[account:Alice] W=[account:Alice]
  tx3: R=[account:Bob,account:Carol] W=[account:Bob,account:Carol]
  tx4: R=[account:Alice] W=[]

Step 2 - 构建冲突图:
  tx1 ↔ tx2 (WW: 都写 account:Alice)
  tx1 ↔ tx4 (WR: tx1 写，tx4 读 account:Alice)
  tx2 ↔ tx4 (WR: tx2 写，tx4 读 account:Alice)

Step 3 - 字典序排序: [tx1, tx2, tx3, tx4]

Step 4 - 贪心染色:
  tx1: 邻居无已染色 → 颜色 0
  tx2: 邻居 tx1=0，usedColors={0} → 颜色 1
  tx3: 无邻居 → 颜色 0
  tx4: 邻居 tx1=0, tx2=1，usedColors={0,1} → 颜色 2

Step 5 - 生成 BatchSchedule:
  batch_id=0: [tx1, tx3]
  batch_id=1: [tx2]
  batch_id=2: [tx4]
```

**peer 侧处理**（`commitWithBatchSchedule`）：

```
Step 1 - MVCC 验证（tx1/tx2 互相冲突，MVCC 标记其中之一为 INVALID）
Step 2 - 存储 Block
Step 3 - 提交 MVCC 写集（非 benchmark tx；benchmark tx 结果将被重放覆盖）
Step 4 - 按批次重放（StateDBAdapter 提供 read-your-own-writes）:
  Batch 0: 重放 tx1（DepositChecking Alice），重放 tx3（SendPayment Bob→Carol）
  Batch 1: 重放 tx2（WriteCheck Alice，读到 Batch 0 写入的 Alice 新余额）
  Batch 2: 重放 tx4（Balance Alice，只读，返回空写集）
  Flush: 一次性写入 stateDB
Step 5 - History DB 提交
```

### 4.5 批次数量的理论上界

贪心图染色的颜色数不超过图的最大度数 + 1（Welsh-Powell 定理）。对于 SmallBank：

- `Amalgamate` 和 `SendPayment` 涉及 2 个账户，最多与操作同一账户的 (n-1) 笔 tx 冲突
- 在随机负载下，账户总数 >> block 内 tx 数，冲突较稀疏，批次数通常为 1-3
- 最坏情况：block 内所有 tx 操作同一个账户，批次数 = tx 数（退化为串行）

---

## 5. 关键设计总结

| 环节 | 核心函数 | 关键文件 |
|------|----------|----------|
| 读写键提取 | `ExtractSmallBankIntent` | `orderer/bench/extractor_smallbank.go` |
| 超立方体构建 | `buildPointHypercube` | `orderer/bench/graph.go` |
| 冲突检测 | `txsConflict` + `intersects` | `orderer/bench/graph.go` |
| 冲突图 | `buildConflictGraph` | `orderer/bench/graph.go` |
| 确定性染色 | `greedyGraphColoring` | `orderer/bench/graph.go` |
| BatchSchedule 生成 | `buildBatchSchedule` | `orderer/bench/analyzer.go` |
| 元数据传递 | `WriteBlock` / `ParseBatchSchedule` | `orderer/solo/consensus.go` / `core/bench/types.go` |
| 提交路径分流 | `Commit` / `commitWithBatchSchedule` | `core/ledger/kvledger/kv_ledger.go` |
| 状态适配器 | `StateDBAdapter` | `core/bench/statedb_adapter.go` |
| SmallBank 重放 | `replaySmallBank` | `core/bench/replay_smallbank.go` |

**核心不变量**：
1. 同一批次内的 tx 读写键不相交，批次内任意顺序重放结果相同
2. 批次间按 batchID 升序执行，保证跨批次数据依赖正确
3. 所有 orderer 节点对同一 block 内容必然产生相同 BatchSchedule（字典序排序保证）
4. 非 benchmark tx 完全走原有 MVCC 路径，不受影响
