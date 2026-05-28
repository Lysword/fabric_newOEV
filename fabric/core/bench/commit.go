// by lyj
package bench

import (
	"fmt"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/statedb"
	cb "github.com/hyperledger/fabric/protos/common"
	pb "github.com/hyperledger/fabric/protos/peer"
	"github.com/hyperledger/fabric/protos/utils"
)

// below by lyj

var logger = flogging.MustGetLogger("bench/commit")

const (
	ccSmallBank = "smallbank"
	ccYCSB      = "ycsb"
)

// txReplayResult 单笔 tx 并行重放的结果
type txReplayResult struct {
	txID   string
	ccName string
	writes map[string][]byte
	err    error
}

// CommitBenchmarkTxs 按 BatchSchedule 的 batch 顺序重放 benchmark tx 并写入 stateDB
// batch 间串行保证因果顺序，同 batch 内并行（图着色保证无冲突）
func CommitBenchmarkTxs(block *cb.Block, schedule *BatchSchedule, db statedb.VersionedDB) error {
	adapter := NewStateDBAdapter(db, block.Header.Number, len(block.Data.Data))

	totalTxCount := 0
	for _, b := range schedule.Batches {
		totalTxCount += len(b.TxIDs)
	}

	replayStart := time.Now()
	logger.Infof("Block [%d]: starting benchmark replay — %d tx in %d batches",
		block.Header.Number, totalTxCount, len(schedule.Batches))

	for batchIdx, batchEntry := range schedule.Batches {
		batchStart := time.Now()
		txCount := len(batchEntry.TxIDs)

		if txCount <= 1 {
			// 单 tx batch 无需并行开销，直接串行
			for _, txID := range batchEntry.TxIDs {
				replaySingleTx(adapter, block, txID)
			}
		} else {
			// 同 batch 内多 tx 并行重放
			results := make([]txReplayResult, txCount)
			var wg sync.WaitGroup
			wg.Add(txCount)

			for i, txID := range batchEntry.TxIDs {
				go func(idx int, tid string) {
					defer wg.Done()
					results[idx] = replayTxForParallel(adapter, block, tid)
				}(i, txID)
			}
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
		}

		logger.Infof("Block [%d]: batch %d/%d completed — %d tx in %v",
			block.Header.Number, batchIdx+1, len(schedule.Batches), txCount, time.Since(batchStart))
	}

	replayElapsed := time.Since(replayStart)
	flushStart := time.Now()
	err := adapter.Flush()
	flushElapsed := time.Since(flushStart)

	logger.Infof("Block [%d]: benchmark replay done — %d tx, %d batches, replay %v, flush %v",
		block.Header.Number, totalTxCount, len(schedule.Batches), replayElapsed, flushElapsed)

	return err
}

// replaySingleTx 串行重放单笔 tx 并直接写入 adapter
func replaySingleTx(adapter *StateDBAdapter, block *cb.Block, txID string) {
	envBytes := findTxInBlock(block, txID)
	if envBytes == nil {
		logger.Warningf("tx %s not found in block %d", txID, block.Header.Number)
		return
	}
	ccName, funcName, strArgs, err := extractInvocationFromEnvBytes(envBytes)
	if err != nil {
		logger.Warningf("tx %s: extract invocation failed: %s", txID, err)
		return
	}
	writes, err := replayTx(adapter, ccName, funcName, strArgs)
	if err != nil {
		logger.Warningf("tx %s: replay %s.%s failed: %s", txID, ccName, funcName, err)
		return
	}
	for key, value := range writes {
		adapter.PutState(ccName, key, value)
	}
}

// replayTxForParallel 并行安全的 tx 重放，返回结果而不直接写 adapter
func replayTxForParallel(adapter *StateDBAdapter, block *cb.Block, txID string) txReplayResult {
	envBytes := findTxInBlock(block, txID)
	if envBytes == nil {
		return txReplayResult{txID: txID, err: fmt.Errorf("tx not found in block %d", block.Header.Number)}
	}
	ccName, funcName, strArgs, err := extractInvocationFromEnvBytes(envBytes)
	if err != nil {
		return txReplayResult{txID: txID, err: fmt.Errorf("extract invocation failed: %s", err)}
	}
	writes, err := replayTx(adapter, ccName, funcName, strArgs)
	if err != nil {
		return txReplayResult{txID: txID, err: fmt.Errorf("replay %s.%s failed: %s", ccName, funcName, err)}
	}
	return txReplayResult{txID: txID, ccName: ccName, writes: writes}
}

// replayTx 根据链码名称分派到对应的重放引擎
func replayTx(db *StateDBAdapter, ccName, funcName string, args []string) (map[string][]byte, error) {
	switch ccName {
	case ccSmallBank:
		return replaySmallBank(db, funcName, args)
	// below by lyj
	case ccYCSB:
		return replayYCSB(db, funcName, args)
	// end by lyj
	default:
		return nil, fmt.Errorf("unsupported chaincode for replay: %s", ccName)
	}
}

// findTxInBlock 在 block 中根据 txID 查找对应的 Envelope 字节
func findTxInBlock(block *cb.Block, txID string) []byte {
	for _, envBytes := range block.Data.Data {
		env, err := utils.GetEnvelopeFromBlock(envBytes)
		if err != nil {
			continue
		}
		payload, err := utils.GetPayload(env)
		if err != nil {
			continue
		}
		chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
		if err != nil {
			continue
		}
		if chdr.TxId == txID {
			return envBytes
		}
	}
	return nil
}

// extractInvocationFromEnvBytes 从 Envelope 字节中提取合约调用信息
func extractInvocationFromEnvBytes(envBytes []byte) (ccName string, funcName string, args []string, err error) {
	env, err := utils.GetEnvelopeFromBlock(envBytes)
	if err != nil {
		return
	}
	payload, err := utils.GetPayload(env)
	if err != nil {
		return
	}
	hdrExt, err := utils.GetChaincodeHeaderExtension(payload.Header)
	if err != nil {
		return
	}
	ccName = hdrExt.ChaincodeId.Name

	tx, err := utils.GetTransaction(payload.Data)
	if err != nil {
		return
	}
	if len(tx.Actions) == 0 {
		err = fmt.Errorf("no actions in tx")
		return
	}
	cap, err := utils.GetChaincodeActionPayload(tx.Actions[0].Payload)
	if err != nil {
		return
	}
	cpp, err := utils.GetChaincodeProposalPayload(cap.ChaincodeProposalPayload)
	if err != nil {
		return
	}
	cis := &pb.ChaincodeInvocationSpec{}
	if err = proto.Unmarshal(cpp.Input, cis); err != nil {
		return
	}

	rawArgs := cis.ChaincodeSpec.Input.Args
	if len(rawArgs) < 1 {
		err = fmt.Errorf("empty args")
		return
	}
	funcName = string(rawArgs[0])
	for _, a := range rawArgs[1:] {
		args = append(args, string(a))
	}
	return
}

// BuildTxIDIndexMap 构建 block 内 txID → txIndex 的映射，用于操作 txFilter
func BuildTxIDIndexMap(block *cb.Block) map[string]int {
	m := make(map[string]int)
	for i, envBytes := range block.Data.Data {
		env, err := utils.GetEnvelopeFromBlock(envBytes)
		if err != nil {
			continue
		}
		payload, err := utils.GetPayload(env)
		if err != nil {
			continue
		}
		chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
		if err != nil {
			continue
		}
		m[chdr.TxId] = i
	}
	return m
}

// end by lyj
