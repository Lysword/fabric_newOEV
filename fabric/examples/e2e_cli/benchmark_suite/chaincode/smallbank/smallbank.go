package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	pb "github.com/hyperledger/fabric/protos/peer"
)

type SmallbankChaincode struct{}

type accountRecord struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	Checking  int64  `json:"checking"`
	Savings   int64  `json:"savings"`
}

type balanceView struct {
	AccountID string `json:"account_id"`
	Checking  int64  `json:"checking"`
	Savings   int64  `json:"savings"`
}

type receiptRecord struct {
	Benchmark       string   `json:"benchmark"`
	RequestID       string   `json:"request_id"`
	Operation       string   `json:"operation"`
	TxID            string   `json:"tx_id"`
	AffectedAccount []string `json:"affected_accounts,omitempty"`
}

type receiptView struct {
	Found   bool           `json:"found"`
	Receipt *receiptRecord `json:"receipt,omitempty"`
}

type pingView struct {
	Benchmark string `json:"benchmark"`
	Status    string `json:"status"`
}

func (cc *SmallbankChaincode) Init(stub shim.ChaincodeStubInterface) pb.Response {
	return shim.Success(nil)
}

func (cc *SmallbankChaincode) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	function, args := stub.GetFunctionAndParameters()

	switch function {
	case "Ping":
		return cc.Ping(stub)
	case "GetReceipt":
		return cc.GetReceipt(stub, args)
	case "BatchGetReceipts":
		return cc.BatchGetReceipts(stub, args)
	case "CreateAccount":
		return cc.CreateAccount(stub, args)
	case "Balance":
		return cc.Balance(stub, args)
	case "DepositChecking":
		return cc.DepositChecking(stub, args)
	case "TransactSavings":
		return cc.TransactSavings(stub, args)
	case "Amalgamate":
		return cc.Amalgamate(stub, args)
	case "WriteCheck":
		return cc.WriteCheck(stub, args)
	case "SendPayment":
		return cc.SendPayment(stub, args)
	default:
		return shim.Error("unsupported function: " + function)
	}
}

func (cc *SmallbankChaincode) Ping(stub shim.ChaincodeStubInterface) pb.Response {
	payload, err := json.Marshal(pingView{
		Benchmark: "smallbank",
		Status:    "ok",
	})
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(payload)
}

func (cc *SmallbankChaincode) GetReceipt(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("GetReceipt expects 1 argument")
	}

	requestID := strings.TrimSpace(args[0])
	if requestID == "" {
		return shim.Error("request_id is required")
	}

	payload, err := stub.GetState(receiptKey(requestID))
	if err != nil {
		return shim.Error(err.Error())
	}
	if payload == nil {
		return marshalReceiptView(receiptView{Found: false})
	}

	var receipt receiptRecord
	if err := json.Unmarshal(payload, &receipt); err != nil {
		return shim.Error(err.Error())
	}

	return marshalReceiptView(receiptView{
		Found:   true,
		Receipt: &receipt,
	})
}

func (cc *SmallbankChaincode) BatchGetReceipts(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("BatchGetReceipts expects 1 argument")
	}

	var requestIDs []string
	if err := json.Unmarshal([]byte(args[0]), &requestIDs); err != nil {
		return shim.Error("invalid request_id list payload")
	}

	result := make(map[string]bool, len(requestIDs))
	for _, rawID := range requestIDs {
		requestID := strings.TrimSpace(rawID)
		if requestID == "" {
			continue
		}
		payload, err := stub.GetState(receiptKey(requestID))
		if err != nil {
			return shim.Error(err.Error())
		}
		result[requestID] = payload != nil
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(payload)
}

func (cc *SmallbankChaincode) CreateAccount(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 4 && len(args) != 5 {
		return shim.Error("CreateAccount expects 4 or 5 arguments")
	}

	accountID := strings.TrimSpace(args[0])
	name := strings.TrimSpace(args[1])
	requestID := optionalRequestID(args, 4)
	checking, err := parseNonNegativeAmount("checking", args[2])
	if err != nil {
		return shim.Error(err.Error())
	}
	savings, err := parseNonNegativeAmount("savings", args[3])
	if err != nil {
		return shim.Error(err.Error())
	}
	if accountID == "" {
		return shim.Error("account_id is required")
	}
	if name == "" {
		return shim.Error("name is required")
	}

	existing, err := stub.GetState(accountKey(accountID))
	if err != nil {
		return shim.Error(err.Error())
	}
	if existing != nil {
		return shim.Error("account already exists: " + accountID)
	}

	account := accountRecord{
		AccountID: accountID,
		Name:      name,
		Checking:  checking,
		Savings:   savings,
	}
	if err := putAccount(stub, account); err != nil {
		return shim.Error(err.Error())
	}
	return successWithOptionalReceipt(stub, requestID, "CreateAccount", []string{accountID})
}

func (cc *SmallbankChaincode) Balance(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("Balance expects 1 argument")
	}

	account, err := getAccount(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}

	payload, err := json.Marshal(balanceView{
		AccountID: account.AccountID,
		Checking:  account.Checking,
		Savings:   account.Savings,
	})
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(payload)
}

func (cc *SmallbankChaincode) DepositChecking(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 && len(args) != 3 {
		return shim.Error("DepositChecking expects 2 or 3 arguments")
	}

	account, err := getAccount(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	requestID := optionalRequestID(args, 2)
	amount, err := parseNonNegativeAmount("amount", args[1])
	if err != nil {
		return shim.Error(err.Error())
	}
	account.Checking += amount
	if err := putAccount(stub, account); err != nil {
		return shim.Error(err.Error())
	}
	return successWithOptionalReceipt(stub, requestID, "DepositChecking", []string{account.AccountID})
}

func (cc *SmallbankChaincode) TransactSavings(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 && len(args) != 3 {
		return shim.Error("TransactSavings expects 2 or 3 arguments")
	}

	account, err := getAccount(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	requestID := optionalRequestID(args, 2)
	amount, err := parseSignedAmount("amount", args[1])
	if err != nil {
		return shim.Error(err.Error())
	}
	if account.Savings+amount < 0 {
		return shim.Error("insufficient funds in savings")
	}
	account.Savings += amount
	if err := putAccount(stub, account); err != nil {
		return shim.Error(err.Error())
	}
	return successWithOptionalReceipt(stub, requestID, "TransactSavings", []string{account.AccountID})
}

func (cc *SmallbankChaincode) Amalgamate(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 && len(args) != 3 {
		return shim.Error("Amalgamate expects 2 or 3 arguments")
	}

	source, err := getAccount(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	dest, err := getAccount(stub, args[1])
	if err != nil {
		return shim.Error(err.Error())
	}
	requestID := optionalRequestID(args, 2)

	transfer := source.Checking + source.Savings
	source.Checking = 0
	source.Savings = 0
	dest.Checking += transfer

	if err := writeAccounts(stub, source, dest); err != nil {
		return shim.Error(err.Error())
	}
	return successWithOptionalReceipt(stub, requestID, "Amalgamate", []string{source.AccountID, dest.AccountID})
}

func (cc *SmallbankChaincode) WriteCheck(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 && len(args) != 3 {
		return shim.Error("WriteCheck expects 2 or 3 arguments")
	}

	account, err := getAccount(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	requestID := optionalRequestID(args, 2)
	amount, err := parseNonNegativeAmount("amount", args[1])
	if err != nil {
		return shim.Error(err.Error())
	}
	if account.Checking+account.Savings < amount {
		account.Checking -= amount + 1
	} else {
		account.Checking -= amount
	}
	if err := putAccount(stub, account); err != nil {
		return shim.Error(err.Error())
	}
	return successWithOptionalReceipt(stub, requestID, "WriteCheck", []string{account.AccountID})
}

func (cc *SmallbankChaincode) SendPayment(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 3 && len(args) != 4 {
		return shim.Error("SendPayment expects 3 or 4 arguments")
	}

	from, err := getAccount(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	to, err := getAccount(stub, args[1])
	if err != nil {
		return shim.Error(err.Error())
	}
	requestID := optionalRequestID(args, 3)
	amount, err := parseNonNegativeAmount("amount", args[2])
	if err != nil {
		return shim.Error(err.Error())
	}
	if from.Checking < amount {
		return shim.Error("insufficient funds in checking")
	}

	from.Checking -= amount
	to.Checking += amount

	if err := writeAccounts(stub, from, to); err != nil {
		return shim.Error(err.Error())
	}
	return successWithOptionalReceipt(stub, requestID, "SendPayment", []string{from.AccountID, to.AccountID})
}

func getAccount(stub shim.ChaincodeStubInterface, accountID string) (accountRecord, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return accountRecord{}, fmt.Errorf("account_id is required")
	}

	payload, err := stub.GetState(accountKey(accountID))
	if err != nil {
		return accountRecord{}, err
	}
	if payload == nil {
		return accountRecord{}, fmt.Errorf("account not found: %s", accountID)
	}

	var account accountRecord
	if err := json.Unmarshal(payload, &account); err != nil {
		return accountRecord{}, err
	}
	return account, nil
}

func putAccount(stub shim.ChaincodeStubInterface, account accountRecord) error {
	payload, err := json.Marshal(account)
	if err != nil {
		return err
	}
	if err := stub.PutState(accountKey(account.AccountID), payload); err != nil {
		return err
	}
	return nil
}

func writeAccounts(stub shim.ChaincodeStubInterface, accounts ...accountRecord) error {
	for _, account := range accounts {
		payload, err := json.Marshal(account)
		if err != nil {
			return err
		}
		if err := stub.PutState(accountKey(account.AccountID), payload); err != nil {
			return err
		}
	}
	return nil
}

func parseNonNegativeAmount(field string, raw string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %s", field, raw)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative", field)
	}
	return value, nil
}

func parseSignedAmount(field string, raw string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %s", field, raw)
	}
	return value, nil
}

func optionalRequestID(args []string, baseArgCount int) string {
	if len(args) == baseArgCount+1 {
		return strings.TrimSpace(args[baseArgCount])
	}
	return ""
}

func successWithOptionalReceipt(stub shim.ChaincodeStubInterface, requestID string, operation string, accounts []string) pb.Response {
	if requestID != "" {
		if err := putReceipt(stub, receiptRecord{
			Benchmark:       "smallbank",
			RequestID:       requestID,
			Operation:       operation,
			TxID:            stub.GetTxID(),
			AffectedAccount: accounts,
		}); err != nil {
			return shim.Error(err.Error())
		}
	}
	return shim.Success(nil)
}

func putReceipt(stub shim.ChaincodeStubInterface, receipt receiptRecord) error {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return stub.PutState(receiptKey(receipt.RequestID), payload)
}

func marshalReceiptView(view receiptView) pb.Response {
	payload, err := json.Marshal(view)
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(payload)
}

func accountKey(accountID string) string {
	return "account:" + accountID
}

func receiptKey(requestID string) string {
	return "receipt:" + requestID
}

func main() {
	if err := shim.Start(new(SmallbankChaincode)); err != nil {
		fmt.Printf("Error starting Smallbank chaincode: %s", err)
	}
}
