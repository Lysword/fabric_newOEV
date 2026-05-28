// by lyj
package bench

import (
	"encoding/json"

	"github.com/hyperledger/fabric/core/bench"
	cb "github.com/hyperledger/fabric/protos/common"
	"github.com/op/go-logging"
)

// below by lyj

var logger = logging.MustGetLogger("orderer/bench")

// AnalyzeBatchAndSchedule 分析 block 内所有 tx，生成批次调度字节
// 若无 benchmark tx 则返回 nil（orderer 保持原有行为）
func AnalyzeBatchAndSchedule(block *cb.Block) []byte {
	intents := extractIntentsFromBlock(block)
	if len(intents) == 0 {
		return nil
	}

	logger.Infof("Block [%d]: found %d benchmark tx, building conflict graph", block.Header.Number, len(intents))

	graph := buildConflictGraph(intents)
	coloring := greedyGraphColoring(graph)
	schedule := buildBatchSchedule(block.Header.Number, intents, coloring)

	logger.Infof("Block [%d]: %d benchmark tx → %d batches", block.Header.Number, len(intents), len(schedule.Batches))

	data, err := json.Marshal(schedule)
	if err != nil {
		logger.Warningf("Block [%d]: failed to marshal BatchSchedule: %s", block.Header.Number, err)
		return nil
	}
	return data
}

func buildBatchSchedule(blockNum uint64, intents []*bench.TxIntent, coloring ColorAssignment) *bench.BatchSchedule {
	batches := make(map[int][]string)
	for _, intent := range intents {
		batchID := coloring.TxToColor[intent.TxID]
		batches[batchID] = append(batches[batchID], intent.TxID)
	}

	entries := make([]bench.BatchEntry, 0, coloring.NumColors)
	for i := 0; i < coloring.NumColors; i++ {
		if txIDs, ok := batches[i]; ok {
			entries = append(entries, bench.BatchEntry{BatchID: i, TxIDs: txIDs})
		}
	}

	return &bench.BatchSchedule{
		BlockNumber: blockNum,
		Batches:     entries,
		Version:     "v1",
	}
}

// end by lyj
