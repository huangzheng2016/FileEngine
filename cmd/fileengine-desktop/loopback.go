package main

import (
	"net"
)

// The desktop UI is served by the in-process Gin server on a random
// 127.0.0.1 port instead of through the Wails asset handler, because the
// Wails v3 beta.27 asset server cannot stream on Windows: its WebView2
// response writer buffers the whole body and hands it over only when the
// handler returns (internal/assetserver/webview/responsewriter_windows.go
// deliberately does not implement http.Flusher), so the never-ending SSE
// event stream would never reach the page.
//
// Any local process can connect to a loopback port, so requests are guarded
// by internal/api exactly as in server mode: a per-launch token (injected via
// app.Options.Token) exchanged for the HttpOnly SameSite=Strict session
// cookie by the window's start URL (api.BootstrapPath), the Host header
// pinned to the listener address, and cross-site requests refused.

func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
