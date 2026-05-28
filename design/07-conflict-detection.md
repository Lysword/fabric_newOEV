# 07 — 冲突检测算法设计

> 上级目录：[00-index.md](00-index.md) | 前置：[05-orderer-changes.md](05-orderer-changes.md)

---

## 7.1 KV 场景超立方体表示

在 KV 模型中，超立方体退化为**一维 key 区间**：
- 精确 key：`[key, key]`（点区间，IsRange=false）
- 范围查询：`[startKey, endKey)`（区间，ycsb scan 使用）

对于 smallbank，账户 key 的格式为 `account:<accountID>`，每笔 tx 涉及的 key 是有限个精确点。

---

## 7.2 冲突规则

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

---

## 7.3 区间相交判断（Go 实现）

```go
// intersects 判断两个 KeyInterval 是否相交
func intersects(a, b KeyInterval) bool {
    if !a.IsRange && !b.IsRange {
        // 两个都是精确 key：直接比较
        return a.Start == b.Start
    }
    // 至少一个是范围：转化为区间 [Start, End)
    aEnd := a.End
    if !a.IsRange {
        aEnd = a.Start + "\x00"  // 精确 key 等价于 [key, key+"\x00")
    }
    bEnd := b.End
    if !b.IsRange {
        bEnd = b.Start + "\x00"
    }
    return a.Start < bEnd && b.Start < aEnd
}
```

---

## 7.4 冲突图构建（伪代码）

```
buildConflictGraph(intents []*TxIntent) -> ConflictGraph:
    graph = ConflictGraph{
        Nodes: [intent.TxID for intent in intents],
        Edges: {},
    }

    for i = 0 to len(intents)-1:
        for j = i+1 to len(intents)-1:
            txi = intents[i]
            txj = intents[j]

            conflict = false

            // 检查 txi.writes ∩ txj.reads
            for wi in txi.Hypercube.WriteIntervals:
                for rj in txj.Hypercube.ReadIntervals:
                    if intersects(wi, rj): conflict = true; break

            // 检查 txj.writes ∩ txi.reads
            for wj in txj.Hypercube.WriteIntervals:
                for ri in txi.Hypercube.ReadIntervals:
                    if intersects(wj, ri): conflict = true; break

            // 检查 writes ∩ writes
            for wi in txi.Hypercube.WriteIntervals:
                for wj in txj.Hypercube.WriteIntervals:
                    if intersects(wi, wj): conflict = true; break

            if conflict:
                graph.Edges[txi.TxID] = append(graph.Edges[txi.TxID], txj.TxID)
                graph.Edges[txj.TxID] = append(graph.Edges[txj.TxID], txi.TxID)

    return graph
```

---

## 7.5 贪心图染色（伪代码）

```
greedyGraphColoring(graph ConflictGraph) -> ColorAssignment:
    color = {}         // txID → batchID
    maxColor = 0

    // 按 txID 字典序遍历（保证所有 orderer 节点确定性输出相同结果）
    sortedNodes = sort(graph.Nodes)

    for txID in sortedNodes:
        usedColors = {color[neighbor] for neighbor in graph.Edges[txID] if neighbor in color}
        c = 0
        while c in usedColors:
            c++
        color[txID] = c
        if c > maxColor: maxColor = c

    return ColorAssignment{TxToColor: color, NumColors: maxColor+1}
```

**复杂度**：O(V+E)，对正常 batch 大小（100-1000 tx）毫秒级完成。

**确定性保证**：固定遍历顺序（txID 字典序）确保相同输入产生相同染色结果。

---

## 7.6 生成 BatchSchedule（伪代码）

```
buildBatchSchedule(blockNum uint64, intents []*TxIntent, coloring ColorAssignment) -> BatchSchedule:
    batches = {}  // batchID → []txID

    for intent in intents:
        batchID = coloring.TxToColor[intent.TxID]
        batches[batchID] = append(batches[batchID], intent.TxID)

    entries = []
    for i = 0 to coloring.NumColors-1:
        if batches[i] != nil:
            entries.append(BatchEntry{BatchID: i, TxIDs: batches[i]})

    schedule = BatchSchedule{
        BlockNumber: blockNum,
        Batches:     entries,
        Version:     "v1",
    }
    return schedule
```

---

## 7.7 Go 实现

**新建文件**：`orderer/bench/graph.go`

```go
package bench

import "sort"

// buildConflictGraph 构建事务冲突无向图
func buildConflictGraph(intents []*TxIntent) ConflictGraph {
    graph := ConflictGraph{
        Edges: make(map[string][]string),
    }
    for _, intent := range intents {
        graph.Nodes = append(graph.Nodes, intent.TxID)
    }

    for i := 0; i < len(intents); i++ {
        for j := i + 1; j < len(intents); j++ {
            if txsConflict(intents[i], intents[j]) {
                a, b := intents[i].TxID, intents[j].TxID
                graph.Edges[a] = append(graph.Edges[a], b)
                graph.Edges[b] = append(graph.Edges[b], a)
            }
        }
    }
    return graph
}

func txsConflict(a, b *TxIntent) bool {
    for _, wi := range a.Hypercube.WriteIntervals {
        for _, rj := range b.Hypercube.ReadIntervals {
            if intersects(wi, rj) { return true }
        }
        for _, wj := range b.Hypercube.WriteIntervals {
            if intersects(wi, wj) { return true }
        }
    }
    for _, wj := range b.Hypercube.WriteIntervals {
        for _, ri := range a.Hypercube.ReadIntervals {
            if intersects(wj, ri) { return true }
        }
    }
    return false
}

// greedyGraphColoring 贪心图染色，返回 txID → batchID 映射
func greedyGraphColoring(graph ConflictGraph) ColorAssignment {
    sorted := make([]string, len(graph.Nodes))
    copy(sorted, graph.Nodes)
    sort.Strings(sorted)  // 确定性排序

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
        for usedColors[c] { c++ }
        color[txID] = c
        if c > maxColor { maxColor = c }
    }

    return ColorAssignment{TxToColor: color, NumColors: maxColor + 1}
}

// buildPointHypercube 从精确 key 列表构建超立方体（SmallBank 使用）
func buildPointHypercube(readKeys, writeKeys []string) HypercubeSet {
    h := HypercubeSet{}
    for _, k := range readKeys {
        h.ReadIntervals = append(h.ReadIntervals, KeyInterval{Start: k, End: k, IsRange: false})
    }
    for _, k := range writeKeys {
        h.WriteIntervals = append(h.WriteIntervals, KeyInterval{Start: k, End: k, IsRange: false})
    }
    return h
}
```

---

*返回：[目录](00-index.md) | 前一篇：[05-orderer-changes.md](05-orderer-changes.md) | 下一篇：[08-smallbank.md](08-smallbank.md)*
