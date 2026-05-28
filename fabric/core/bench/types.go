// by lyj
package bench

import (
	"encoding/json"

	"github.com/golang/protobuf/proto"
	cb "github.com/hyperledger/fabric/protos/common"
)

// below by lyj

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
	IsRange bool
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
	Args      []string
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

// BatchEntry 单个批次内的交易列表
type BatchEntry struct {
	BatchID int      `json:"batch_id"`
	TxIDs   []string `json:"tx_ids"`
}

// BatchSchedule orderer 产生的批次调度结果，写入 BlockMetadata[ORDERER]
type BatchSchedule struct {
	BlockNumber uint64       `json:"block_number"`
	Batches     []BatchEntry `json:"batches"`
	Version     string       `json:"version"`
}

// AllTxIDs 返回 BatchSchedule 中所有 benchmark txID 的集合
func (s *BatchSchedule) AllTxIDs() map[string]bool {
	set := make(map[string]bool)
	for _, entry := range s.Batches {
		for _, txID := range entry.TxIDs {
			set[txID] = true
		}
	}
	return set
}

// ParseBatchSchedule 从 block metadata 解析批次调度信息
func ParseBatchSchedule(block *cb.Block) (*BatchSchedule, bool) {
	if block.Metadata == nil || len(block.Metadata.Metadata) <= int(cb.BlockMetadataIndex_ORDERER) {
		return nil, false
	}
	raw := block.Metadata.Metadata[cb.BlockMetadataIndex_ORDERER]
	if len(raw) == 0 {
		return nil, false
	}
	ordererMeta := &cb.Metadata{}
	if err := proto.Unmarshal(raw, ordererMeta); err != nil {
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

// end by lyj
