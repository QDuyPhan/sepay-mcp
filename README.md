# sepay-mcp

MCP server cho [SePay](https://sepay.vn). Cho phép Claude, ChatGPT, Cursor và các AI client khác đọc giao dịch ngân hàng, đối soát đơn hàng, tổng hợp doanh thu, tạo mã VietQR và quản lý đơn thanh toán qua tài khoản ảo (VA).

> "Khách chuyển 500k cho đơn DH123 chưa?" · "Đối soát giúp 30 đơn hôm qua" · "Tuần này thu bao nhiêu?" · "Tạo QR 1,2 triệu nội dung DH456"

Mặc định **chỉ đọc**. Các tool ghi dữ liệu (tạo/hủy đơn VA) chỉ bật khi đặt `SEPAY_ENABLE_WRITE=1`.

## Tools

| Tool | Làm gì |
|---|---|
| `list_transactions` | Liệt kê giao dịch, lọc theo ngày, số tài khoản, số tiền, nội dung (không phân biệt hoa thường và dấu cách) |
| `get_transaction` | Chi tiết một giao dịch theo ID SePay hoặc mã tham chiếu ngân hàng (mã trên ảnh chụp chuyển khoản) |
| `reconcile_orders` | Đối soát hàng loạt đơn (mã đơn + số tiền): đã trả đủ / thiếu / dư / chưa trả, cộng dồn khi khách chuyển nhiều lần, liệt kê tiền vào không khớp đơn nào |
| `summarize_revenue` | Tổng tiền vào/ra, số giao dịch, theo ngày, giao dịch lớn nhất |
| `list_bank_accounts` | Các tài khoản đã liên kết kèm số dư |
| `create_payment_qr` | Link ảnh VietQR (không gọi API, không tính vào quota) |
| `list_va_orders` | Danh sách đơn thanh toán qua tài khoản ảo (VA) và trạng thái |
| `get_va_order` | Trạng thái một đơn VA: đã trả chưa, đã trả bao nhiêu |
| `create_va_order` ✏️ | Tạo đơn VA: SePay cấp số tài khoản riêng cho đơn, khách chuyển vào là khớp chính xác |
| `cancel_va_order` ✏️ | Hủy đơn VA đang chờ thanh toán |

✏️ = ghi dữ liệu, cần `SEPAY_ENABLE_WRITE=1`. AI client sẽ hỏi xác nhận trước khi gọi (tool được đánh dấu không phải read-only).

## Cài đặt

Tạo API token: my.sepay.vn → Cấu hình công ty → API Access → Thêm API.

**Cách 1: tải binary** từ [Releases](https://github.com/QDuyPhan/sepay-mcp/releases) (macOS, Linux, Windows), giải nén và đặt vào `PATH`.

**Cách 2: Go**
```sh
go install github.com/QDuyPhan/sepay-mcp@latest
```

**Cách 3: Docker**
```sh
docker run -i --rm -e SEPAY_API_TOKEN=your_token ghcr.io/qduyphan/sepay-mcp
```

### Cấu hình AI client

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

## Chạy bản remote (HTTP)

Dùng cho ChatGPT, Claude.ai bản web, n8n hoặc chatbot của bạn:

```sh
sepay-mcp -http :8080
# hoặc
docker run -p 8080:8080 ghcr.io/qduyphan/sepay-mcp -http :8080
```

- Endpoint: `POST /mcp` (Streamable HTTP, stateless). Health check: `GET /healthz`.
- **Mỗi request phải gửi** `Authorization: Bearer <SePay API token>` của chính người dùng. Server không lưu token hay session, nên nhiều người dùng chung một server mà không thấy dữ liệu của nhau.
- Không đặt `SEPAY_API_TOKEN` ở chế độ này.
- **Bắt buộc chạy sau HTTPS** (reverse proxy như Caddy/Nginx hoặc nền tảng như Fly.io, Railway), vì token đi trong header.

## Đơn hàng VA (tài khoản ảo)

Dùng SePay API v2. Mỗi ngân hàng có điều kiện riêng:

| Ngân hàng | Loại tài khoản | Tham số bắt buộc thêm |
|---|---|---|
| BIDV | Doanh nghiệp | Không (`amount` có thể bỏ trống = đơn mở) |
| Sacombank | Cá nhân / hộ kinh doanh | `va_prefix` |
| Vietcombank | Doanh nghiệp / hộ kinh doanh | `tid` (Terminal ID của Vietcombank) |
| VietinBank | Doanh nghiệp | Đăng ký dịch vụ thu hộ định danh |
| Shinhan | Doanh nghiệp | SePay cấu hình kết nối API |

Chi tiết: [tài liệu SePay](https://developer.sepay.vn/vi/sepay-api/v2/don-hang/bat-dau-nhanh).

## Ghi chú

- Giao dịch và tài khoản dùng SePay User API v1; đơn VA dùng API v2. Một token dùng cho cả hai.
- Tự giãn nhịp request (SePay giới hạn theo IP) và thử lại khi gặp HTTP 429.
- `summarize_revenue` và `reconcile_orders` lấy tối đa 5000 giao dịch mỗi lần; nếu chạm giới hạn thì trả `truncated: true`.
- Gói miễn phí của SePay (50 giao dịch/tháng) đủ để dùng thử.

## Phát triển

```sh
go test ./...
```

Phát hành: tạo tag `vX.Y.Z` → GitHub Actions build binary lên Releases và Docker image lên `ghcr.io`. Cập nhật `version` trong `server.json` rồi `mcp-publisher publish` để đăng lên [MCP Registry](https://registry.modelcontextprotocol.io).
