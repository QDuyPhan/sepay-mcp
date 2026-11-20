package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fakeList = `{"status":200,"error":null,"messages":{"success":true},"transactions":[
 {"id":"3","transaction_date":"2023-05-05 19:59:48","account_number":"001","amount_in":"500000.00","amount_out":"0.00","accumulated":"1","transaction_content":"NGUYEN VAN A chuyen tien DH 123","reference_number":"r3","code":null,"bank_brand_name":"Vietcombank","bank_account_id":"19"},
 {"id":"2","transaction_date":"2023-05-05 08:00:00","account_number":"001","amount_in":"0.00","amount_out":"20000.00","accumulated":"1","transaction_content":"phi","reference_number":"r2","code":null,"bank_brand_name":"Vietcombank","bank_account_id":"19"},
 {"id":"1","transaction_date":"2023-05-04 11:59:47","account_number":"001","amount_in":"1200000.00","amount_out":"0.00","accumulated":"1","transaction_content":"DH999","reference_number":"r1","code":null,"bank_brand_name":"Vietcombank","bank_account_id":"19"}]}`

// Chạy server MCP thật qua transport in-memory, SePay giả bằng httptest.
func TestTools(t *testing.T) {
	var gotQuery string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/transactions/list":
			w.Write([]byte(fakeList))
		case "/bankaccounts/list":
			w.Write([]byte(`{"status":200,"bankaccounts":[{"id":"19","account_number":"001","accumulated":"1000.00","bank_short_name":"Vietcombank"}]}`))
		}
	}))
	defer api.Close()

	c := NewClient("tok")
	c.BaseURL = api.URL
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := newServer(c).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	call := func(name string, args map[string]any, out any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out != nil && !res.IsError {
			b, _ := json.Marshal(res.StructuredContent)
			json.Unmarshal(b, out)
		}
		return res
	}

	// Lọc nội dung bỏ qua hoa thường và dấu cách: "dh123" khớp "DH 123".
	var list ListOutput
	call("list_transactions", map[string]any{"content": "dh123", "to": "2023-05-05"}, &list)
	if list.Count != 1 || list.Transactions[0].ID != "3" {
		t.Errorf("content filter: %+v", list)
	}
	if !strings.Contains(gotQuery, "transaction_date_max=2023-05-05+23%3A59%3A59") {
		t.Errorf("to-date not widened to end of day: %s", gotQuery)
	}

	var sum Summary
	call("summarize_revenue", map[string]any{"from": "2023-05-01"}, &sum)
	if sum.TotalIn != 1700000 || sum.TotalOut != 20000 || sum.CountIn != 2 || len(sum.ByDay) != 2 ||
		sum.ByDay[0].Date != "2023-05-04" || sum.LargestIn.ID != "1" {
		t.Errorf("summary: %+v", sum)
	}

	var accs AccountsOutput
	call("list_bank_accounts", nil, &accs)
	if len(accs.BankAccounts) != 1 {
		t.Errorf("accounts: %+v", accs)
	}

	var qr QROutput
	call("create_payment_qr", map[string]any{"account_number": "001", "bank": "VCB", "amount": 50000, "description": "thanh toan DH1"}, &qr)
	if qr.URL != "https://vietqr.app/img?acc=001&amount=50000&bank=VCB&des=thanh%20toan%20DH1" {
		t.Errorf("qr: %s", qr.URL)
	}

	// Sai token -> lỗi dễ hiểu, không crash.
	c.Token = "bad"
	if res := call("list_bank_accounts", nil, nil); !res.IsError {
		t.Error("expected tool error for bad token")
	}
}
