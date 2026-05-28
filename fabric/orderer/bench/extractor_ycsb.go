// by lyj
package bench

import (
	"unicode/utf8"

	"github.com/hyperledger/fabric/core/bench"
)

// below by lyj

// ExtractYCSBIntent 从 funcName + args 提取 YCSB 交易的读写意图
func ExtractYCSBIntent(txID, channelID, funcName string, args []string) *bench.TxIntent {
	intent := &bench.TxIntent{
		TxID:          txID,
		ChannelID:     channelID,
		ChaincodeName: BenchmarkYCSB,
		Benchmark:     bench.BenchYCSB,
		FunctionName:  funcName,
		Args:          args,
		ReplayPlan:    bench.ReplayOp{Benchmark: bench.BenchYCSB, Function: funcName, Args: args},
	}

	switch funcName {
	case "Read":
		if len(args) < 1 {
			return nil
		}
		intent.ReadKeys = []string{args[0]}
		intent.WriteKeys = nil
		intent.Hypercube = buildPointHypercube(intent.ReadKeys, nil)

	case "Insert", "Update":
		if len(args) < 2 {
			return nil
		}
		key := args[0]
		intent.ReadKeys = []string{key}
		intent.WriteKeys = []string{key}
		intent.Hypercube = buildPointHypercube(intent.ReadKeys, intent.WriteKeys)

	case "Scan":
		if len(args) < 2 {
			return nil
		}
		startKey := args[0]
		endKey := string(utf8.MaxRune)
		intent.ReadKeys = nil
		intent.WriteKeys = nil
		intent.Hypercube = bench.HypercubeSet{
			ReadIntervals: []bench.KeyInterval{{Start: startKey, End: endKey, IsRange: true}},
		}

	default:
		return nil
	}
	return intent
}

// end by lyj
