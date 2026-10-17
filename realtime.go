package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// The realtime bridge lets the UI drive WebSocket, Socket.IO and SSE
// connections through the Go process, so custom headers, cookies, proxies and
// TLS settings apply exactly as for HTTP requests.

type bridgeMsg struct {
	Type      string `json:"type"`
	Mode      string `json:"mode,omitempty"` // ws | socketio | sse
	URL       string `json:"url,omitempty"`
	Headers   []KV   `json:"headers,omitempty"`
	Protocols string `json:"protocols,omitempty"`
	EnvID     string `json:"envId,omitempty"`
	Data      string `json:"data,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Event     string `json:"event,omitempty"`
	ID        string `json:"id,omitempty"`
	Code      int    `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Time      int64  `json:"time,omitempty"`
	Status    int    `json:"status,omitempty"`
	RespHdrs  []KV   `json:"responseHeaders,omitempty"`
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (a *App) handleBridge(w http.ResponseWriter, r *http.Request) {
	ui, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ui.Close()
	var wmu sync.Mutex
	send := func(m bridgeMsg) {
		m.Time = time.Now().UnixMilli()
		wmu.Lock()
		defer wmu.Unlock()
		ui.WriteJSON(m)
	}
	var cfg bridgeMsg
	if err := ui.ReadJSON(&cfg); err != nil || cfg.Type != "connect" {
		return
	}
	ws := a.store.Workspace()
	vars := varsFor(ws, nil, cfg.EnvID, nil)
	cfg.URL = vars.Resolve(strings.TrimSpace(cfg.URL))
	cfg.Headers = resolveKVs(cfg.Headers, vars)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if cfg.Mode == "sse" {
		go func() {
			for {
				var m bridgeMsg
				if ui.ReadJSON(&m) != nil || m.Type == "disconnect" {
					cancel()
					return
				}
			}
		}()
		a.runSSE(ctx, cfg, send)
		return
	}

	target := cfg.URL
	if cfg.Mode == "socketio" {
		u, err := url.Parse(normalizeWS(target))
		if err != nil {
			send(bridgeMsg{Type: "error", Data: err.Error()})
			return
		}
		if u.Path == "" || u.Path == "/" {
			u.Path = "/socket.io/"
		}
		q := u.Query()
		q.Set("EIO", "4")
		q.Set("transport", "websocket")
		u.RawQuery = q.Encode()
		target = u.String()
	} else {
		target = normalizeWS(target)
	}
	h := http.Header{}
	for _, kv := range cfg.Headers {
		if kv.Enabled && kv.Key != "" {
			h.Add(kv.Key, kv.Value)
		}
	}
	tu, _ := url.Parse(target)
	host := ""
	if tu != nil {
		host = tu.Hostname()
	}
	tr, err := a.engine.transport(ws.Settings, ws.Settings.VerifySSL, host)
	if err != nil {
		send(bridgeMsg{Type: "error", Data: err.Error()})
		return
	}
	// The HTTP transport's TLS config advertises h2 via ALPN; WebSocket needs HTTP/1.1.
	wsTLS := tr.TLSClientConfig.Clone()
	wsTLS.NextProtos = nil
	d := websocket.Dialer{Proxy: tr.Proxy, TLSClientConfig: wsTLS, HandshakeTimeout: 20 * time.Second, Jar: jarAdapter{a.jar}, EnableCompression: true}
	if cfg.Protocols != "" {
		for _, p := range strings.Split(cfg.Protocols, ",") {
			d.Subprotocols = append(d.Subprotocols, strings.TrimSpace(p))
		}
	}
	conn, resp, err := d.DialContext(ctx, target, h)
	if err != nil {
		msg := err.Error()
		st := 0
		if resp != nil {
			st = resp.StatusCode
			msg = fmt.Sprintf("%s (HTTP %d)", msg, resp.StatusCode)
		}
		send(bridgeMsg{Type: "error", Data: msg, Status: st})
		return
	}
	defer conn.Close()
	open := bridgeMsg{Type: "open", URL: target, Status: resp.StatusCode}
	for k, vs := range resp.Header {
		for _, v := range vs {
			open.RespHdrs = append(open.RespHdrs, KV{Key: k, Value: v, Enabled: true})
		}
	}
	send(open)

	var cmu sync.Mutex
	write := func(t int, b []byte) error {
		cmu.Lock()
		defer cmu.Unlock()
		return conn.WriteMessage(t, b)
	}

	go func() { // UI -> target
		defer cancel()
		for {
			var m bridgeMsg
			if ui.ReadJSON(&m) != nil {
				return
			}
			switch m.Type {
			case "send":
				var err error
				if m.Binary {
					b, derr := base64.StdEncoding.DecodeString(m.Data)
					if derr != nil {
						send(bridgeMsg{Type: "error", Data: "binary payload must be base64"})
						continue
					}
					err = write(websocket.BinaryMessage, b)
				} else {
					err = write(websocket.TextMessage, []byte(m.Data))
				}
				if err != nil {
					send(bridgeMsg{Type: "error", Data: err.Error()})
					return
				}
				send(bridgeMsg{Type: "sent", Data: m.Data, Binary: m.Binary})
			case "emit": // socket.io
				args := strings.TrimSpace(m.Data)
				payload := fmt.Sprintf(`42[%s]`, jsonString(m.Event))
				if args != "" {
					if !json.Valid([]byte(args)) {
						args = jsonString(args)
					}
					payload = fmt.Sprintf(`42[%s,%s]`, jsonString(m.Event), args)
				}
				if err := write(websocket.TextMessage, []byte(payload)); err != nil {
					send(bridgeMsg{Type: "error", Data: err.Error()})
					return
				}
				send(bridgeMsg{Type: "sent", Event: m.Event, Data: args})
			case "disconnect":
				cmu.Lock()
				conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				cmu.Unlock()
				return
			}
		}
	}()
	go func() { <-ctx.Done(); conn.Close() }()

	for { // target -> UI
		t, data, err := conn.ReadMessage()
		if err != nil {
			code, reason := 1006, err.Error()
			if ce, ok := err.(*websocket.CloseError); ok {
				code, reason = ce.Code, ce.Text
			}
			send(bridgeMsg{Type: "close", Code: code, Reason: reason})
			return
		}
		if cfg.Mode == "socketio" && t == websocket.TextMessage {
			s := string(data)
			switch {
			case s == "2": // engine.io ping
				write(websocket.TextMessage, []byte("3"))
				continue
			case strings.HasPrefix(s, "0"): // engine.io open → connect default namespace
				send(bridgeMsg{Type: "info", Data: "engine.io handshake " + s[1:]})
				write(websocket.TextMessage, []byte("40"))
				continue
			case strings.HasPrefix(s, "40"):
				send(bridgeMsg{Type: "info", Data: "connected to namespace " + s[2:]})
				continue
			case strings.HasPrefix(s, "42"):
				var arr []json.RawMessage
				if json.Unmarshal([]byte(s[2:]), &arr) == nil && len(arr) > 0 {
					var ev string
					json.Unmarshal(arr[0], &ev)
					rest, _ := json.Marshal(arr[1:])
					send(bridgeMsg{Type: "message", Event: ev, Data: string(rest)})
					continue
				}
			case strings.HasPrefix(s, "41"):
				send(bridgeMsg{Type: "info", Data: "namespace disconnected"})
				continue
			}
		}
		if t == websocket.BinaryMessage || !utf8.Valid(data) {
			send(bridgeMsg{Type: "message", Data: base64.StdEncoding.EncodeToString(data), Binary: true})
		} else {
			send(bridgeMsg{Type: "message", Data: string(data)})
		}
	}
}

func normalizeWS(u string) string {
	switch {
	case strings.HasPrefix(u, "http://"):
		return "ws://" + u[7:]
	case strings.HasPrefix(u, "https://"):
		return "wss://" + u[8:]
	case !strings.Contains(u, "://"):
		return "ws://" + u
	}
	return u
}

func (a *App) runSSE(ctx context.Context, cfg bridgeMsg, send func(bridgeMsg)) {
	req, err := http.NewRequestWithContext(ctx, "GET", normalizeURL(cfg.URL), nil)
	if err != nil {
		send(bridgeMsg{Type: "error", Data: err.Error()})
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for _, kv := range cfg.Headers {
		if kv.Enabled && kv.Key != "" {
			req.Header.Set(kv.Key, kv.Value)
		}
	}
	ws := a.store.Workspace()
	tr, err := a.engine.transport(ws.Settings, ws.Settings.VerifySSL, req.URL.Hostname())
	if err != nil {
		send(bridgeMsg{Type: "error", Data: err.Error()})
		return
	}
	resp, err := (&http.Client{Transport: tr, Jar: jarAdapter{a.jar}}).Do(req)
	if err != nil {
		send(bridgeMsg{Type: "error", Data: err.Error()})
		return
	}
	defer resp.Body.Close()
	open := bridgeMsg{Type: "open", Status: resp.StatusCode, URL: req.URL.String()}
	for k, vs := range resp.Header {
		for _, v := range vs {
			open.RespHdrs = append(open.RespHdrs, KV{Key: k, Value: v, Enabled: true})
		}
	}
	send(open)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var ev, id string
	var data []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if len(data) > 0 || ev != "" {
				if ev == "" {
					ev = "message"
				}
				send(bridgeMsg{Type: "message", Event: ev, ID: id, Data: strings.Join(data, "\n")})
			}
			ev, data = "", nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		v = strings.TrimPrefix(v, " ")
		switch k {
		case "event":
			ev = v
		case "data":
			data = append(data, v)
		case "id":
			id = v
		}
	}
	reason := "stream ended"
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		reason = err.Error()
	}
	send(bridgeMsg{Type: "close", Reason: reason})
}
