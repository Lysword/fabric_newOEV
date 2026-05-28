# 09 — YCSB 简化实现方案

> 上级目录：[00-index.md](00-index.md) | 前置：[08-smallbank.md](08-smallbank.md)

---

## 9.1 Key 格式分析

```
数据 key：  任意字符串（如 "user000001"）
回执 key：  "receipt:<requestID>"
```

YCSB 操作直接用客户端传入的 key 字符串，不需要 `account:` 前缀转换。

---

## 9.2 各操作读写分析

| 操作 | 参数 | Read Keys | Write Keys | 写值依赖状态 | 可预计算写值 |
|------|------|-----------|------------|------------|------------|
| `Read` | key | key | ∅ | 否 | - |
| `Insert` | key, value | key（存在检查）| key | 否 | **是**（value 直接在参数中）|
| `Update` | key, value | key（存在检查）| key | 否 | **是**（value 直接在参数中）|
| `Scan` | startKey, limit | [startKey, MaxRune) 范围 | ∅ | 否 | - |

**YCSB 的优势**：`Update`/`Insert` 的**写值直接由参数提供**，提交阶段可以直接写入，无需读取当前状态。

---

## 9.3 YCSB Intent 提取

**新建文件**：`orderer/bench/extractor_ycsb.go`

```go
package bench

import "unicode/utf8"

// ExtractYCSBIntent 从 funcName + args 提取 YCSB 交易的读写意图
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
        intent.ReadKeys = []string{key}  // 存在性检查
        intent.WriteKeys = []string{key}
        intent.Hypercube = buildPointHypercube(intent.ReadKeys, intent.WriteKeys)

    case "Scan":
        if len(args) < 1 { return nil }
        startKey := args[0]
        endKey := string(utf8.MaxRune)
        intent.ReadKeys = nil   // 范围读，用 ReadIntervals 表示
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

---

## 9.4 YCSB 重放引擎

**新建文件**：`core/bench/replay_ycsb.go`

```go
package bench

import (
    "encoding/json"
    "fmt"
)

type ycsbRecord struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}

// replayYCSB 在当前 stateDB 上重放 YCSB 操作，返回写集
func replayYCSB(db StateDBInterface, funcName string, args []string) (map[string][]byte, error) {
    switch funcName {
    case "Insert", "Update":
        if len(args) < 2 {
            return nil, fmt.Errorf("insert/update requires key and value")
        }
        key := args[0]
        value := args[1]
        record, err := json.Marshal(ycsbRecord{Key: key, Value: value})
        if err != nil { return nil, err }
        return map[string][]byte{key: record}, nil

    case "Read":
        return map[string][]byte{}, nil  // 纯读，无写

    case "Scan":
        return map[string][]byte{}, nil  // 纯读，无写

    default:
        return nil, fmt.Errorf("unsupported ycsb function: %s", funcName)
    }
}
```

> **注意**：YCSB 合约中的 `ycsbRecord` 结构和 value 的序列化格式需要与原始合约保持完全一致，否则会造成数据格式不兼容。编码时需要对照 `ycsb.go` 中实际的 `PutState` 调用确认格式。

---

## 9.5 YCSB Scan 冲突检测规则

Scan 操作读取 `[startKey, utf8.MaxRune)` 范围内的所有 key：

| 操作对 | 冲突？ | 原因 |
|-------|-------|------|
| `Scan(A)` vs `Update(K)` (K 在 A 范围内) | ✓ | 写操作可能改变 Scan 的结果 |
| `Scan(A)` vs `Scan(B)` | ✗ | 读-读不冲突 |
| `Update(K)` vs `Update(K)` | ✓ | 写-写同一 key |
| `Insert(K)` vs `Scan(A)` (K 在 A 范围内) | ✓ | 新插入的 key 会出现在 Scan 结果中 |

**范围与点的相交判断**：
```
Scan [startKey, MaxRune) 与 Insert/Update key K 相交：
  条件：startKey <= K < MaxRune  （即 K >= startKey）
```

实现见 [07-conflict-detection.md](07-conflict-detection.md) 中的 `intersects()` 函数。

---

## 9.6 实现优先级

| 操作 | Phase | 说明 |
|------|-------|------|
| `Read` | Phase 2 | 纯读，重放无写，最简单 |
| `Update` | Phase 2 | 直接写，值在参数中 |
| `Insert` | Phase 2 | 直接写，值在参数中 |
| `Scan` | Phase 2 | 纯读，但 intent 需要范围区间，影响冲突图 |

**建议**：先完整支持 `Read`/`Update`/`Insert`，`Scan` 的范围冲突检测在 `graph.go` 的 `intersects()` 添加范围支持后自动生效。

---

*返回：[目录](00-index.md) | 前一篇：[08-smallbank.md](08-smallbank.md) | 下一篇：[10-risks-phases.md](10-risks-phases.md)*
