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
			if r.URL.Query().Get("reference_number") == "nope" {
				w.Write([]byte(`{"status":200,"transactions":[]}`))
				return
			}
			w.Write([]byte(fakeList))
		case "/transactions/details/3":
			w.Write([]byte(`{"status":200,"transaction":{"id":"3","amount_in":"500000.00","reference_number":"r3"}}`))
		case "/transactions/details/404":
			w.Write([]byte(`{"status":200,"transaction":null}`))
		case "/bankaccounts/list":
			w.Write([]byte(`{"status":200,"bankaccounts":[{"id":"19","account_number":"001","accumulated":"1000.00","bank_short_name":"Vietcombank"}]}`))
		}
	}))
	defer api.Close()

	c := NewClient("tok")
	c.BaseURL = api.URL
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := newServer(c, true).Connect(ctx, st, nil); err != nil {
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

	var got Transaction
	call("get_transaction", map[string]any{"id": "3"}, &got)
	if got.ID != "3" || got.AmountIn != "500000.00" {
		t.Errorf("get_transaction by id: %+v", got)
	}
	got = Transaction{}
	call("get_transaction", map[string]any{"reference_number": "r3"}, &got)
	if got.ID != "3" || !strings.Contains(gotQuery, "reference_number=r3") {
		t.Errorf("get_transaction by ref: %+v, query %s", got, gotQuery)
	}
	for _, args := range []map[string]any{{"id": "404"}, {"reference_number": "nope"}, {}, {"id": "3", "reference_number": "r3"}} {
		if res := call("get_transaction", args, nil); !res.IsError {
			t.Errorf("get_transaction %v: expected error", args)
		}
	}

	var rec ReconcileOutput
	call("reconcile_orders", map[string]any{"orders": []map[string]any{{"code": "DH123", "amount": 500000}}}, &rec)
	if rec.Paid != 1 || len(rec.Unmatched) != 1 {
		t.Errorf("reconcile_orders: %+v", rec)
	}
	if res := call("reconcile_orders", map[string]any{"orders": []map[string]any{}}, nil); !res.IsError {
		t.Error("expected error for empty orders")
	}

	// Sai token -> lỗi dễ hiểu, không crash.
	c.Token = "bad"
	if res := call("list_bank_accounts", nil, nil); !res.IsError {
		t.Error("expected tool error for bad token")
	}
}

func TestReconcile(t *testing.T) {
	tx := func(id, amt, content string) Transaction {
		return Transaction{ID: id, AmountIn: amt, AmountOut: "0.00", TransactionContent: content}
	}
	txs := []Transaction{
		tx("1", "300000.00", "MOMO chuyen tien dh 123 cam on"),
		tx("2", "200000.00", "DH123 lan 2"),                                              // trả 2 lần -> cộng dồn
		tx("3", "50000.00", "DH45"),                                                      // thiếu
		tx("4", "999000.00", "DH7"),                                                      // dư
		tx("5", "10000.00", "tien la"),                                                   // không khớp đơn nào
		{ID: "6", AmountIn: "0.00", AmountOut: "500000.00", TransactionContent: "DH123"}, // tiền ra, bỏ qua
	}
	out := reconcile([]Order{
		{"DH123", 500000}, {"DH12", 100000}, {"DH45", 100000}, {"DH7", 500000},
	}, txs)

	want := map[string]string{"DH123": "paid", "DH12": "unpaid", "DH45": "partial", "DH7": "overpaid"}
	for _, r := range out.Orders {
		if r.Status != want[r.Code] {
			t.Errorf("%s: got %s (received %d), want %s", r.Code, r.Status, r.Received, want[r.Code])
		}
	}
	if out.Paid != 1 || out.Unpaid != 1 || out.Partial != 1 || out.Overpaid != 1 {
		t.Errorf("counts: %+v", out)
	}
	if len(out.Unmatched) != 1 || out.Unmatched[0].ID != "5" {
		t.Errorf("unmatched: %+v", out.Unmatched)
	}
	if !hasCode("abc DH12 xyz", "DH12") || hasCode("DH123", "DH12") || !hasCode("DH12ABC", "DH12") {
		t.Error("hasCode boundary")
	}
	if validateOrders(nil) == nil || validateOrders([]Order{{"DH1", 0}}) == nil {
		t.Error("validateOrders should reject empty/zero")
	}
}
