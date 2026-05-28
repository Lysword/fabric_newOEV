# Phase 2：YCSB 支持 + commitWithBatchSchedule 核心 BUG 修复分析

本文档总结 Phase 2 阶段的完整工作：YCSB benchmark 支持实现、代码校验发现的 BUG 及修复方案。

---

## 1. Phase 2 新增：YCSB 支持

### 1.1 YCSB 操作与读写意图映射

YCSB 链码支持 4 种操作，意图提取规则如下：

| 操作 | 参数 | ReadKeys | WriteKeys | 超立方体类型 |
|------|------|----------|-----------|-------------|
| Read | key | [key] | nil | 点集（读） |
| Insert | key, value | [key] | [key] | 点集（读+写） |
| Update | key, value | [key] | [key] | 点集（读+写） |
| Scan | startKey, limit | nil | nil | 范围区间 [startKey, MaxRune) |

实现文件：`orderer/bench/extractor_ycsb.go`

```go
func ExtractYCSBIntent(txID, channelID, funcName string, args []string) *bench.TxIntent
```

**关键设计决策：**

- **Insert/Update 的 ReadKeys 包含 key**：虽然链码层面 Insert 不一定需要先读，但为了冲突检测的保守正确性，将 key 同时加入读集和写集。两个 Insert 同一 key 的 tx 会被检测为冲突（WW 冲突），避免后写覆盖前写。
- **Scan 使用范围区间**：`[startKey, string(utf8.MaxRune))`，因为 YCSB 的 Scan 从 startKey 开始按字典序扫描，上界设为 Unicode 最大字符，确保冲突检测覆盖所有可能被扫描到的 key。`graph.go` 中的 `intersects()` 已支持 `IsRange=true` 的区间重叠判断，无需修改。

### 1.2 YCSB 重放引擎

实现文件：`core/bench/replay_ycsb.go`

```go
func replayYCSB(db *StateDBAdapter, funcName string, args []string) (map[string][]byte, error)
```

| 操作 | 重放行为 | 写集 |
|------|---------|------|
| Insert/Update | 规范化 value → 序列化 ycsbRecord → 写入 | `{key: json(ycsbRecord)}` |
| Read | 无写操作 | 空 map |
| Scan | 无写操作 | 空 map |

**Value 规范化逻辑**：

链码的 `normalizeValuePayload` 会尝试从 `{"value":"..."}` JSON 中提取实际 value。重放引擎的 `ycsbNormalizeValue` 完全复刻此行为：

```go
func ycsbNormalizeValue(raw string) (string, error) {
    var payload ycsbValuePayload
    if err := json.Unmarshal([]byte(raw), &payload); err != nil {
        return raw, nil  // 非 JSON 则原样返回
    }
    if payload.Value == "" {
        return raw, nil  // value 字段为空则原样返回
    }
    return payload.Value, nil
}
```

写入格式与链码完全一致：
```go
type ycsbRecord struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}
```

### 1.3 路由启用

- `orderer/bench/extractor.go`：`extractIntentFromEnvBytes` 中新增 `case BenchmarkYCSB` 分支
- `core/bench/commit.go`：`replayTx` 中新增 `case ccYCSB` 分支

---

## 2. 代码校验发现的问题

对完整执行流（orderer 提取意图 → 超立方体冲突检测 → 图着色分批 → peer 提交重放）做了端到端代码审查，发现以下问题：

### BUG-1（严重）：commitWithBatchSchedule 执行顺序错误

**原代码流程：**

```
ValidateAndPrepare(block)   ← 对所有 tx 做 MVCC，benchmark tx 的 endorsed 写集进入 updateBatch
txtmgmt.Commit()            ← endorsed 写集写入 stateDB
CommitBenchmarkTxs(replay)  ← 重放读到"被污染"的状态
```

**问题根因：**

orderer 的超立方体冲突检测已经保证同批次 benchmark tx 无冲突、不同批次严格有序。benchmark tx 根本不需要经过 MVCC 读写集校验。但原实现让所有 tx（包括 benchmark tx）都经过 `ValidateAndPrepare`，导致：

1. **MVCC 误判**：同 block 内多个 benchmark tx 读写相同 key，endorsed 时的版本号到提交时已过期，被标记为 `MVCC_READ_CONFLICT`
2. **状态污染**：即使 benchmark tx 通过了 MVCC，其 endorsed 写集被 `txtmgmt.Commit()` 先写入 stateDB。随后 replay 读取到的是被污染的状态，对有状态依赖的操作产生错误结果

**具体影响示例（SmallBank DepositChecking）：**

```
初始状态: account:A = {checking: 100, saving: 200}

Tx1: DepositChecking(A, 50)
  endorsed 写集: checking = 150（背书时读到 100，+50）
Tx2: DepositChecking(A, 30)
  endorsed 写集: checking = 130（背书时也读到 100，+30）

正确结果（按批次重放）: 100 + 50 + 30 = 180

原代码（BUG）:
  MVCC Commit → checking = 150（Tx1 的 endorsed 写集）
  Replay Tx1: 读到 150, 150+50 = 200 ← 错误！双重计算
  Replay Tx2: 读到 200, 200+30 = 230 ← 严重偏差
```

### BUG-2（中等）：重放错误静默忽略

```go
// 原代码
if err := bench.CommitBenchmarkTxs(...); err != nil {
    logger.Warningf(...)  // 仅打日志，不返回错误
}
```

`CommitBenchmarkTxs` 失败意味着 benchmark tx 的写入丢失，但 block 已经存储、txFilter 标记为 VALID。数据不一致且无告警。

### BUG-3（中等）：benchmark tx 的 txFilter 状态不正确

被 MVCC 标记为 `MVCC_READ_CONFLICT` 的 benchmark tx，在 `AddBlock` 存储后永远是 INVALID 状态。即使后续重放成功，`GetTxValidationCodeByTxID` 查询仍返回 INVALID。

### ISSUE-5（低）：CreateAccount 参数校验不严格

`extractor_smallbank.go` 中 `CreateAccount` 检查 `len(args) < 2`，但 SmallBank 链码需要 4 个参数（accountID, customerName, savingBalance, checkingBalance）。虽然不影响意图提取（仅用 args[0]），但放行参数不足的 tx 会导致后续重放失败。

---

## 3. 修复方案

### 3.1 commitWithBatchSchedule 重写（BUG-1/2/3）

**核心思路**：benchmark tx 既然已经过 orderer 超立方体冲突检测，就不应该再经历 MVCC 读写集校验。

**修复后流程：**

```
┌─────────────────────────────────────────────────────────────┐
│ Step 1: 构建 benchTxIDs + txIDToIndex 映射                    │
│ Step 2: 预标记 benchmark tx 为 NOT_VALIDATED                  │
│         → ValidateAndPrepareBatch 中 IsInvalid() 返回 true    │
│         → 跳过这些 tx，不解析读写集，不加入 updateBatch         │
│ Step 3: ValidateAndPrepare(block, true)                      │
│         仅验证非 benchmark tx                                 │
│ Step 4: 标记 benchmark tx 回 VALID                           │
│         orderer 超立方体冲突检测已保证正确性                     │
│ Step 5: AddBlock(block)                                      │
│         txFilter 最终状态: benchmark=VALID, 非benchmark=按MVCC │
│ Step 6: CommitBenchmarkTxs (在 txtmgmt.Commit 之前!)          │
│         读取干净的 pre-block 状态，按批次顺序重放               │
│ Step 7: txtmgmt.Commit()                                     │
│         仅写入非 benchmark tx 的写集                           │
│ Step 8: historyDB.Commit                                     │
└─────────────────────────────────────────────────────────────┘
```

**关键机制解析：**

1. **预标记跳过 MVCC**：`state_based_validator.go:92` 检查 `txsFilter.IsInvalid(txIndex)`，`NOT_VALIDATED`（值 254）!= `VALID`（值 0），因此 `IsInvalid` 返回 true，validator 跳过该 tx。benchmark tx 的 endorsed 读写集不会被解析，不会加入 updateBatch。

2. **重放在 Commit 之前**：`CommitBenchmarkTxs` 在 `txtmgmt.Commit()` 之前执行。此时 stateDB 仍是 pre-block 状态（`ValidateAndPrepare` 只构建内存 updateBatch，不写 DB），重放引擎通过 `StateDBAdapter` 读取到的是干净状态。

3. **两次 ApplyUpdates 不冲突**：benchmark 写集（通过 `StateDBAdapter.Flush()`）和非 benchmark 写集（通过 `txtmgmt.Commit()`）操作不同 namespace 的 key（benchmark chaincode vs 系统链码），不会互相覆盖。两次调用都设置相同的 savepoint `(blockNum, numTxs-1)`。

4. **错误传播**：`CommitBenchmarkTxs` 返回的 error 现在直接 `return err`，不再静默吞掉。

**新增辅助函数**：`core/bench/commit.go` 中的 `BuildTxIDIndexMap` 构建 block 内 txID → txIndex 映射，供预标记 txFilter 使用。

### 3.2 CreateAccount 参数校验修复（ISSUE-5）

`extractor_smallbank.go` 中 `len(args) < 2` → `len(args) < 4`。

---

## 4. 修改文件清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `orderer/bench/extractor_ycsb.go` | 新增 | YCSB 意图提取 |
| `core/bench/replay_ycsb.go` | 新增 | YCSB 重放引擎 |
| `orderer/bench/extractor.go` | 修改 | 启用 YCSB case 分支 |
| `core/bench/commit.go` | 修改 | 启用 YCSB 重放分支 + 新增 BuildTxIDIndexMap |
| `core/ledger/kvledger/kv_ledger.go` | 修改 | 重写 commitWithBatchSchedule + 新增 ledgerutil 导入 |
| `orderer/bench/extractor_smallbank.go` | 修改 | CreateAccount 参数校验 < 2 → < 4 |

**Git commit**: `5838c81` on branch `main`

---

## 5. 已知局限 & 后续计划

| 编号 | 内容 | 优先级 | 说明 |
|------|------|-------|------|
| L-1 | SmallBank receipt key 未重放 | 低 | `receipt:<requestID>` 在重放中未写入，影响 GetReceipt 查询。已知限制，不影响核心数据正确性 |
| L-2 | 无端到端集成测试 | 高 | 目前修复基于代码审查，需要在实际 Fabric 环境中验证完整流程 |
| L-3 | graph.go 范围区间冲突检测已就绪 | — | `intersects()` 原已支持 `IsRange=true`，Phase 2 无需修改，但 Scan 与 Write 的冲突场景需实际测试验证 |
