package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	pb "github.com/hyperledger/fabric/protos/peer"
)

func newSmallbankStub() *shim.MockStub {
	cc := new(SmallbankChaincode)
	return shim.NewMockStub("smallbank", cc)
}

func requireOK(t *testing.T, res pb.Response) {
	t.Helper()
	if res.Status != shim.OK {
		t.Fatalf("expected OK, got status=%d message=%s", res.Status, res.Message)
	}
}

func requireErrorContains(t *testing.T, res pb.Response, part string) {
	t.Helper()
	if res.Status != shim.ERROR {
		t.Fatalf("expected ERROR, got status=%d payload=%s", res.Status, string(res.Payload))
	}
	if !strings.Contains(res.Message, part) {
		t.Fatalf("expected error containing %q, got %q", part, res.Message)
	}
}

func TestCreateAccountAndBalance(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-0001"),
		[]byte("alice"),
		[]byte("1000"),
		[]byte("2000"),
	}))

	response := stub.MockInvoke("3", [][]byte{
		[]byte("Balance"),
		[]byte("acct-0001"),
	})
	requireOK(t, response)

	expected := `{"account_id":"acct-0001","checking":1000,"savings":2000}`
	if string(response.Payload) != expected {
		t.Fatalf("unexpected balance payload: got %s want %s", string(response.Payload), expected)
	}
}

func TestPingAndReceipt(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-0001"),
		[]byte("alice"),
		[]byte("1000"),
		[]byte("2000"),
		[]byte("load-1"),
	}))

	ping := stub.MockInvoke("3", [][]byte{[]byte("Ping")})
	requireOK(t, ping)
	if string(ping.Payload) != `{"benchmark":"smallbank","status":"ok"}` {
		t.Fatalf("unexpected ping payload: %s", string(ping.Payload))
	}

	receipt := stub.MockInvoke("4", [][]byte{[]byte("GetReceipt"), []byte("load-1")})
	requireOK(t, receipt)

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
	if receiptPayload["operation"] != "CreateAccount" {
		t.Fatalf("unexpected receipt operation: %#v", receiptPayload["operation"])
	}
}

func TestSendPaymentMovesFunds(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-1"),
		[]byte("alice"),
		[]byte("1000"),
		[]byte("1000"),
	}))
	requireOK(t, stub.MockInvoke("3", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-2"),
		[]byte("bob"),
		[]byte("1000"),
		[]byte("1000"),
	}))

	requireOK(t, stub.MockInvoke("4", [][]byte{
		[]byte("SendPayment"),
		[]byte("acct-1"),
		[]byte("acct-2"),
		[]byte("250"),
	}))

	left := stub.MockInvoke("5", [][]byte{[]byte("Balance"), []byte("acct-1")})
	right := stub.MockInvoke("6", [][]byte{[]byte("Balance"), []byte("acct-2")})
	requireOK(t, left)
	requireOK(t, right)

	if string(left.Payload) != `{"account_id":"acct-1","checking":750,"savings":1000}` {
		t.Fatalf("unexpected sender balance: %s", string(left.Payload))
	}
	if string(right.Payload) != `{"account_id":"acct-2","checking":1250,"savings":1000}` {
		t.Fatalf("unexpected recipient balance: %s", string(right.Payload))
	}
}

func TestTransactSavingsRejectsInsufficientFunds(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-1"),
		[]byte("alice"),
		[]byte("100"),
		[]byte("100"),
	}))

	response := stub.MockInvoke("3", [][]byte{
		[]byte("TransactSavings"),
		[]byte("acct-1"),
		[]byte("-250"),
	})
	requireErrorContains(t, response, "insufficient")
}

func TestWriteCheckAllowsCheckingOverdraftWhenTotalFundsSuffice(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-1"),
		[]byte("alice"),
		[]byte("100"),
		[]byte("200"),
	}))

	requireOK(t, stub.MockInvoke("3", [][]byte{
		[]byte("WriteCheck"),
		[]byte("acct-1"),
		[]byte("250"),
		[]byte("workload-3"),
	}))

	balance := stub.MockInvoke("4", [][]byte{[]byte("Balance"), []byte("acct-1")})
	requireOK(t, balance)
	if string(balance.Payload) != `{"account_id":"acct-1","checking":-150,"savings":200}` {
		t.Fatalf("unexpected balance payload: %s", string(balance.Payload))
	}
}

func TestWriteCheckChargesPenaltyWhenTotalFundsDoNotSuffice(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-1"),
		[]byte("alice"),
		[]byte("100"),
		[]byte("100"),
	}))

	requireOK(t, stub.MockInvoke("3", [][]byte{
		[]byte("WriteCheck"),
		[]byte("acct-1"),
		[]byte("250"),
		[]byte("workload-3"),
	}))

	balance := stub.MockInvoke("4", [][]byte{[]byte("Balance"), []byte("acct-1")})
	requireOK(t, balance)
	if string(balance.Payload) != `{"account_id":"acct-1","checking":-151,"savings":100}` {
		t.Fatalf("unexpected balance payload: %s", string(balance.Payload))
	}
}

func TestTransactSavingsSupportsSignedDelta(t *testing.T) {
	stub := newSmallbankStub()

	requireOK(t, stub.MockInit("1", nil))
	requireOK(t, stub.MockInvoke("2", [][]byte{
		[]byte("CreateAccount"),
		[]byte("acct-1"),
		[]byte("alice"),
		[]byte("100"),
		[]byte("200"),
	}))

	requireOK(t, stub.MockInvoke("3", [][]byte{
		[]byte("TransactSavings"),
		[]byte("acct-1"),
		[]byte("-50"),
		[]byte("workload-3"),
	}))
	requireOK(t, stub.MockInvoke("4", [][]byte{
		[]byte("TransactSavings"),
		[]byte("acct-1"),
		[]byte("25"),
		[]byte("workload-4"),
	}))

	balance := stub.MockInvoke("5", [][]byte{[]byte("Balance"), []byte("acct-1")})
	requireOK(t, balance)
	if string(balance.Payload) != `{"account_id":"acct-1","checking":100,"savings":175}` {
		t.Fatalf("unexpected balance payload: %s", string(balance.Payload))
	}
}
