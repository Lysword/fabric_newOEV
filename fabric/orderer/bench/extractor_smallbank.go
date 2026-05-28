// by lyj
package bench

import (
	"strings"

	"github.com/hyperledger/fabric/core/bench"
)

// below by lyj

// ExtractSmallBankIntent 从 funcName + args 提取 SmallBank 交易的读写意图
func ExtractSmallBankIntent(txID, channelID, funcName string, args []string) *bench.TxIntent {
	intent := &bench.TxIntent{
		TxID:          txID,
		ChannelID:     channelID,
		ChaincodeName: BenchmarkSmallBank,
		Benchmark:     bench.BenchSmallBank,
		FunctionName:  funcName,
		Args:          args,
		ReplayPlan:    bench.ReplayOp{Benchmark: bench.BenchSmallBank, Function: funcName, Args: args},
	}

	switch funcName {
	case "Balance":
		if len(args) < 1 {
			return nil
		}
		intent.ReadKeys = []string{sbAccountKey(args[0])}
		intent.WriteKeys = nil
	case "CreateAccount":
		if len(args) < 2 {
			return nil
		}
		k := sbAccountKey(args[0])
		intent.ReadKeys = []string{k}
		intent.WriteKeys = []string{k}
	case "DepositChecking", "TransactSavings", "WriteCheck":
		if len(args) < 2 {
			return nil
		}
		k := sbAccountKey(args[0])
		intent.ReadKeys = []string{k}
		intent.WriteKeys = []string{k}
	case "Amalgamate":
		if len(args) < 2 {
			return nil
		}
		srcKey := sbAccountKey(args[0])
		dstKey := sbAccountKey(args[1])
		intent.ReadKeys = []string{srcKey, dstKey}
		intent.WriteKeys = []string{srcKey, dstKey}
	case "SendPayment":
		if len(args) < 3 {
			return nil
		}
		fromKey := sbAccountKey(args[0])
		toKey := sbAccountKey(args[1])
		intent.ReadKeys = []string{fromKey, toKey}
		intent.WriteKeys = []string{fromKey, toKey}
	default:
		return nil
	}

	intent.Hypercube = buildPointHypercube(intent.ReadKeys, intent.WriteKeys)
	return intent
}

func sbAccountKey(accountID string) string {
	return "account:" + strings.TrimSpace(accountID)
}

// end by lyj
