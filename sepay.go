package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Client gọi SePay User API v1 (giao dịch, tài khoản) và v2 (đơn hàng VA).
// Docs: https://docs.sepay.vn/gioi-thieu-api.html, https://developer.sepay.vn/vi/sepay-api/v2/gioi-thieu
type Client struct {
	BaseURL string // v1
	BaseV2  string
	Token   string
	HTTP    *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		BaseURL: "https://my.sepay.vn/userapi",
		BaseV2:  "https://userapi.sepay.vn/v2",
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type Transaction struct {
	ID                 string `json:"id"`
	BankBrandName      string `json:"bank_brand_name"`
	AccountNumber      string `json:"account_number"`
	SubAccount         string `json:"sub_account,omitempty"`
	TransactionDate    string `json:"transaction_date"`
	AmountIn           string `json:"amount_in"`
	AmountOut          string `json:"amount_out"`
	Accumulated        string `json:"accumulated"`
	TransactionContent string `json:"transaction_content"`
	ReferenceNumber    string `json:"reference_number"`
	Code               string `json:"code,omitempty"`
}

type BankAccount struct {
	ID                string `json:"id"`
	AccountHolderName string `json:"account_holder_name"`
	AccountNumber     string `json:"account_number"`
	Accumulated       string `json:"accumulated"`
	LastTransaction   string `json:"last_transaction"`
	Label             string `json:"label"`
	Active            string `json:"active"`
	BankShortName     string `json:"bank_short_name"`
	BankCode          string `json:"bank_code"`
	BankBin           string `json:"bank_bin"`
}

func (c *Client) ListTransactions(ctx context.Context, q url.Values) ([]Transaction, error) {
	var r struct {
		Transactions []Transaction `json:"transactions"`
	}
	return r.Transactions, c.do(ctx, http.MethodGet, c.BaseURL+"/transactions/list?"+q.Encode(), nil, &r)
}

func (c *Client) GetTransaction(ctx context.Context, id string) (Transaction, error) {
	var r struct {
		Transaction Transaction `json:"transaction"`
	}
	return r.Transaction, c.do(ctx, http.MethodGet, c.BaseURL+"/transactions/details/"+url.PathEscape(id), nil, &r)
}

func (c *Client) ListBankAccounts(ctx context.Context) ([]BankAccount, error) {
	var r struct {
		BankAccounts []BankAccount `json:"bankaccounts"`
	}
	return r.BankAccounts, c.do(ctx, http.MethodGet, c.BaseURL+"/bankaccounts/list", nil, &r)
}

// v2 gọi API v2; path đã gồm query nếu có.
func (c *Client) v2(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, c.BaseV2+path, body, out)
}

func (c *Client) do(ctx context.Context, method, u string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		if err := pace.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("không kết nối được SePay: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("không đọc được phản hồi SePay: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			secs, _ := strconv.Atoi(resp.Header.Get("x-sepay-userapi-retry-after")) // v1
			if secs == 0 {
				secs, _ = strconv.Atoi(resp.Header.Get("Retry-After")) // v2
			}
			if err := sleep(ctx, time.Duration(max(secs, 1))*time.Second); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return apiError(resp.StatusCode, data)
		}
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("không đọc được phản hồi SePay: %w", err)
		}
		return nil
	}
}

// apiError đổi lỗi HTTP thành câu dễ hiểu; v2 có body {"message","error_code"}.
func apiError(status int, body []byte) error {
	var e struct {
		Message   string `json:"message"`
		ErrorCode string `json:"error_code"`
	}
	json.Unmarshal(body, &e)
	detail := ""
	if e.Message != "" || e.ErrorCode != "" {
		detail = fmt.Sprintf(": %s %s", e.ErrorCode, e.Message)
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if e.ErrorCode == "" || e.ErrorCode == "unauthorized" {
			return fmt.Errorf("SePay từ chối API token (HTTP %d). Kiểm tra token", status)
		}
	case http.StatusTooManyRequests:
		return fmt.Errorf("SePay báo gọi quá nhanh (HTTP 429), thử lại sau vài giây")
	case http.StatusNotFound:
		return fmt.Errorf("SePay: không tìm thấy (HTTP 404)%s", detail)
	}
	return fmt.Errorf("SePay trả lỗi HTTP %d%s", status, detail)
}

// pace giãn mọi request cách nhau tối thiểu 500ms: SePay giới hạn theo IP (v1: 2/s, v2: 3/s),
// nên dùng chung cho mọi token/phiên trong process.
// ponytail: chỉ trong 1 process; chạy nhiều instance sau cùng IP vẫn có thể bị 429 (đã có retry).
var pace = &pacer{gap: 500 * time.Millisecond}

type pacer struct {
	gap  time.Duration
	mu   sync.Mutex
	last time.Time
}

func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	next := p.last.Add(p.gap)
	if now := time.Now(); next.Before(now) {
		next = now
	}
	p.last = next
	p.mu.Unlock()
	return sleep(ctx, time.Until(next))
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
