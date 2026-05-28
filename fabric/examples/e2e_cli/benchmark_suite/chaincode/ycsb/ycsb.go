package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	pb "github.com/hyperledger/fabric/protos/peer"
)

type YCSBChaincode struct{}

type ycsbValuePayload struct {
	Value string `json:"value"`
}

type ycsbRecord struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ycsbReceiptRecord struct {
	Benchmark string `json:"benchmark"`
	RequestID string `json:"request_id"`
	Operation string `json:"operation"`
	Key       string `json:"key,omitempty"`
	TxID      string `json:"tx_id"`
}

type ycsbReceiptView struct {
	Found   bool               `json:"found"`
	Receipt *ycsbReceiptRecord `json:"receipt,omitempty"`
}

type ycsbPingView struct {
	Benchmark string `json:"benchmark"`
	Status    string `json:"status"`
}

func (cc *YCSBChaincode) Init(stub shim.ChaincodeStubInterface) pb.Response {
	return shim.Success(nil)
}

func (cc *YCSBChaincode) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	function, args := stub.GetFunctionAndParameters()

	switch function {
	case "Ping":
		return cc.ping(stub)
	case "GetReceipt":
		return cc.getReceipt(stub, args)
	case "BatchGetReceipts":
		return cc.batchGetReceipts(stub, args)
	case "Insert":
		return cc.insert(stub, args)
	case "Read":
		return cc.read(stub, args)
	case "Update":
		return cc.update(stub, args)
	case "Scan":
		return cc.scan(stub, args)
	default:
		return shim.Error("unsupported function: " + function)
	}
}

func (cc *YCSBChaincode) ping(stub shim.ChaincodeStubInterface) pb.Response {
	payload, err := json.Marshal(ycsbPingView{
		Benchmark: "ycsb",
		Status:    "ok",
	})
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(payload)
}

func (cc *YCSBChaincode) getReceipt(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("GetReceipt expects 1 argument")
	}

	requestID := args[0]
	if requestID == "" {
		return shim.Error("request_id must not be empty")
	}

	payload, err := stub.GetState(ycsbReceiptKey(requestID))
	if err != nil {
		return shim.Error(err.Error())
	}
	if payload == nil {
		return marshalYCSBReceiptView(ycsbReceiptView{Found: false})
	}

	var receipt ycsbReceiptRecord
	if err := json.Unmarshal(payload, &receipt); err != nil {
		return shim.Error(err.Error())
	}
	return marshalYCSBReceiptView(ycsbReceiptView{
		Found:   true,
		Receipt: &receipt,
	})
}

func (cc *YCSBChaincode) batchGetReceipts(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("BatchGetReceipts expects 1 argument")
	}

	var requestIDs []string
	if err := json.Unmarshal([]byte(args[0]), &requestIDs); err != nil {
		return shim.Error("invalid request_id list payload")
	}

	result := make(map[string]bool, len(requestIDs))
	for _, requestID := range requestIDs {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			continue
		}
		payload, err := stub.GetState(ycsbReceiptKey(requestID))
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

func (cc *YCSBChaincode) insert(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 && len(args) != 3 {
		return shim.Error("incorrect number of arguments. expecting 2 or 3")
	}

	key := args[0]
	if key == "" {
		return shim.Error("key must not be empty")
	}

	existing, err := stub.GetState(key)
	if err != nil {
		return shim.Error(err.Error())
	}
	if existing != nil {
		return shim.Error("record already exists for key: " + key)
	}
	requestID := optionalYCSBRequestID(args, 2)

	value, err := normalizeValuePayload(args[1])
	if err != nil {
		return shim.Error(err.Error())
	}

	recordBytes, err := marshalRecord(ycsbRecord{
		Key:   key,
		Value: value,
	})
	if err != nil {
		return shim.Error(err.Error())
	}

	if err := stub.PutState(key, recordBytes); err != nil {
		return shim.Error(err.Error())
	}

	return ycsbSuccessWithOptionalReceipt(stub, requestID, "Insert", key, recordBytes)
}

func (cc *YCSBChaincode) read(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("incorrect number of arguments. expecting 1")
	}

	record, err := getExistingRecord(stub, args[0])
	if err != nil {
		return shim.Error(err.Error())
	}

	recordBytes, err := marshalRecord(record)
	if err != nil {
		return shim.Error(err.Error())
	}

	return shim.Success(recordBytes)
}

func (cc *YCSBChaincode) update(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 && len(args) != 3 {
		return shim.Error("incorrect number of arguments. expecting 2 or 3")
	}

	key := args[0]
	if _, err := getExistingRecord(stub, key); err != nil {
		return shim.Error(err.Error())
	}
	requestID := optionalYCSBRequestID(args, 2)

	value, err := normalizeValuePayload(args[1])
	if err != nil {
		return shim.Error(err.Error())
	}

	recordBytes, err := marshalRecord(ycsbRecord{
		Key:   key,
		Value: value,
	})
	if err != nil {
		return shim.Error(err.Error())
	}

	if err := stub.PutState(key, recordBytes); err != nil {
		return shim.Error(err.Error())
	}

	return ycsbSuccessWithOptionalReceipt(stub, requestID, "Update", key, recordBytes)
}

func (cc *YCSBChaincode) scan(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 {
		return shim.Error("incorrect number of arguments. expecting 2")
	}

	startKey := args[0]
	if startKey == "" {
		return shim.Error("start key must not be empty")
	}

	limit, err := strconv.Atoi(args[1])
	if err != nil || limit <= 0 {
		return shim.Error("scan length must be a positive integer")
	}

	endKey := string(utf8.MaxRune)
	iterator, err := stub.GetStateByRange(startKey, endKey)
	if err != nil {
		return shim.Error(err.Error())
	}
	defer iterator.Close()

	records := make([]ycsbRecord, 0, limit)
	for iterator.HasNext() && len(records) < limit {
		item, nextErr := iterator.Next()
		if nextErr != nil {
			return shim.Error(nextErr.Error())
		}

		if strings.HasPrefix(item.Key, "receipt:") {
			continue
		}

		record, decodeErr := decodeStoredRecord(item.Key, item.Value)
		if decodeErr != nil {
			return shim.Error(decodeErr.Error())
		}

		records = append(records, record)
	}

	payload, err := json.Marshal(records)
	if err != nil {
		return shim.Error(err.Error())
	}

	return shim.Success(payload)
}

func getExistingRecord(stub shim.ChaincodeStubInterface, key string) (ycsbRecord, error) {
	if key == "" {
		return ycsbRecord{}, fmt.Errorf("key must not be empty")
	}

	recordBytes, err := stub.GetState(key)
	if err != nil {
		return ycsbRecord{}, err
	}
	if recordBytes == nil {
		return ycsbRecord{}, fmt.Errorf("record not found for key: %s", key)
	}

	return decodeStoredRecord(key, recordBytes)
}

func decodeStoredRecord(key string, payload []byte) (ycsbRecord, error) {
	var record ycsbRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return ycsbRecord{}, err
	}

	if record.Key == "" {
		record.Key = key
	}

	return record, nil
}

func normalizeValuePayload(raw string) (string, error) {
	var payload ycsbValuePayload
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	if err := decoder.Decode(&payload); err != nil {
		return "", fmt.Errorf("invalid value payload: %s", err)
	}
	if payload.Value == "" {
		return "", fmt.Errorf("value must not be empty")
	}

	return payload.Value, nil
}

func marshalRecord(record ycsbRecord) ([]byte, error) {
	return json.Marshal(record)
}

func optionalYCSBRequestID(args []string, baseArgCount int) string {
	if len(args) == baseArgCount+1 {
		return args[baseArgCount]
	}
	return ""
}

func ycsbSuccessWithOptionalReceipt(stub shim.ChaincodeStubInterface, requestID string, operation string, key string, payload []byte) pb.Response {
	if requestID != "" {
		if err := putYCSBReceipt(stub, ycsbReceiptRecord{
			Benchmark: "ycsb",
			RequestID: requestID,
			Operation: operation,
			Key:       key,
			TxID:      stub.GetTxID(),
		}); err != nil {
			return shim.Error(err.Error())
		}
	}
	return shim.Success(payload)
}

func putYCSBReceipt(stub shim.ChaincodeStubInterface, receipt ycsbReceiptRecord) error {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return stub.PutState(ycsbReceiptKey(receipt.RequestID), payload)
}

func marshalYCSBReceiptView(view ycsbReceiptView) pb.Response {
	payload, err := json.Marshal(view)
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(payload)
}

func ycsbReceiptKey(requestID string) string {
	return "receipt:" + requestID
}

func main() {
	if err := shim.Start(new(YCSBChaincode)); err != nil {
		fmt.Printf("Error starting YCSB chaincode: %s", err)
	}
}
