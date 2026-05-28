// by lyj
package bench

import (
	"encoding/json"
	"fmt"
)

// below by lyj

type ycsbValuePayload struct {
	Value string `json:"value"`
}

type ycsbRecord struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// replayYCSB 在当前 stateDB 上重放 YCSB 操作，返回写集 map[key]value
func replayYCSB(db *StateDBAdapter, funcName string, args []string) (map[string][]byte, error) {
	switch funcName {
	case "Insert", "Update":
		if len(args) < 2 {
			return nil, fmt.Errorf("%s requires at least 2 args (key, value)", funcName)
		}
		key := args[0]
		value, err := ycsbNormalizeValue(args[1])
		if err != nil {
			return nil, fmt.Errorf("normalize value for key %s: %s", key, err)
		}
		record, err := json.Marshal(ycsbRecord{Key: key, Value: value})
		if err != nil {
			return nil, err
		}
		return map[string][]byte{key: record}, nil

	case "Read":
		return map[string][]byte{}, nil

	case "Scan":
		return map[string][]byte{}, nil

	default:
		return nil, fmt.Errorf("unsupported ycsb function: %s", funcName)
	}
}

// ycsbNormalizeValue 从 JSON payload {"value":"..."} 中提取实际 value 字符串，
// 与合约 normalizeValuePayload 行为一致
func ycsbNormalizeValue(raw string) (string, error) {
	var payload ycsbValuePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw, nil
	}
	if payload.Value == "" {
		return raw, nil
	}
	return payload.Value, nil
}

// end by lyj
