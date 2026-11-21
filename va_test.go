package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	pace.gap = time.Millisecond // không chờ 500ms giữa các request giả
	os.Exit(m.Run())
}

// fakeV2 giả SePay API v2 cho đơn VA, ghi lại body của request tạo đơn.
func fakeV2(t *testing.T, gotBody *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"status":"error","message":"Missing Authorization header","error_code":"unauthorized"}`))
			return
		}
		const order = `{"id":"o-1","order_code":"DH20250001","amount":500000,"paid_amount":0,"status":"Pending","va":[{"va_number":"963NQD1","amount":500000,"status":"Unpaid"}]}`
		switch r.Method + " " + r.URL.Path {
		case "GET /bank-accounts":
			w.Write([]byte(`{"status":"success","data":[{"id":"ba-uuid","account_number":"001"}]}`))
		case "POST /bank-accounts/ba-uuid/orders":
			json.NewDecoder(r.Body).Decode(gotBody)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"success","message":"Order created successfully","data":{"id":"o-1","order_code":"DH20250001","va_number":"963NQD1","amount":500000,"status":"Pending","qr_code":"data:image/png;base64,AAAA","qr_code_url":"https://vietqr.app/img?x"}}`))
		case "GET /bank-accounts/ba-uuid/orders":
			w.Write([]byte(`{"status":"success","data":[` + order + `],"meta":{"pagination":{"total":1,"has_more":false}}}`))
		case "GET /bank-accounts/ba-uuid/orders/o-1":
			w.Write([]byte(`{"status":"success","data":` + order + `}`))
		case "DELETE /bank-accounts/ba-uuid/orders/o-1":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /bank-accounts/ba-uuid/orders/paid":
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"status":"error","message":"Order is not pending","error_code":"invalid_state"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func connect(t *testing.T, c *Client, allowWrite bool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := newServer(c, allowWrite).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, out)
	}
	return res
}

func errText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestVATools(t *testing.T) {
	var body map[string]any
	api := fakeV2(t, &body)
	defer api.Close()
	c := NewClient("tok")
	c.BaseV2 = api.URL
	cs := connect(t, c, true)

	var created VAOrder
	callTool(t, cs, "create_va_order", map[string]any{"account_number": "001", "amount": 500000, "order_code": "DH20250001"}, &created)
	if created.ID != "o-1" || created.VANumber != "963NQD1" || created.QRCodeURL == "" {
		t.Errorf("create: %+v", created)
	}
	if body["amount"] != float64(500000) || body["order_code"] != "DH20250001" || body["with_qrcode"] != "1" || body["tid"] != nil {
		t.Errorf("create body: %v", body)
	}

	var list ListVAOutput
	callTool(t, cs, "list_va_orders", map[string]any{"account_number": "001"}, &list)
	if len(list.Orders) != 1 || list.Orders[0].VA[0].VANumber != "963NQD1" {
		t.Errorf("list: %+v", list)
	}

	var got VAOrder
	callTool(t, cs, "get_va_order", map[string]any{"account_number": "001", "order_id": "o-1"}, &got)
	if got.Status != "Pending" || got.Amount != 500000 {
		t.Errorf("get: %+v", got)
	}

	var cancel CancelOutput
	callTool(t, cs, "cancel_va_order", map[string]any{"account_number": "001", "order_id": "o-1"}, &cancel)
	if !cancel.Cancelled {
		t.Errorf("cancel: %+v", cancel)
	}

	// Lỗi v2 hiện error_code để AI giải thích được.
	res := callTool(t, cs, "cancel_va_order", map[string]any{"account_number": "001", "order_id": "paid"}, nil)
	if !res.IsError || !strings.Contains(errText(res), "invalid_state") {
		t.Errorf("cancel paid: %v %q", res.IsError, errText(res))
	}
	res = callTool(t, cs, "get_va_order", map[string]any{"account_number": "999", "order_id": "o-1"}, nil)
	if !res.IsError || !strings.Contains(errText(res), "999") {
		t.Errorf("unknown account: %q", errText(res))
	}
}

// Mặc định không có tool ghi dữ liệu.
func TestWriteToolsOff(t *testing.T) {
	cs := connect(t, NewClient("tok"), false)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s should be read-only", tl.Name)
		}
	}
	if s := strings.Join(names, ","); strings.Contains(s, "create_va_order") || strings.Contains(s, "cancel_va_order") || !strings.Contains(s, "get_va_order") {
		t.Errorf("tools: %s", s)
	}
}

type authRT struct{ token string }

func (a authRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(r)
}

// Chế độ HTTP: thiếu token -> 401; token của từng request được chuyển tiếp tới SePay.
func TestHTTP(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"status":200,"bankaccounts":[{"id":"19","account_number":"001"}]}`))
	}))
	defer api.Close()
	srv := httptest.NewServer(httpHandler(false, func(tok string) *Client {
		c := NewClient(tok)
		c.BaseURL = api.URL
		return c
	}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: got %d", resp.StatusCode)
	}

	for tok, wantErr := range map[string]bool{"tok": false, "bad": true} {
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(),
			&mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: authRT{tok}}}, nil)
		if err != nil {
			t.Fatalf("connect %s: %v", tok, err)
		}
		var out AccountsOutput
		res := callTool(t, cs, "list_bank_accounts", nil, &out)
		if res.IsError != wantErr || (!wantErr && len(out.BankAccounts) != 1) {
			t.Errorf("token %s: isError=%v out=%+v", tok, res.IsError, out)
		}
		cs.Close()
	}
}
