# sepay-mcp

MCP server cho [SePay](https://sepay.vn). Cho phép Claude, Cursor và các AI client khác đọc giao dịch ngân hàng, tổng hợp doanh thu và tạo mã VietQR.

> "Khách chuyển 500k cho đơn DH123 chưa?" · "Tuần này thu bao nhiêu?" · "Tạo QR 1,2 triệu nội dung DH456"

Chỉ đọc dữ liệu, không thể chuyển tiền hay sửa gì trên tài khoản.

## Tools

| Tool | Làm gì |
|---|---|
| `list_transactions` | Liệt kê giao dịch, lọc theo ngày, số tài khoản, số tiền, nội dung (không phân biệt hoa thường và dấu cách) |
| `get_transaction` | Chi tiết một giao dịch theo ID SePay hoặc mã tham chiếu ngân hàng (mã trên ảnh chụp chuyển khoản) |
| `reconcile_orders` | Đối soát hàng loạt đơn (mã đơn + số tiền): đã trả đủ / thiếu / dư / chưa trả, cộng dồn khi khách chuyển nhiều lần, liệt kê tiền vào không khớp đơn nào |
| `summarize_revenue` | Tổng tiền vào/ra, số giao dịch, theo ngày, giao dịch lớn nhất |
| `list_bank_accounts` | Các tài khoản đã liên kết kèm số dư |
| `create_payment_qr` | Link ảnh VietQR (không gọi API, không tính vào quota) |

## Cài đặt

1. Tạo API token: my.sepay.vn → Cấu hình công ty → API Access → Thêm API
2. Build: `go install github.com/QDuyPhan/sepay-mcp@latest` (hoặc `go build` trong thư mục này)

**Claude Code**
```sh
claude mcp add sepay -e SEPAY_API_TOKEN=your_token -- sepay-mcp
```

**Claude Desktop / Cursor** (`claude_desktop_config.json` / `.cursor/mcp.json`)
```json
{
  "mcpServers": {
    "sepay": {
      "command": "sepay-mcp",
      "env": { "SEPAY_API_TOKEN": "your_token" }
    }
  }
}
```

## Ghi chú

- Dùng SePay User API v1, tự giãn nhịp 2 request/giây và thử lại khi gặp HTTP 429.
- `summarize_revenue` lấy tối đa 5000 giao dịch mỗi lần; nếu chạm giới hạn thì trả `truncated: true`.
- Gói miễn phí của SePay (50 giao dịch/tháng) đủ để dùng thử.

## Phát triển

```sh
go test ./...
```
