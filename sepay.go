package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Client gọi SePay User API v1. Docs: https://docs.sepay.vn/gioi-thieu-api.html
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	mu   sync.Mutex
	last time.Time
}

// SePay giới hạn 2 request/giây/IP.
const minGap = 500 * time.Millisecond

func NewClient(token string) *Client {
	return &Client{
		BaseURL: "https://my.sepay.vn/userapi",
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
	return r.Transactions, c.get(ctx, "/transactions/list", q, &r)
}

func (c *Client) ListBankAccounts(ctx context.Context) ([]BankAccount, error) {
	var r struct {
		BankAccounts []BankAccount `json:"bankaccounts"`
	}
	return r.BankAccounts, c.get(ctx, "/bankaccounts/list", nil, &r)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("không kết nối được SePay: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			resp.Body.Close()
			secs, _ := strconv.Atoi(resp.Header.Get("x-sepay-userapi-retry-after"))
			if err := sleep(ctx, time.Duration(max(secs, 1))*time.Second); err != nil {
				return err
			}
			continue
		}

		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return fmt.Errorf("SePay từ chối API token (HTTP %d). Kiểm tra SEPAY_API_TOKEN", resp.StatusCode)
		case resp.StatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("SePay báo gọi quá nhanh (HTTP 429), thử lại sau vài giây")
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("SePay trả lỗi HTTP %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("không đọc được phản hồi SePay: %w", err)
		}
		return nil
	}
}

// wait giãn các request cách nhau tối thiểu minGap.
// ponytail: chỉ giãn trong 1 process; nhiều process dùng chung IP vẫn có thể bị 429 (đã có retry).
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(minGap)
	if now := time.Now(); next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
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
