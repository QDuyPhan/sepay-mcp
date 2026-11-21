package main

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// httpHandler phục vụ MCP qua Streamable HTTP ở chế độ stateless: mỗi request tự mang token SePay
// của người gọi trong header Authorization, server không lưu token hay session nào.
// Vì vậy session của người này không thể dùng token của người khác.
func httpHandler(allowWrite bool, mkClient func(token string) *Client) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return newServer(mkClient(bearer(r)), allowWrite)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer(r) == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sepay"`)
			http.Error(w, "missing Authorization: Bearer <SePay API token>", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	return mux
}

// ponytail: không có rate limit theo người gọi; ai có URL đều gọi được (nhưng cần token SePay của chính họ).
// Thêm giới hạn theo IP nếu server public bị lạm dụng.
func serveHTTP(addr string, allowWrite bool) error {
	log.Printf("sepay-mcp %s: MCP endpoint http://%s/mcp (write tools: %v)", version, addr, allowWrite)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpHandler(allowWrite, NewClient),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      2 * time.Minute,
	}
	return srv.ListenAndServe()
}

func bearer(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(tok)
}
