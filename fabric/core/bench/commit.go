// by lyj
package bench

import (
	"fmt"

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

// CommitBenchmarkTxs 按 BatchSchedule 的 batch 顺序重放 benchmark tx 并写入 stateDB
func CommitBenchmarkTxs(block *cb.Block, schedule *BatchSchedule, db statedb.VersionedDB) error {
	adapter := NewStateDBAdapter(db, block.Header.Number, len(block.Data.Data))

	for _, batchEntry := range schedule.Batches {
		for _, txID := range batchEntry.TxIDs {
			envBytes := findTxInBlock(block, txID)
			if envBytes == nil {
				logger.Warningf("tx %s not found in block %d", txID, block.Header.Number)
				continue
			}
			ccName, funcName, strArgs, err := extractInvocationFromEnvBytes(envBytes)
			if err != nil {
				logger.Warningf("tx %s: extract invocation failed: %s", txID, err)
				continue
			}

			writes, err := replayTx(adapter, ccName, funcName, strArgs)
			if err != nil {
				logger.Warningf("tx %s: replay %s.%s failed: %s", txID, ccName, funcName, err)
				continue
			}

			for key, value := range writes {
				adapter.PutState(ccName, key, value)
			}
		}
	}

	return adapter.Flush()
}

// replayTx 根据链码名称分派到对应的重放引擎
func replayTx(db *StateDBAdapter, ccName, funcName string, args []string) (map[string][]byte, error) {
	switch ccName {
	case ccSmallBank:
		return replaySmallBank(db, funcName, args)
	// case ccYCSB:
	//     return replayYCSB(db, funcName, args) // Phase 2
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

// end by lyj
