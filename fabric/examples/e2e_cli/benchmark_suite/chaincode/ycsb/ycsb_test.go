package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	pb "github.com/hyperledger/fabric/protos/peer"
)

func newYCSBStub() *shim.MockStub {
	cc := new(YCSBChaincode)
	return shim.NewMockStub("ycsb", cc)
}

func requireYCSBOK(t *testing.T, res pb.Response) {
	t.Helper()
	if res.Status != shim.OK {
		t.Fatalf("expected OK, got status=%d message=%s", res.Status, res.Message)
	}
}

func requireYCSBErrorContains(t *testing.T, res pb.Response, part string) {
	t.Helper()
	if res.Status != shim.ERROR {
		t.Fatalf("expected ERROR, got status=%d payload=%s", res.Status, string(res.Payload))
	}
	if !strings.Contains(res.Message, part) {
		t.Fatalf("expected error containing %q, got %q", part, res.Message)
	}
}

func TestInsertReadUpdate(t *testing.T) {
	stub := newYCSBStub()
	requireYCSBOK(t, stub.MockInit("1", nil))

	inserted := stub.MockInvoke("2", [][]byte{[]byte("Insert"), []byte("user000001"), []byte(`{"value":"alpha"}`)})
	requireYCSBOK(t, inserted)

	read := stub.MockInvoke("3", [][]byte{[]byte("Read"), []byte("user000001")})
	requireYCSBOK(t, read)
	if string(read.Payload) != `{"key":"user000001","value":"alpha"}` {
		t.Fatalf("unexpected read payload: %s", string(read.Payload))
	}

	updated := stub.MockInvoke("4", [][]byte{[]byte("Update"), []byte("user000001"), []byte(`{"value":"beta"}`)})
	requireYCSBOK(t, updated)

	readUpdated := stub.MockInvoke("5", [][]byte{[]byte("Read"), []byte("user000001")})
	requireYCSBOK(t, readUpdated)
	if string(readUpdated.Payload) != `{"key":"user000001","value":"beta"}` {
		t.Fatalf("unexpected updated payload: %s", string(readUpdated.Payload))
	}
}

func TestPingAndReceipt(t *testing.T) {
	stub := newYCSBStub()
	requireYCSBOK(t, stub.MockInit("1", nil))

	inserted := stub.MockInvoke("2", [][]byte{
		[]byte("Insert"),
		[]byte("user000001"),
		[]byte(`{"value":"alpha"}`),
		[]byte("load-1"),
	})
	requireYCSBOK(t, inserted)

	ping := stub.MockInvoke("3", [][]byte{[]byte("Ping")})
	requireYCSBOK(t, ping)
	if string(ping.Payload) != `{"benchmark":"ycsb","status":"ok"}` {
		t.Fatalf("unexpected ping payload: %s", string(ping.Payload))
	}

	receipt := stub.MockInvoke("4", [][]byte{[]byte("GetReceipt"), []byte("load-1")})
	requireYCSBOK(t, receipt)

	var payload map[string]interface{}
	if err := json.Unmarshal(receipt.Payload, &payload); err != nil {
		t.Fatalf("failed to decode receipt: %v", err)
	}
	if payload["found"] != true {
		t.Fatalf("expected receipt to be found: %#v", payload)
	}
	receiptPayload, ok := payload["receipt"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected receipt payload shape: %#v", payload["receipt"])
	}
	if receiptPayload["operation"] != "Insert" {
		t.Fatalf("unexpected receipt operation: %#v", receiptPayload["operation"])
	}
}

func TestScanReturnsOrderedRange(t *testing.T) {
	stub := newYCSBStub()
	requireYCSBOK(t, stub.MockInit("1", nil))

	requireYCSBOK(t, stub.MockInvoke("2", [][]byte{[]byte("Insert"), []byte("user000001"), []byte(`{"value":"a"}`)}))
	requireYCSBOK(t, stub.MockInvoke("3", [][]byte{[]byte("Insert"), []byte("user000002"), []byte(`{"value":"b"}`)}))
	requireYCSBOK(t, stub.MockInvoke("4", [][]byte{[]byte("Insert"), []byte("user000003"), []byte(`{"value":"c"}`)}))

	scanned := stub.MockInvoke("5", [][]byte{[]byte("Scan"), []byte("user000001"), []byte("2")})
	requireYCSBOK(t, scanned)
	if string(scanned.Payload) != `[{"key":"user000001","value":"a"},{"key":"user000002","value":"b"}]` {
		t.Fatalf("unexpected scan payload: %s", string(scanned.Payload))
	}
}

func TestScanSkipsReceiptKeys(t *testing.T) {
	stub := newYCSBStub()
	requireYCSBOK(t, stub.MockInit("1", nil))

	requireYCSBOK(t, stub.MockInvoke("2", [][]byte{[]byte("Insert"), []byte("user000001"), []byte(`{"value":"a"}`), []byte("load-1")}))
	requireYCSBOK(t, stub.MockInvoke("3", [][]byte{[]byte("Insert"), []byte("user000002"), []byte(`{"value":"b"}`)}))

	scanned := stub.MockInvoke("4", [][]byte{[]byte("Scan"), []byte("user000001"), []byte("2")})
	requireYCSBOK(t, scanned)
	if strings.Contains(string(scanned.Payload), `"key":"receipt:`) {
		t.Fatalf("scan payload should not contain receipt keys: %s", string(scanned.Payload))
	}
}

func TestInsertRejectsDuplicateKey(t *testing.T) {
	stub := newYCSBStub()
	requireYCSBOK(t, stub.MockInit("1", nil))
	requireYCSBOK(t, stub.MockInvoke("2", [][]byte{[]byte("Insert"), []byte("user1"), []byte(`{"value":"a"}`)}))

	duplicate := stub.MockInvoke("3", [][]byte{[]byte("Insert"), []byte("user1"), []byte(`{"value":"b"}`)})
	requireYCSBErrorContains(t, duplicate, "already exists")
}

func TestReadAndUpdateRejectMissingKey(t *testing.T) {
	stub := newYCSBStub()
	requireYCSBOK(t, stub.MockInit("1", nil))

	read := stub.MockInvoke("2", [][]byte{[]byte("Read"), []byte("missing")})
	requireYCSBErrorContains(t, read, "not found")

	update := stub.MockInvoke("3", [][]byte{[]byte("Update"), []byte("missing"), []byte(`{"value":"beta"}`)})
	requireYCSBErrorContains(t, update, "not found")
}
