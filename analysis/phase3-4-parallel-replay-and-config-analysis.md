# Phase 3+4：同 Batch 并行重放 + 配置开关与兼容性分析

本文档从源码层面剖析 Phase 3 和 Phase 4 的完整工作：batch 内 goroutine 并行重放、性能统计、配置开关和兼容性保障。

---

## 1. Phase 3：同 Batch 并行重放

### 1.1 并行化的理论基础

orderer 阶段的超立方体冲突检测 + 贪心图着色（`orderer/bench/graph.go`）将冲突 tx 分配到不同 batch，保证同一 batch 内的 tx 两两无 key 冲突：

```
冲突规则：tx A 与 tx B 冲突 ⟺
  WA ∩ RB ≠ ∅ ∨ RA ∩ WB ≠ ∅ ∨ WA ∩ WB ≠ ∅

图着色保证：同 batch 内任意两个 tx 之间无边 → 无冲突
  → 同 batch 内 tx 读写的 key 集合两两无交集
  → 可安全并行执行，不存在数据竞争
```

这意味着同 batch 内的 tx：
- 不会读写相同的 key（无 RW/WR/WW 冲突）
- 各自操作独立的 key 子集
- 并行执行的结果与任意串行顺序等价

### 1.2 并行重放架构

改造前（Phase 2）的执行模型：

```
batch 0:  tx0 → tx1 → tx2          （串行）
batch 1:  tx3 → tx4                 （串行）
batch 2:  tx5 → tx6 → tx7 → tx8    （串行）
```

改造后（Phase 3）的执行模型：

```
batch 0:  tx0 ─┐
          tx1 ─┤ 并行 → 合并写入
          tx2 ─┘
batch 1:  tx3 ─┐
          tx4 ─┘ 并行 → 合并写入
batch 2:  tx5 ─┐
          tx6 ─┤ 并行 → 合并写入
          tx7 ─┤
          tx8 ─┘
```

关键约束：**batch 间仍然严格串行**。不同 batch 之间可能存在冲突（图着色将冲突 tx 分到不同 batch），必须按 batch 顺序执行以保证因果一致性。

### 1.3 核心实现：`CommitBenchmarkTxs`

实现文件：`core/bench/commit.go:36`

```go
func CommitBenchmarkTxs(block *cb.Block, schedule *BatchSchedule, db statedb.VersionedDB) error
```

**分支策略**：按 batch 内 tx 数量选择执行路径。

| 条件 | 执行路径 | 原因 |
|------|---------|------|
| `txCount <= 1` | 串行（`replaySingleTx`） | 单 tx 无并行收益，避免 goroutine 创建开销 |
| `txCount > 1` | 并行（goroutine + WaitGroup） | 图着色保证无冲突，并行安全 |

### 1.4 并行重放的并发安全性分析

并行阶段涉及两类共享数据的访问：

#### (1) `StateDBAdapter` 的并发读

每个 goroutine 调用 `replayTx(adapter, ccName, funcName, strArgs)`，内部通过 `adapter.GetState()` 读取状态。

`StateDBAdapter.GetState()` 实现（`core/bench/statedb_adapter.go:29`）：

```go
func (a *StateDBAdapter) GetState(namespace, key string) ([]byte, error) {
    if vv := a.batch.Get(namespace, key); vv != nil {
        return vv.Value, nil           // ① 查本地写缓存
    }
    vv, err := a.db.GetState(namespace, key)  // ② 查底层 DB
    ...
}
```

- **路径 ①**：`batch.Get()` 读取 `map[string]*nsUpdates`，是只读访问。同 batch 内各 tx 操作不同 key，不会有 goroutine 在写同一 batch entry，故并发读安全。
- **路径 ②**：`db.GetState()` 是底层 LevelDB/CouchDB 的读操作，LevelDB 的 `Get` 本身是线程安全的（内部有 read lock）。

#### (2) 写入隔离

并行阶段 **不直接调用 `adapter.PutState()`**。每个 goroutine 返回独立的 `txReplayResult`：

```go
type txReplayResult struct {
    txID   string
    ccName string
    writes map[string][]byte  // 独立的写缓冲，不共享
    err    error
}
```

写入合并在 `wg.Wait()` 之后的串行阶段完成：

```go
wg.Wait()

// 串行合并所有写入（保证确定性）
for _, r := range results {
    if r.err != nil {
        logger.Warningf("tx %s: parallel replay failed: %s", r.txID, r.err)
        continue
    }
    for key, value := range r.writes {
        adapter.PutState(r.ccName, key, value)
    }
}
```

由于同 batch 内的 tx 写不同 key（图着色保证），合并顺序不影响最终结果。这里按 `results` 数组顺序（即 `batchEntry.TxIDs` 的原始顺序）合并，保证确定性。

#### (3) `findTxInBlock` 的并发安全性

多个 goroutine 并发调用 `findTxInBlock(block, txID)`，遍历 `block.Data.Data` 查找目标 tx。这是纯只读操作（遍历 slice + proto 反序列化），不修改 block 数据，并发安全。

### 1.5 串行路径优化

对 `txCount <= 1` 的 batch，使用 `replaySingleTx` 直接串行处理：

```go
func replaySingleTx(adapter *StateDBAdapter, block *cb.Block, txID string)
```

与并行路径的区别：
- 直接调用 `adapter.PutState()` 写入，无需中间 `txReplayResult`
- 无 `sync.WaitGroup` 和 goroutine 创建开销
- 错误仅记录日志（与 Phase 2 行为一致）

### 1.6 goroutine 闭包的变量捕获

```go
for i, txID := range batchEntry.TxIDs {
    go func(idx int, tid string) {
        defer wg.Done()
        results[idx] = replayTxForParallel(adapter, block, tid)
    }(i, txID)
}
```

通过参数传递 `i` 和 `txID`（值拷贝），避免了 Go 闭包中常见的循环变量捕获问题。`results[idx]` 各 goroutine 写不同下标，无竞争。

---

## 2. Phase 3：性能统计

### 2.1 统计指标

在 `CommitBenchmarkTxs` 中增加了三级统计：

| 级别 | 指标 | 输出时机 |
|------|------|---------|
| 总体 | block 编号、benchmark tx 总数、batch 数量 | 函数入口 |
| 每 batch | batch 序号、tx 数量、耗时 | 每个 batch 完成后 |
| 汇总 | 总 replay 耗时、flush 耗时 | 函数退出前 |

### 2.2 日志输出示例

```
[bench/commit] Block [42]: starting benchmark replay — 100 tx in 3 batches
[bench/commit] Block [42]: batch 1/3 completed — 45 tx in 12.3ms
[bench/commit] Block [42]: batch 2/3 completed — 35 tx in 9.1ms
[bench/commit] Block [42]: batch 3/3 completed — 20 tx in 5.7ms
[bench/commit] Block [42]: benchmark replay done — 100 tx, 3 batches, replay 27.1ms, flush 3.2ms
```

### 2.3 性能预期

| 场景 | Phase 2（串行） | Phase 3（并行） | 预期加速比 |
|------|----------------|----------------|-----------|
| 单 batch 多 tx | 逐一串行 | goroutine 并行 | 接近 min(txCount, CPU核数) |
| 多 batch 少 tx | 逐 batch 串行 | 各 batch 内并行 | 1.x（并行收益有限） |
| 所有 tx 互相冲突 | N 个 batch 各 1 tx | 退化为串行 | 1.0（无退化） |
| 无冲突 | 1 batch N tx | 全部并行 | 最大加速 |

**注意**：实际加速受多因素影响：CPU 核数、LevelDB 读延迟、JSON 序列化/反序列化开销、goroutine 调度开销。高冲突场景下 batch 数量多但每 batch tx 少，并行收益递减。

---

## 3. Phase 4：配置开关

### 3.1 配置项设计

| 配置 key | 类型 | 默认值 | 作用 |
|---------|------|-------|------|
| `ledger.bench.enableReplay` | bool | `true` | 控制是否启用超立方体冲突检测 + batch 重放路径 |

遵循 Fabric 现有配置命名惯例，与 `ledger.history.enableHistoryDatabase` 同级。

### 3.2 配置读取

实现文件：`core/ledger/ledgerconfig/ledger_config.go`

```go
func IsBenchReplayEnabled() bool {
    if !viper.IsSet("ledger.bench.enableReplay") {
        return true  // 未配置时默认启用
    }
    return viper.GetBool("ledger.bench.enableReplay")
}
```

使用 `viper.IsSet` 先检查配置是否存在，未设置时默认返回 `true`。这避免了 viper 对未设置的 bool key 返回 `false` 的默认行为，确保不配置等同于启用。

### 3.3 开关生效位置

实现文件：`core/ledger/kvledger/kv_ledger.go:222`

```go
func (l *kvLedger) Commit(block *common.Block) error {
    // ...
    if ledgerconfig.IsBenchReplayEnabled() {
        schedule, hasBatchSchedule := bench.ParseBatchSchedule(block)
        if hasBatchSchedule {
            return l.commitWithBatchSchedule(block, schedule)
        }
    }
    // 原 MVCC 路径...
}
```

开关检查在 `ParseBatchSchedule` 之前，关闭时连序列化开销都省去。

### 3.4 core.yaml 配置段

实现文件：`sampleconfig/core.yaml`

```yaml
ledger:
  # ... state, history ...

  bench:
    enableReplay: true
```

放在 `ledger.history` 之后，与现有配置结构一致。

### 3.5 开关使用场景

| 场景 | enableReplay | 行为 |
|------|-------------|------|
| 正常 benchmark 运行 | `true` | 超立方体冲突检测 + batch 重放 |
| 调试/对比测试 | `false` | 所有 tx 走原 MVCC 路径（可用于对比性能） |
| 生产环境非 benchmark | `true` | 无影响（非 benchmark tx 无 BatchSchedule） |
| orderer 有 BatchSchedule 但 peer 关闭 | `false` | peer 忽略 BatchSchedule，走 MVCC（benchmark tx 大概率被 MVCC 标记 INVALID） |

---

## 4. Phase 4：兼容性保障

### 4.1 系统链码保护

系统链码（cscc/lscc/escc/vscc/qscc）始终不受影响，保护机制在 orderer 端精确匹配：

```go
// orderer/bench/extractor.go
func IsBenchmarkChaincode(name string) bool {
    return name == "smallbank" || name == "ycsb"
}
```

仅 `"smallbank"` 和 `"ycsb"` 精确匹配时才提取意图。系统链码名称不可能匹配，因此：
- 系统链码 tx 不会出现在 BatchSchedule 中
- 系统链码 tx 始终走 MVCC 路径
- 系统链码 tx 不受 `enableReplay` 开关影响

### 4.2 混合 Block 处理

当一个 block 同时包含 benchmark tx 和非 benchmark tx 时：

```
Block [N]:
  tx0: smallbank.DepositChecking  → BatchSchedule batch 0
  tx1: smallbank.SendPayment      → BatchSchedule batch 0
  tx2: lscc.deploy                → 非 benchmark，走 MVCC
  tx3: smallbank.Amalgamate       → BatchSchedule batch 1
  tx4: mycc.invoke                → 非 benchmark，走 MVCC
```

`commitWithBatchSchedule` 的处理流程：

1. **Step 1-2**：benchmark tx（tx0/1/3）预标记 `NOT_VALIDATED`
2. **Step 3**：`ValidateAndPrepare` 仅验证 tx2/tx4 的 MVCC 读写集
3. **Step 4**：benchmark tx 标记回 `VALID`
4. **Step 5**：`AddBlock` — 所有 tx 的 txFilter 已确定
5. **Step 6**：`CommitBenchmarkTxs` — 仅重放 tx0/1/3
6. **Step 7**：`txtmgmt.Commit()` — 仅提交 tx2/tx4 的写集

新增的统计日志明确输出两类 tx 数量：

```go
logger.Infof("Channel [%s]: Block [%d] tx breakdown — %d benchmark, %d non-benchmark, %d total",
    l.ledgerID, blockNo, len(benchTxIDs), nonBenchCount, len(block.Data.Data))
```

示例输出：
```
Channel [mychannel]: Block [42] tx breakdown — 3 benchmark, 2 non-benchmark, 5 total
```

### 4.3 两次 ApplyUpdates 的互不干扰

`commitWithBatchSchedule` 中存在两次 stateDB 写入：

| 顺序 | 来源 | 写入方式 | 写入内容 |
|------|------|---------|---------|
| Step 6 | `CommitBenchmarkTxs` | `StateDBAdapter.Flush()` → `db.ApplyUpdates(batch, height)` | benchmark tx 的重放结果 |
| Step 7 | `txtmgmt.Commit()` | 内部 `db.ApplyUpdates(updateBatch, height)` | 非 benchmark tx 的 endorsed 写集 |

两次写入操作不同 namespace 的 key：
- benchmark 写集操作 `smallbank` 或 `ycsb` namespace
- 非 benchmark 写集操作其他链码 namespace

两者设置相同的 savepoint `(blockNum, numTxs-1)`，后写入覆盖前写入的 savepoint 值，但值相同，无实际影响。

---

## 5. 修改文件清单

| 文件 | 变更类型 | Phase | 说明 |
|------|---------|-------|------|
| `core/bench/commit.go` | 修改 | P3 | 并行重放 + 性能统计 |
| `core/ledger/kvledger/kv_ledger.go` | 修改 | P4 | 配置开关检查 + 兼容性统计日志 |
| `core/ledger/ledgerconfig/ledger_config.go` | 修改 | P4 | 新增 `IsBenchReplayEnabled()` |
| `sampleconfig/core.yaml` | 修改 | P4 | 新增 `bench.enableReplay` 配置段 |

**Git commit**: `299f060` on branch `main`

**变更统计**：4 files changed, 130 insertions(+), 23 deletions(-)

---

## 6. 已知局限 & 后续方向

| 编号 | 内容 | 优先级 | 说明 |
|------|------|-------|------|
| L-1 | goroutine 数量未设上限 | 低 | 当前每 batch 内 tx 数量等于 goroutine 数量。正常 batch 大小（100-1000 tx）下 goroutine 开销可控，但极端情况（单 batch 万级 tx）可能需要 worker pool |
| L-2 | `StateDBAdapter` 无并发写保护 | — | 设计上并行阶段不调用 `PutState`，合并阶段串行执行，无需加锁。但若未来改为并行写入需重新评估 |
| L-3 | 性能统计仅日志输出 | 低 | 当前通过 `logger.Infof` 输出，未接入 metrics 系统。后续可考虑 Prometheus 指标 |
| L-4 | orderer 端无配置开关 | 低 | `enableReplay=false` 仅在 peer 端生效，orderer 仍会执行冲突检测并写入 BatchSchedule。orderer 端开关需修改 `orderer/solo/consensus.go`，但 BatchSchedule 写入 metadata 对 peer 无害，优先级低 |
| L-5 | SmallBank `receipt` key 未重放 | 低 | Phase 1 遗留问题，不影响核心数据正确性 |
| L-6 | 无端到端集成测试 | 高 | 需在实际 Fabric 环境中验证并行重放的正确性和性能提升 |
