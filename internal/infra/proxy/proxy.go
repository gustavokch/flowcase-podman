package proxy

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// insecure transport for backend containers with self-signed certs
var insecureTransport = &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

// HTTPProxy creates a reverse proxy to a backend URL, stripping the given prefix from the path
func HTTPProxy(backendURL string, stripPrefix string, basicAuth string) http.Handler {
	target, err := url.Parse(backendURL)
	if err != nil {
		slog.Error("invalid proxy target", "url", backendURL, "error", err)
		return http.NotFoundHandler()
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = insecureTransport

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		if stripPrefix != "" {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
		}

		if basicAuth != "" {
			req.Header.Set("Authorization", "Basic "+basicAuth)
		}

		req.Host = target.Host
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Warn("proxy error", "target", backendURL, "path", r.URL.Path, "error", err)
		w.WriteHeader(http.StatusBadGateway)
	}

	return proxy
}

// WebSocketProxy handles WebSocket upgrade and bidirectional piping to a backend
func WebSocketProxy(backendURL string, basicAuth string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Upgrade client connection
		clientConn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("ws upgrade failed", "error", err)
			return
		}
		defer clientConn.Close()

		// Construct backend WebSocket URL
		backendWS := strings.Replace(backendURL, "https://", "wss://", 1)
		backendWS = strings.Replace(backendWS, "http://", "ws://", 1)

		dialer := websocket.Dialer{
			TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
			HandshakeTimeout: 10 * time.Second,
		}

		headers := http.Header{}
		if basicAuth != "" {
			headers.Set("Authorization", "Basic "+basicAuth)
		}

		backendConn, _, err := dialer.Dial(backendWS, headers)
		if err != nil {
			slog.Warn("ws backend dial failed", "url", backendWS, "error", err)
			return
		}
		defer backendConn.Close()

		// Bidirectional pipe
		done := make(chan struct{}, 2)

		go func() {
			defer func() { done <- struct{}{} }()
			pipeWS(clientConn, backendConn)
		}()

		go func() {
			defer func() { done <- struct{}{} }()
			pipeWS(backendConn, clientConn)
		}()

		<-done
	})
}

func pipeWS(src, dst *websocket.Conn) {
	for {
		msgType, msg, err := src.ReadMessage()
		if err != nil {
			return
		}
		if err := dst.WriteMessage(msgType, msg); err != nil {
			return
		}
	}
}

// RawTCPWebSocketProxy upgrades both sides and copies raw TCP bytes for maximum performance
func RawTCPWebSocketProxy(backendAddr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "websocket hijack not supported", http.StatusInternalServerError)
			return
		}

		// Dial backend
		backendConn, err := tls.DialWithDialer(
			&net.Dialer{Timeout: 10 * time.Second},
			"tcp", backendAddr,
			&tls.Config{InsecureSkipVerify: true},
		)
		if err != nil {
			slog.Warn("raw tcp dial failed", "addr", backendAddr, "error", err)
			http.Error(w, "backend unavailable", http.StatusBadGateway)
			return
		}
		defer backendConn.Close()

		// Forward the upgrade request to backend
		if err := r.Write(backendConn); err != nil {
			slog.Warn("raw tcp write failed", "error", err)
			return
		}

		// Hijack client connection
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			slog.Warn("hijack failed", "error", err)
			return
		}
		defer clientConn.Close()

		// Bidirectional copy
		done := make(chan struct{}, 2)
		go func() {
			io.Copy(backendConn, clientConn)
			done <- struct{}{}
		}()
		go func() {
			io.Copy(clientConn, backendConn)
			done <- struct{}{}
		}()
		<-done
	})
}
