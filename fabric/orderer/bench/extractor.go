// by lyj
package bench

import (
	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/core/bench"
	cb "github.com/hyperledger/fabric/protos/common"
	pb "github.com/hyperledger/fabric/protos/peer"
	"github.com/hyperledger/fabric/protos/utils"
)

// below by lyj

const (
	BenchmarkSmallBank = "smallbank"
	BenchmarkYCSB      = "ycsb"
)

// IsBenchmarkChaincode 判断链码是否为 benchmark 目标（精确名称匹配）
func IsBenchmarkChaincode(name string) bool {
	return name == BenchmarkSmallBank || name == BenchmarkYCSB
}

// extractIntentsFromBlock 遍历 block 内所有 envelope，提取 benchmark tx 的 intent
func extractIntentsFromBlock(block *cb.Block) []*bench.TxIntent {
	var intents []*bench.TxIntent
	for _, envBytes := range block.Data.Data {
		intent := extractIntentFromEnvBytes(envBytes)
		if intent != nil {
			intents = append(intents, intent)
		}
	}
	return intents
}

func extractIntentFromEnvBytes(envBytes []byte) *bench.TxIntent {
	env, err := utils.GetEnvelopeFromBlock(envBytes)
	if err != nil {
		return nil
	}
	payload, err := utils.GetPayload(env)
	if err != nil {
		return nil
	}
	chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
	if err != nil {
		return nil
	}

	if cb.HeaderType(chdr.Type) != cb.HeaderType_ENDORSER_TRANSACTION {
		return nil
	}

	hdrExt, err := utils.GetChaincodeHeaderExtension(payload.Header)
	if err != nil {
		return nil
	}
	ccName := hdrExt.ChaincodeId.Name

	if !IsBenchmarkChaincode(ccName) {
		return nil
	}

	tx, err := utils.GetTransaction(payload.Data)
	if err != nil {
		return nil
	}
	if len(tx.Actions) == 0 {
		return nil
	}
	cap, err := utils.GetChaincodeActionPayload(tx.Actions[0].Payload)
	if err != nil {
		return nil
	}
	cpp, err := utils.GetChaincodeProposalPayload(cap.ChaincodeProposalPayload)
	if err != nil {
		return nil
	}

	cis := &pb.ChaincodeInvocationSpec{}
	if err = proto.Unmarshal(cpp.Input, cis); err != nil {
		return nil
	}

	rawArgs := cis.ChaincodeSpec.Input.Args
	if len(rawArgs) < 1 {
		return nil
	}
	funcName := string(rawArgs[0])
	strArgs := argsToStrings(rawArgs[1:])

	switch ccName {
	case BenchmarkSmallBank:
		return ExtractSmallBankIntent(chdr.TxId, chdr.ChannelId, funcName, strArgs)
	// below by lyj
	case BenchmarkYCSB:
		return ExtractYCSBIntent(chdr.TxId, chdr.ChannelId, funcName, strArgs)
	// end by lyj
	}
	return nil
}

func argsToStrings(raw [][]byte) []string {
	result := make([]string, len(raw))
	for i, b := range raw {
		result[i] = string(b)
	}
	return result
}

// end by lyj
