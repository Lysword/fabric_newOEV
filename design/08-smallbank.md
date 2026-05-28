# 08 — SmallBank 完整实现方案

> 上级目录：[00-index.md](00-index.md) | 前置：[07-conflict-detection.md](07-conflict-detection.md)

---

## 8.1 Key 格式分析

```
账户 key：  "account:<accountID>"
回执 key：  "receipt:<requestID>"
```

所有业务操作仅涉及 `account:` 命名空间的 key，回执 key 仅在提供 requestID 时写入。

---

## 8.2 各操作读写分析

| 操作 | 参数（位置） | Read Keys | Write Keys | 依赖当前状态 | 可预计算写值 |
|------|------------|-----------|------------|------------|------------|
| `Balance` | accountID | `account:A` | ∅ | 否 | - |
| `CreateAccount` | A,name,checking,savings | `account:A`（存在检查）| `account:A` | 否 | **是**（直接用参数） |
| `DepositChecking` | A, amount | `account:A` | `account:A` | 是（checking += amount）| **否** |
| `TransactSavings` | A, amount | `account:A` | `account:A` | 是（savings += amount，需判断余额）| **否** |
| `WriteCheck` | A, amount | `account:A` | `account:A` | 是（checking -= amount，有条件）| **否** |
| `Amalgamate` | src, dest | `account:src`, `account:dest` | `account:src`, `account:dest` | 是（src全清→dest）| **否** |
| `SendPayment` | from, to, amount | `account:from`, `account:to` | `account:from`, `account:to` | 是（from.checking -= amount，有余额检查）| **否** |

**结论**：除 `CreateAccount` 外，所有写操作均依赖当前状态，无法预计算写值。**提交阶段必须重放操作**。

---

## 8.3 SmallBank Intent 提取

**新建文件**：`orderer/bench/extractor_smallbank.go`

```go
package bench

import "strings"

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
        if len(args) < 1 { return nil }
        intent.ReadKeys = []string{sbAccountKey(args[0])}
        intent.WriteKeys = nil
    case "CreateAccount":
        if len(args) < 2 { return nil }
        k := sbAccountKey(args[0])
        intent.ReadKeys = []string{k}
        intent.WriteKeys = []string{k}
    case "DepositChecking", "TransactSavings", "WriteCheck":
        if len(args) < 1 { return nil }
        k := sbAccountKey(args[0])
        intent.ReadKeys = []string{k}
        intent.WriteKeys = []string{k}
    case "Amalgamate":
        if len(args) < 2 { return nil }
        srcKey := sbAccountKey(args[0])
        dstKey := sbAccountKey(args[1])
        intent.ReadKeys = []string{srcKey, dstKey}
        intent.WriteKeys = []string{srcKey, dstKey}
    case "SendPayment":
        if len(args) < 2 { return nil }
        fromKey := sbAccountKey(args[0])
        toKey := sbAccountKey(args[1])
        intent.ReadKeys = []string{fromKey, toKey}
        intent.WriteKeys = []string{fromKey, toKey}
    default:
        return nil  // Ping, GetReceipt 等只读或管理操作，不参与 intent 分析
    }

    intent.Hypercube = buildPointHypercube(intent.ReadKeys, intent.WriteKeys)
    return intent
}

func sbAccountKey(accountID string) string {
    return "account:" + strings.TrimSpace(accountID)
}
```

---

## 8.4 SmallBank 重放引擎

**新建文件**：`core/bench/replay_smallbank.go`

```go
package bench

import (
    "encoding/json"
    "fmt"
    "strconv"
    "strings"
)

type sbAccountRecord struct {
    AccountID string `json:"account_id"`
    Name      string `json:"name"`
    Checking  int64  `json:"checking"`
    Savings   int64  `json:"savings"`
}

// replaySmallBank 在当前 stateDB 上重放 SmallBank 操作，返回写集
func replaySmallBank(db StateDBInterface, funcName string, args []string) (map[string][]byte, error) {
    switch funcName {
    case "DepositChecking":
        return sbReplayDepositChecking(db, args)
    case "TransactSavings":
        return sbReplayTransactSavings(db, args)
    case "WriteCheck":
        return sbReplayWriteCheck(db, args)
    case "Amalgamate":
        return sbReplayAmalgamate(db, args)
    case "SendPayment":
        return sbReplaySendPayment(db, args)
    case "CreateAccount":
        return sbReplayCreateAccount(db, args)
    case "Balance":
        return map[string][]byte{}, nil  // 纯读，无写
    default:
        return nil, fmt.Errorf("unsupported smallbank function: %s", funcName)
    }
}

func sbGetAccount(db StateDBInterface, accountID string) (sbAccountRecord, error) {
    data, err := db.GetState("smallbank", sbKey(accountID))
    if err != nil { return sbAccountRecord{}, err }
    if data == nil { return sbAccountRecord{}, fmt.Errorf("account not found: %s", accountID) }
    var acc sbAccountRecord
    if err := json.Unmarshal(data, &acc); err != nil { return sbAccountRecord{}, err }
    return acc, nil
}

func sbMarshalAccount(acc sbAccountRecord) ([]byte, error) {
    return json.Marshal(acc)
}

func sbKey(accountID string) string {
    return "account:" + strings.TrimSpace(accountID)
}

func sbReplayDepositChecking(db StateDBInterface, args []string) (map[string][]byte, error) {
    accountID := args[0]
    amount, err := strconv.ParseInt(args[1], 10, 64)
    if err != nil { return nil, err }

    acc, err := sbGetAccount(db, accountID)
    if err != nil { return nil, err }
    acc.Checking += amount
    value, err := sbMarshalAccount(acc)
    if err != nil { return nil, err }
    return map[string][]byte{sbKey(accountID): value}, nil
}

func sbReplayTransactSavings(db StateDBInterface, args []string) (map[string][]byte, error) {
    accountID := args[0]
    amount, err := strconv.ParseInt(args[1], 10, 64)
    if err != nil { return nil, err }

    acc, err := sbGetAccount(db, accountID)
    if err != nil { return nil, err }
    if acc.Savings+amount < 0 {
        return nil, fmt.Errorf("insufficient funds in savings")
    }
    acc.Savings += amount
    value, err := sbMarshalAccount(acc)
    if err != nil { return nil, err }
    return map[string][]byte{sbKey(accountID): value}, nil
}

func sbReplayWriteCheck(db StateDBInterface, args []string) (map[string][]byte, error) {
    accountID := args[0]
    amount, err := strconv.ParseInt(args[1], 10, 64)
    if err != nil { return nil, err }

    acc, err := sbGetAccount(db, accountID)
    if err != nil { return nil, err }
    if acc.Checking+acc.Savings < amount {
        acc.Checking -= amount + 1  // penalty
    } else {
        acc.Checking -= amount
    }
    value, err := sbMarshalAccount(acc)
    if err != nil { return nil, err }
    return map[string][]byte{sbKey(accountID): value}, nil
}

func sbReplayAmalgamate(db StateDBInterface, args []string) (map[string][]byte, error) {
    srcID, dstID := args[0], args[1]
    src, err := sbGetAccount(db, srcID)
    if err != nil { return nil, err }
    dst, err := sbGetAccount(db, dstID)
    if err != nil { return nil, err }

    transfer := src.Checking + src.Savings
    src.Checking = 0
    src.Savings = 0
    dst.Checking += transfer

    srcBytes, err := sbMarshalAccount(src)
    if err != nil { return nil, err }
    dstBytes, err := sbMarshalAccount(dst)
    if err != nil { return nil, err }
    return map[string][]byte{sbKey(srcID): srcBytes, sbKey(dstID): dstBytes}, nil
}

func sbReplaySendPayment(db StateDBInterface, args []string) (map[string][]byte, error) {
    fromID, toID := args[0], args[1]
    amount, err := strconv.ParseInt(args[2], 10, 64)
    if err != nil { return nil, err }

    from, err := sbGetAccount(db, fromID)
    if err != nil { return nil, err }
    to, err := sbGetAccount(db, toID)
    if err != nil { return nil, err }
    if from.Checking < amount {
        return nil, fmt.Errorf("insufficient funds in checking")
    }
    from.Checking -= amount
    to.Checking += amount

    fromBytes, err := sbMarshalAccount(from)
    if err != nil { return nil, err }
    toBytes, err := sbMarshalAccount(to)
    if err != nil { return nil, err }
    return map[string][]byte{sbKey(fromID): fromBytes, sbKey(toID): toBytes}, nil
}

func sbReplayCreateAccount(db StateDBInterface, args []string) (map[string][]byte, error) {
    accountID := args[0]
    name := args[1]
    checking, err := strconv.ParseInt(args[2], 10, 64)
    if err != nil { return nil, err }
    savings, err := strconv.ParseInt(args[3], 10, 64)
    if err != nil { return nil, err }

    acc := sbAccountRecord{AccountID: accountID, Name: name, Checking: checking, Savings: savings}
    value, err := sbMarshalAccount(acc)
    if err != nil { return nil, err }
    return map[string][]byte{sbKey(accountID): value}, nil
}
```

---

## 8.5 SmallBank 冲突规则

由于所有 key 都是精确点（无范围），冲突检测退化为：

**两笔 tx 涉及同一账户 key，且至少一笔有写操作 → 冲突**

| 操作对 | 冲突？ |
|-------|-------|
| DepositChecking(A) vs DepositChecking(A) | ✓（写-写，同一账户） |
| SendPayment(A→B) vs SendPayment(A→C) | ✓（写 A，读/写 A） |
| SendPayment(A→B) vs Amalgamate(B, C) | ✓（写 B，读/写 B） |
| Balance(A) vs Balance(A) | ✗（读-读） |
| DepositChecking(A) vs DepositChecking(B) | ✗（不同账户） |

---

*返回：[目录](00-index.md) | 前一篇：[07-conflict-detection.md](07-conflict-detection.md) | 下一篇：[09-ycsb.md](09-ycsb.md)*
