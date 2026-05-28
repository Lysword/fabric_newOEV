// by lyj
package bench

import (
	"sort"

	"github.com/hyperledger/fabric/core/bench"
)

// below by lyj

// ConflictGraph 事务冲突无向图（仅在 orderer 内存中使用，不序列化）
type ConflictGraph struct {
	Nodes []string
	Edges map[string][]string
}

// ColorAssignment 图染色结果（batchID = color）
type ColorAssignment struct {
	TxToColor map[string]int
	NumColors int
}

// buildConflictGraph 构建事务冲突无向图
func buildConflictGraph(intents []*bench.TxIntent) ConflictGraph {
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

func txsConflict(a, b *bench.TxIntent) bool {
	// WA ∩ RB
	for _, wi := range a.Hypercube.WriteIntervals {
		for _, rj := range b.Hypercube.ReadIntervals {
			if intersects(wi, rj) {
				return true
			}
		}
		// WA ∩ WB
		for _, wj := range b.Hypercube.WriteIntervals {
			if intersects(wi, wj) {
				return true
			}
		}
	}
	// WB ∩ RA
	for _, wj := range b.Hypercube.WriteIntervals {
		for _, ri := range a.Hypercube.ReadIntervals {
			if intersects(wj, ri) {
				return true
			}
		}
	}
	return false
}

// intersects 判断两个 KeyInterval 是否相交
func intersects(a, b bench.KeyInterval) bool {
	if !a.IsRange && !b.IsRange {
		return a.Start == b.Start
	}
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

// greedyGraphColoring 贪心图染色，返回 txID → batchID 映射
// 固定遍历顺序（txID 字典序）确保所有 orderer 节点确定性输出相同结果
func greedyGraphColoring(graph ConflictGraph) ColorAssignment {
	sorted := make([]string, len(graph.Nodes))
	copy(sorted, graph.Nodes)
	sort.Strings(sorted)

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
		for usedColors[c] {
			c++
		}
		color[txID] = c
		if c > maxColor {
			maxColor = c
		}
	}

	numColors := 0
	if len(sorted) > 0 {
		numColors = maxColor + 1
	}
	return ColorAssignment{TxToColor: color, NumColors: numColors}
}

// buildPointHypercube 从精确 key 列表构建超立方体（SmallBank 使用）
func buildPointHypercube(readKeys, writeKeys []string) bench.HypercubeSet {
	h := bench.HypercubeSet{}
	for _, k := range readKeys {
		h.ReadIntervals = append(h.ReadIntervals, bench.KeyInterval{Start: k, End: k, IsRange: false})
	}
	for _, k := range writeKeys {
		h.WriteIntervals = append(h.WriteIntervals, bench.KeyInterval{Start: k, End: k, IsRange: false})
	}
	return h
}

// end by lyj
