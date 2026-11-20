package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	token := os.Getenv("SEPAY_API_TOKEN")
	if token == "" {
		log.Fatal("thiếu biến môi trường SEPAY_API_TOKEN (tạo token tại https://my.sepay.vn/companyapi)")
	}
	server := newServer(NewClient(token))
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func newServer(c *Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "sepay", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_transactions",
		Description: "Liệt kê giao dịch ngân hàng (tiền vào/ra) theo dõi bởi SePay. " +
			"Dùng để kiểm tra khách đã chuyển khoản chưa: lọc theo số tiền (amount_in) và/hoặc nội dung chuyển khoản (content, ví dụ mã đơn DH123). " +
			"List bank transactions tracked by SePay; filter by date, amount, transfer content.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOutput, error) {
		txs, err := listTransactions(ctx, c, in)
		return nil, ListOutput{Count: len(txs), Transactions: txs}, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "summarize_revenue",
		Description: "Tổng hợp tiền vào/ra trong một khoảng thời gian: tổng tiền, số giao dịch, theo từng ngày, giao dịch vào lớn nhất. " +
			"Summarize money in/out over a date range.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SummaryInput) (*mcp.CallToolResult, Summary, error) {
		q := dateRange(in.From, in.To)
		setIf(q, "account_number", in.AccountNumber)
		q.Set("limit", "5000")
		txs, err := c.ListTransactions(ctx, q)
		if err != nil {
			return nil, Summary{}, err
		}
		return nil, summarize(txs), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_transaction",
		Description: "Xem chi tiết một giao dịch theo ID SePay hoặc mã tham chiếu ngân hàng (mã trên ảnh chụp chuyển khoản của khách). " +
			"Get one transaction by SePay id or bank reference number.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetInput) (*mcp.CallToolResult, Transaction, error) {
		t, err := getTransaction(ctx, c, in)
		return nil, t, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "reconcile_orders",
		Description: "Đối soát hàng loạt đơn hàng với tiền chuyển khoản: mỗi đơn (mã đơn + số tiền) trả về đã trả đủ / thiếu / dư / chưa trả, " +
			"cộng dồn nếu khách chuyển nhiều lần, kèm danh sách tiền vào không khớp đơn nào. " +
			"Reconcile orders against incoming bank transfers by order code in the transfer content.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReconcileInput) (*mcp.CallToolResult, ReconcileOutput, error) {
		if err := validateOrders(in.Orders); err != nil {
			return nil, ReconcileOutput{}, err
		}
		q := dateRange(in.From, in.To)
		setIf(q, "account_number", in.AccountNumber)
		q.Set("limit", "5000")
		txs, err := c.ListTransactions(ctx, q)
		if err != nil {
			return nil, ReconcileOutput{}, err
		}
		return nil, reconcile(in.Orders, txs), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_bank_accounts",
		Description: "Liệt kê các tài khoản ngân hàng đã liên kết với SePay, kèm số dư. List linked bank accounts with balances.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, AccountsOutput, error) {
		accs, err := c.ListBankAccounts(ctx)
		return nil, AccountsOutput{BankAccounts: accs}, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_payment_qr",
		Description: "Tạo link ảnh mã VietQR để khách quét chuyển khoản đúng số tiền và nội dung. Không gọi API, không tốn giao dịch. " +
			"Build a VietQR image URL for a bank transfer.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in QRInput) (*mcp.CallToolResult, QROutput, error) {
		if in.AccountNumber == "" || in.Bank == "" {
			return nil, QROutput{}, fmt.Errorf("cần account_number và bank")
		}
		return nil, QROutput{URL: qrURL(in)}, nil
	})

	return s
}

type ListInput struct {
	From          string `json:"from,omitempty" jsonschema:"Từ ngày, dạng YYYY-MM-DD hoặc YYYY-MM-DD HH:MM:SS"`
	To            string `json:"to,omitempty" jsonschema:"Đến ngày (tính cả ngày đó), dạng YYYY-MM-DD hoặc YYYY-MM-DD HH:MM:SS"`
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Chỉ lấy giao dịch của số tài khoản này"`
	AmountIn      int64  `json:"amount_in,omitempty" jsonschema:"Số tiền vào chính xác (VND), ví dụ 500000"`
	Content       string `json:"content,omitempty" jsonschema:"Chuỗi cần có trong nội dung chuyển khoản, không phân biệt hoa thường/dấu cách, ví dụ DH123"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Số giao dịch tối đa trả về, mặc định 50"`
}

type ListOutput struct {
	Count        int           `json:"count"`
	Transactions []Transaction `json:"transactions"`
}

type GetInput struct {
	ID              string `json:"id,omitempty" jsonschema:"ID giao dịch trong SePay"`
	ReferenceNumber string `json:"reference_number,omitempty" jsonschema:"Mã tham chiếu ngân hàng, ví dụ 731086.040523.120001"`
}

func getTransaction(ctx context.Context, c *Client, in GetInput) (Transaction, error) {
	var t Transaction
	switch {
	case (in.ID == "") == (in.ReferenceNumber == ""):
		return t, fmt.Errorf("cần đúng một trong hai: id hoặc reference_number")
	case in.ID != "":
		var err error
		if t, err = c.GetTransaction(ctx, in.ID); err != nil {
			return t, err
		}
	default:
		txs, err := c.ListTransactions(ctx, url.Values{"reference_number": {in.ReferenceNumber}})
		if err != nil {
			return t, err
		}
		if len(txs) > 0 {
			t = txs[0]
		}
	}
	if t.ID == "" {
		return t, fmt.Errorf("không tìm thấy giao dịch")
	}
	return t, nil
}

type SummaryInput struct {
	From          string `json:"from" jsonschema:"Từ ngày, dạng YYYY-MM-DD"`
	To            string `json:"to,omitempty" jsonschema:"Đến ngày (tính cả ngày đó), dạng YYYY-MM-DD. Bỏ trống = đến hiện tại"`
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Chỉ tính cho số tài khoản này"`
}

type DayTotal struct {
	Date string `json:"date"`
	In   int64  `json:"in"`
	Out  int64  `json:"out"`
}

type Summary struct {
	TotalIn   int64        `json:"total_in"`
	TotalOut  int64        `json:"total_out"`
	CountIn   int          `json:"count_in"`
	CountOut  int          `json:"count_out"`
	ByDay     []DayTotal   `json:"by_day"`
	LargestIn *Transaction `json:"largest_in,omitempty"`
	Truncated bool         `json:"truncated" jsonschema:"true nếu chạm giới hạn 5000 giao dịch, tổng có thể thiếu"`
}

type AccountsOutput struct {
	BankAccounts []BankAccount `json:"bank_accounts"`
}

type QRInput struct {
	AccountNumber string `json:"account_number" jsonschema:"Số tài khoản nhận tiền"`
	Bank          string `json:"bank" jsonschema:"Mã ngân hàng: code (VCB), bin (970436) hoặc tên ngắn (Vietcombank)"`
	Amount        int64  `json:"amount,omitempty" jsonschema:"Số tiền (VND). Bỏ trống để khách tự nhập"`
	Description   string `json:"description,omitempty" jsonschema:"Nội dung chuyển khoản, ví dụ DH123. Tối đa 94 ký tự, dấu tiếng Việt sẽ bị bỏ"`
	Template      string `json:"template,omitempty" jsonschema:"Kiểu ảnh: để trống, compact, qronly hoặc standee"`
}

type QROutput struct {
	URL string `json:"url"`
}

func listTransactions(ctx context.Context, c *Client, in ListInput) ([]Transaction, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	q := dateRange(in.From, in.To)
	setIf(q, "account_number", in.AccountNumber)
	if in.AmountIn > 0 {
		q.Set("amount_in", strconv.FormatInt(in.AmountIn, 10))
	}
	// Lọc nội dung làm phía mình nên phải lấy nhiều rồi mới lọc.
	if in.Content != "" {
		q.Set("limit", "5000")
	} else {
		q.Set("limit", strconv.Itoa(min(limit, 5000)))
	}

	txs, err := c.ListTransactions(ctx, q)
	if err != nil {
		return nil, err
	}
	if in.Content != "" {
		want := normalize(in.Content)
		kept := txs[:0]
		for _, t := range txs {
			if strings.Contains(normalize(t.TransactionContent), want) {
				kept = append(kept, t)
			}
		}
		txs = kept
	}
	if len(txs) > limit {
		txs = txs[:limit]
	}
	return txs, nil
}

func summarize(txs []Transaction) Summary {
	s := Summary{Truncated: len(txs) >= 5000, ByDay: []DayTotal{}}
	days := map[string]*DayTotal{}
	var largest int64
	for i, t := range txs {
		in, out := vnd(t.AmountIn), vnd(t.AmountOut)
		day := t.TransactionDate[:min(10, len(t.TransactionDate))]
		d := days[day]
		if d == nil {
			d = &DayTotal{Date: day}
			days[day] = d
		}
		if in > 0 {
			s.TotalIn += in
			s.CountIn++
			d.In += in
			if in > largest {
				largest = in
				s.LargestIn = &txs[i]
			}
		}
		if out > 0 {
			s.TotalOut += out
			s.CountOut++
			d.Out += out
		}
	}
	for _, d := range days {
		s.ByDay = append(s.ByDay, *d)
	}
	sort.Slice(s.ByDay, func(i, j int) bool { return s.ByDay[i].Date < s.ByDay[j].Date })
	return s
}

func qrURL(in QRInput) string {
	q := url.Values{"acc": {in.AccountNumber}, "bank": {in.Bank}}
	if in.Amount > 0 {
		q.Set("amount", strconv.FormatInt(in.Amount, 10))
	}
	if in.Description != "" {
		d := []rune(in.Description)
		q.Set("des", string(d[:min(len(d), 94)]))
	}
	setIf(q, "template", in.Template)
	return "https://vietqr.app/img?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}

// dateRange đổi YYYY-MM-DD thành khoảng trọn ngày như ví dụ trong docs SePay.
func dateRange(from, to string) url.Values {
	q := url.Values{}
	if len(from) == 10 {
		from += " 00:00:00"
	}
	if len(to) == 10 {
		to += " 23:59:59"
	}
	setIf(q, "transaction_date_min", from)
	setIf(q, "transaction_date_max", to)
	return q
}

func setIf(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

// vnd đọc số tiền dạng "19689000.00".
func vnd(s string) int64 {
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f)
}

// normalize bỏ dấu cách/ký tự đặc biệt và viết thường, vì ngân hàng hay thêm/bớt dấu cách trong nội dung.
func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
