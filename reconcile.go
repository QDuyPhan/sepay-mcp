package main

import (
	"fmt"
	"slices"
	"unicode"
)

type Order struct {
	Code   string `json:"code" jsonschema:"Mã đơn khách ghi trong nội dung chuyển khoản, ví dụ DH123"`
	Amount int64  `json:"amount" jsonschema:"Số tiền đơn hàng (VND)"`
}

type ReconcileInput struct {
	Orders        []Order `json:"orders" jsonschema:"Danh sách đơn cần đối soát"`
	From          string  `json:"from,omitempty" jsonschema:"Tìm giao dịch từ ngày, dạng YYYY-MM-DD. Bỏ trống = 5000 giao dịch gần nhất"`
	To            string  `json:"to,omitempty" jsonschema:"Đến ngày (tính cả ngày đó), dạng YYYY-MM-DD"`
	AccountNumber string  `json:"account_number,omitempty" jsonschema:"Chỉ đối soát với số tài khoản này"`
}

type OrderResult struct {
	Code     string   `json:"code"`
	Amount   int64    `json:"amount"`
	Received int64    `json:"received"`
	Status   string   `json:"status" jsonschema:"paid = đủ, partial = thiếu, overpaid = dư, unpaid = chưa trả"`
	TxIDs    []string `json:"transaction_ids,omitempty"`
}

type ReconcileOutput struct {
	Paid      int           `json:"paid"`
	Partial   int           `json:"partial"`
	Overpaid  int           `json:"overpaid"`
	Unpaid    int           `json:"unpaid"`
	Orders    []OrderResult `json:"orders"`
	Unmatched []Transaction `json:"unmatched_incoming" jsonschema:"Tiền vào không khớp đơn nào (tối đa 50)"`
	Truncated bool          `json:"truncated" jsonschema:"true nếu chạm giới hạn 5000 giao dịch, có thể sót"`
}

func validateOrders(orders []Order) error {
	if len(orders) == 0 {
		return fmt.Errorf("cần ít nhất một đơn trong orders")
	}
	for _, o := range orders {
		if normalize(o.Code) == "" || o.Amount <= 0 {
			return fmt.Errorf("đơn %q cần code và amount > 0", o.Code)
		}
	}
	return nil
}

// reconcile khớp tiền vào với từng đơn theo mã đơn trong nội dung chuyển khoản, cộng dồn nếu khách trả nhiều lần.
// ponytail: một giao dịch ghi 2 mã đơn sẽ được tính cho cả 2 đơn; chia tiền giữa các đơn khi có người cần.
func reconcile(orders []Order, txs []Transaction) ReconcileOutput {
	out := ReconcileOutput{Truncated: len(txs) >= 5000, Orders: []OrderResult{}, Unmatched: []Transaction{}}
	matched := map[string]bool{}
	for _, o := range orders {
		r := OrderResult{Code: o.Code, Amount: o.Amount}
		for _, t := range txs {
			in := vnd(t.AmountIn)
			if in > 0 && hasCode(t.TransactionContent, o.Code) {
				r.Received += in
				r.TxIDs = append(r.TxIDs, t.ID)
				matched[t.ID] = true
			}
		}
		switch {
		case r.Received == 0:
			r.Status, out.Unpaid = "unpaid", out.Unpaid+1
		case r.Received < o.Amount:
			r.Status, out.Partial = "partial", out.Partial+1
		case r.Received > o.Amount:
			r.Status, out.Overpaid = "overpaid", out.Overpaid+1
		default:
			r.Status, out.Paid = "paid", out.Paid+1
		}
		out.Orders = append(out.Orders, r)
	}
	for _, t := range txs {
		if vnd(t.AmountIn) > 0 && !matched[t.ID] && len(out.Unmatched) < 50 {
			out.Unmatched = append(out.Unmatched, t)
		}
	}
	return out
}

// hasCode tìm mã đơn trong nội dung (bỏ qua hoa thường, dấu cách) nhưng không để "DH12" khớp nhầm "DH123":
// ký tự ngay sau mã không được cùng loại (số/chữ) với ký tự cuối của mã.
func hasCode(content, code string) bool {
	c, k := []rune(normalize(content)), []rune(normalize(code))
	if len(k) == 0 {
		return false
	}
	last := k[len(k)-1]
	for i := 0; i+len(k) <= len(c); i++ {
		if !slices.Equal(c[i:i+len(k)], k) {
			continue
		}
		if end := i + len(k); end == len(c) || unicode.IsDigit(c[end]) != unicode.IsDigit(last) {
			return true
		}
	}
	return false
}
