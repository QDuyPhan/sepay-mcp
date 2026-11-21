package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Đơn hàng thanh toán qua tài khoản ảo (VA), SePay API v2.
// Docs: https://developer.sepay.vn/vi/sepay-api/v2/don-hang/bat-dau-nhanh
// Hỗ trợ BIDV, Sacombank, Vietcombank, VietinBank, Shinhan; mỗi ngân hàng có điều kiện riêng (xem README).

type VA struct {
	VANumber  string `json:"va_number"`
	Amount    int64  `json:"amount"`
	Status    string `json:"status" jsonschema:"Unpaid, Paid hoặc Cancelled"`
	ExpiredAt string `json:"expired_at,omitempty"`
	PaidAt    string `json:"paid_at,omitempty"`
}

type VAOrder struct {
	ID                string `json:"id" jsonschema:"ID đơn (UUID), dùng cho get_va_order/cancel_va_order"`
	OrderCode         string `json:"order_code"`
	Amount            int64  `json:"amount"`
	PaidAmount        int64  `json:"paid_amount,omitempty"`
	Status            string `json:"status" jsonschema:"Pending, Paid, Partially hoặc Cancelled"`
	BankName          string `json:"bank_name,omitempty"`
	AccountNumber     string `json:"account_number,omitempty"`
	AccountHolderName string `json:"account_holder_name,omitempty"`
	VANumber          string `json:"va_number,omitempty" jsonschema:"Số tài khoản ảo khách chuyển vào (khi vừa tạo)"`
	ExpiredAt         string `json:"expired_at,omitempty"`
	QRCodeURL         string `json:"qr_code_url,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	VA                []VA   `json:"va,omitempty"`
}

type VAAccountInput struct {
	AccountNumber string `json:"account_number" jsonschema:"Số tài khoản ngân hàng chính (đã liên kết SePay) mà đơn VA thuộc về"`
}

type VAOrderRef struct {
	AccountNumber string `json:"account_number" jsonschema:"Số tài khoản ngân hàng chính của đơn"`
	OrderID       string `json:"order_id" jsonschema:"ID đơn (UUID) trả về khi tạo hoặc từ list_va_orders"`
}

type ListVAInput struct {
	AccountNumber string `json:"account_number" jsonschema:"Số tài khoản ngân hàng chính"`
	Page          int    `json:"page,omitempty" jsonschema:"Trang, mặc định 1 (50 đơn/trang, mới nhất trước)"`
}

type ListVAOutput struct {
	Orders  []VAOrder `json:"orders"`
	HasMore bool      `json:"has_more"`
}

type CreateVAInput struct {
	AccountNumber string `json:"account_number" jsonschema:"Số tài khoản ngân hàng chính (đã liên kết SePay)"`
	Amount        int64  `json:"amount,omitempty" jsonschema:"Số tiền (VND). Bắt buộc trừ BIDV (bỏ trống = đơn mở, nhận số tiền bất kỳ)"`
	OrderCode     string `json:"order_code,omitempty" jsonschema:"Mã đơn 6-50 ký tự chữ/số (Vietcombank, Shinhan: tối đa 15). Bỏ trống để SePay tự sinh"`
	Duration      int    `json:"duration,omitempty" jsonschema:"Thời hạn thanh toán (giây). Mặc định tùy ngân hàng"`
	TID           string `json:"tid,omitempty" jsonschema:"Chỉ Vietcombank (bắt buộc): Terminal ID của Vietcombank"`
	VAPrefix      string `json:"va_prefix,omitempty" jsonschema:"Chỉ Sacombank (bắt buộc): tiền tố VA, ví dụ SEP200001WEB"`
}

func addVATools(s *mcp.Server, c *Client, allowWrite bool) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_va_orders",
		Description: "Liệt kê đơn hàng thanh toán qua tài khoản ảo (VA) của một tài khoản ngân hàng, kèm trạng thái đã trả chưa. " +
			"List virtual-account (VA) payment orders.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListVAInput) (*mcp.CallToolResult, ListVAOutput, error) {
		ba, err := c.bankAccountXID(ctx, in.AccountNumber)
		if err != nil {
			return nil, ListVAOutput{}, err
		}
		q := url.Values{"per_page": {"50"}, "page": {strconv.Itoa(max(in.Page, 1))}}
		var r struct {
			Data []VAOrder `json:"data"`
			Meta struct {
				Pagination struct {
					HasMore bool `json:"has_more"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if err := c.v2(ctx, http.MethodGet, "/bank-accounts/"+ba+"/orders?"+q.Encode(), nil, &r); err != nil {
			return nil, ListVAOutput{}, err
		}
		if r.Data == nil {
			r.Data = []VAOrder{}
		}
		return nil, ListVAOutput{Orders: r.Data, HasMore: r.Meta.Pagination.HasMore}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_va_order",
		Description: "Xem trạng thái một đơn VA: đã trả chưa, đã trả bao nhiêu, các VA của đơn. Get one VA order's payment status.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in VAOrderRef) (*mcp.CallToolResult, VAOrder, error) {
		var r struct {
			Data VAOrder `json:"data"`
		}
		err := c.vaOrderCall(ctx, http.MethodGet, in, nil, &r)
		return nil, r.Data, err
	})

	if !allowWrite {
		return
	}

	no, yes := false, true
	mcp.AddTool(s, &mcp.Tool{
		Name: "create_va_order",
		Description: "TẠO đơn hàng thanh toán qua tài khoản ảo (VA): SePay cấp một số tài khoản riêng cho đơn, khách chuyển vào là khớp chính xác. " +
			"Thao tác ghi dữ liệu thật trên SePay, hãy xác nhận với người dùng trước khi gọi. Create a VA payment order (write).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateVAInput) (*mcp.CallToolResult, VAOrder, error) {
		if in.Amount < 0 || in.Duration < 0 {
			return nil, VAOrder{}, fmt.Errorf("amount và duration không được âm")
		}
		ba, err := c.bankAccountXID(ctx, in.AccountNumber)
		if err != nil {
			return nil, VAOrder{}, err
		}
		body := map[string]any{"with_qrcode": "1"}
		if in.Amount > 0 {
			body["amount"] = in.Amount
		}
		for k, v := range map[string]string{"order_code": in.OrderCode, "tid": in.TID, "va_prefix": in.VAPrefix} {
			if v != "" {
				body[k] = v
			}
		}
		if in.Duration > 0 {
			body["duration"] = in.Duration
		}
		var r struct {
			Data VAOrder `json:"data"`
		}
		err = c.v2(ctx, http.MethodPost, "/bank-accounts/"+ba+"/orders", body, &r)
		return nil, r.Data, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "cancel_va_order",
		Description: "HỦY một đơn VA đang chờ thanh toán (Pending); các VA chưa trả của đơn bị hủy theo. Không hoàn tác được, hãy xác nhận với người dùng trước. " +
			"Cancel a pending VA order (destructive).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in VAOrderRef) (*mcp.CallToolResult, CancelOutput, error) {
		if err := c.vaOrderCall(ctx, http.MethodDelete, in, nil, nil); err != nil {
			return nil, CancelOutput{}, err
		}
		return nil, CancelOutput{Cancelled: true, OrderID: in.OrderID}, nil
	})
}

type CancelOutput struct {
	Cancelled bool   `json:"cancelled"`
	OrderID   string `json:"order_id"`
}

func (c *Client) vaOrderCall(ctx context.Context, method string, in VAOrderRef, body, out any) error {
	if in.OrderID == "" {
		return fmt.Errorf("cần order_id")
	}
	ba, err := c.bankAccountXID(ctx, in.AccountNumber)
	if err != nil {
		return err
	}
	return c.v2(ctx, method, "/bank-accounts/"+ba+"/orders/"+url.PathEscape(in.OrderID), body, out)
}

// bankAccountXID tìm UUID tài khoản (API v2 dùng UUID, không dùng số tài khoản).
func (c *Client) bankAccountXID(ctx context.Context, accountNumber string) (string, error) {
	if accountNumber == "" {
		return "", fmt.Errorf("cần account_number")
	}
	var r struct {
		Data []struct {
			ID            string `json:"id"`
			AccountNumber string `json:"account_number"`
		} `json:"data"`
	}
	// ponytail: chỉ xem 100 tài khoản đầu; phân trang khi có công ty liên kết hơn 100 tài khoản.
	if err := c.v2(ctx, http.MethodGet, "/bank-accounts?per_page=100", nil, &r); err != nil {
		return "", err
	}
	for _, a := range r.Data {
		if a.AccountNumber == accountNumber {
			return url.PathEscape(a.ID), nil
		}
	}
	return "", fmt.Errorf("không thấy tài khoản %s trong SePay", accountNumber)
}
