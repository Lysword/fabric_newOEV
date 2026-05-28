// by lyj
package bench

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// below by lyj

type sbAccountRecord struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	Checking  int64  `json:"checking"`
	Savings   int64  `json:"savings"`
}

// replaySmallBank 在当前 stateDB 上重放 SmallBank 操作，返回写集 map[key]value
func replaySmallBank(db *StateDBAdapter, funcName string, args []string) (map[string][]byte, error) {
	switch funcName {
	case "DepositChecking":
		return sbReplayDepositChecking(db, args)
	case "TransactSavings":
		return sbReplayTransactSavings(db, args)
	case "WriteCheck":
		return sbReplayWriteCheck(db, args)
	case "Amalgamate":
		return sbReplayAmalgamate(db, args)
	case "SendPayment":
		return sbReplaySendPayment(db, args)
	case "CreateAccount":
		return sbReplayCreateAccount(db, args)
	case "Balance":
		return map[string][]byte{}, nil
	default:
		return nil, fmt.Errorf("unsupported smallbank function: %s", funcName)
	}
}

func sbGetAccount(db *StateDBAdapter, accountID string) (sbAccountRecord, error) {
	data, err := db.GetState("smallbank", sbKey(accountID))
	if err != nil {
		return sbAccountRecord{}, err
	}
	if data == nil {
		return sbAccountRecord{}, fmt.Errorf("account not found: %s", accountID)
	}
	var acc sbAccountRecord
	if err := json.Unmarshal(data, &acc); err != nil {
		return sbAccountRecord{}, err
	}
	return acc, nil
}

func sbMarshalAccount(acc sbAccountRecord) ([]byte, error) {
	return json.Marshal(acc)
}

func sbKey(accountID string) string {
	return "account:" + strings.TrimSpace(accountID)
}

func sbReplayDepositChecking(db *StateDBAdapter, args []string) (map[string][]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("DepositChecking requires 2 args")
	}
	accountID := args[0]
	amount, err := strconv.ParseInt(strings.TrimSpace(args[1]), 10, 64)
	if err != nil {
		return nil, err
	}

	acc, err := sbGetAccount(db, accountID)
	if err != nil {
		return nil, err
	}
	acc.Checking += amount
	value, err := sbMarshalAccount(acc)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{sbKey(accountID): value}, nil
}

func sbReplayTransactSavings(db *StateDBAdapter, args []string) (map[string][]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("TransactSavings requires 2 args")
	}
	accountID := args[0]
	amount, err := strconv.ParseInt(strings.TrimSpace(args[1]), 10, 64)
	if err != nil {
		return nil, err
	}

	acc, err := sbGetAccount(db, accountID)
	if err != nil {
		return nil, err
	}
	if acc.Savings+amount < 0 {
		return nil, fmt.Errorf("insufficient funds in savings")
	}
	acc.Savings += amount
	value, err := sbMarshalAccount(acc)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{sbKey(accountID): value}, nil
}

func sbReplayWriteCheck(db *StateDBAdapter, args []string) (map[string][]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("WriteCheck requires 2 args")
	}
	accountID := args[0]
	amount, err := strconv.ParseInt(strings.TrimSpace(args[1]), 10, 64)
	if err != nil {
		return nil, err
	}

	acc, err := sbGetAccount(db, accountID)
	if err != nil {
		return nil, err
	}
	if acc.Checking+acc.Savings < amount {
		acc.Checking -= amount + 1
	} else {
		acc.Checking -= amount
	}
	value, err := sbMarshalAccount(acc)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{sbKey(accountID): value}, nil
}

func sbReplayAmalgamate(db *StateDBAdapter, args []string) (map[string][]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("Amalgamate requires 2 args")
	}
	srcID, dstID := args[0], args[1]
	src, err := sbGetAccount(db, srcID)
	if err != nil {
		return nil, err
	}
	dst, err := sbGetAccount(db, dstID)
	if err != nil {
		return nil, err
	}

	transfer := src.Checking + src.Savings
	src.Checking = 0
	src.Savings = 0
	dst.Checking += transfer

	srcBytes, err := sbMarshalAccount(src)
	if err != nil {
		return nil, err
	}
	dstBytes, err := sbMarshalAccount(dst)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{sbKey(srcID): srcBytes, sbKey(dstID): dstBytes}, nil
}

func sbReplaySendPayment(db *StateDBAdapter, args []string) (map[string][]byte, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("SendPayment requires 3 args")
	}
	fromID, toID := args[0], args[1]
	amount, err := strconv.ParseInt(strings.TrimSpace(args[2]), 10, 64)
	if err != nil {
		return nil, err
	}

	from, err := sbGetAccount(db, fromID)
	if err != nil {
		return nil, err
	}
	to, err := sbGetAccount(db, toID)
	if err != nil {
		return nil, err
	}
	if from.Checking < amount {
		return nil, fmt.Errorf("insufficient funds in checking")
	}
	from.Checking -= amount
	to.Checking += amount

	fromBytes, err := sbMarshalAccount(from)
	if err != nil {
		return nil, err
	}
	toBytes, err := sbMarshalAccount(to)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{sbKey(fromID): fromBytes, sbKey(toID): toBytes}, nil
}

func sbReplayCreateAccount(db *StateDBAdapter, args []string) (map[string][]byte, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("CreateAccount requires 4 args")
	}
	accountID := args[0]
	name := args[1]
	checking, err := strconv.ParseInt(strings.TrimSpace(args[2]), 10, 64)
	if err != nil {
		return nil, err
	}
	savings, err := strconv.ParseInt(strings.TrimSpace(args[3]), 10, 64)
	if err != nil {
		return nil, err
	}

	acc := sbAccountRecord{AccountID: accountID, Name: name, Checking: checking, Savings: savings}
	value, err := sbMarshalAccount(acc)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{sbKey(accountID): value}, nil
}

// end by lyj
