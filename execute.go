package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxBody = 50 << 20

type Timings struct {
	DNS      float64 `json:"dns"`
	Connect  float64 `json:"connect"`
	TLS      float64 `json:"tls"`
	TTFB     float64 `json:"ttfb"`
	Download float64 `json:"download"`
	Total    float64 `json:"total"`
}

type CookieOut struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  string `json:"expires,omitempty"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
}

type SentRequest struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Headers []KV   `json:"headers"`
	Body    string `json:"body,omitempty"`
}

type Response struct {
	Code       int         `json:"code"`
	Status     string      `json:"status"`
	Proto      string      `json:"proto"`
	Headers    []KV        `json:"headers"`
	Body       string      `json:"body"`
	BodyBase64 bool        `json:"bodyBase64"`
	Size       int         `json:"size"`
	HeaderSize int         `json:"headerSize"`
	TimeMs     float64     `json:"time"`
	Timings    Timings     `json:"timings"`
	Cookies    []CookieOut `json:"cookies"`
	URL        string      `json:"url"`
	Redirects  []string    `json:"redirects,omitempty"`
	Remote     string      `json:"remote,omitempty"`
	TLS        *TLSInfo    `json:"tls,omitempty"`

	raw []byte
}

type TLSInfo struct {
	Version  string `json:"version"`
	Cipher   string `json:"cipher"`
	Subject  string `json:"subject"`
	Issuer   string `json:"issuer"`
	NotAfter string `json:"notAfter"`
}

// Engine sends HTTP requests. Transports are cached per TLS/proxy configuration
// so connections are reused without leaking idle sockets.
type Engine struct {
	store *Store
	jar   *Jar
	mu    sync.Mutex
	trans map[string]*http.Transport
}

func newEngine(store *Store, jar *Jar) *Engine {
	return &Engine{store: store, jar: jar, trans: map[string]*http.Transport{}}
}

func (e *Engine) transport(st Settings, verify bool, host string) (*http.Transport, error) {
	var cert *ClientCert
	for i := range st.ClientCerts {
		c := &st.ClientCerts[i]
		if c.Host != "" && (strings.EqualFold(c.Host, host) || strings.HasSuffix(host, "."+c.Host)) {
			cert = c
			break
		}
	}
	key := fmt.Sprintf("%v|%s|%s|%v", verify, st.ProxyURL, st.CACertPath, cert)
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.trans[key]; ok {
		return t, nil
	}
	tc := &tls.Config{InsecureSkipVerify: !verify}
	if st.CACertPath != "" {
		pem, err := os.ReadFile(st.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("CA certificate: %w", err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pem)
		tc.RootCAs = pool
	}
	if cert != nil {
		kp, err := tls.LoadX509KeyPair(cert.CertPath, cert.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("client certificate for %s: %w", cert.Host, err)
		}
		tc.Certificates = []tls.Certificate{kp}
	}
	t := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     tc,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  false,
	}
	if st.ProxyURL != "" {
		pu, err := url.Parse(st.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("proxy url: %w", err)
		}
		t.Proxy = http.ProxyURL(pu)
	}
	e.trans[key] = t
	return t, nil
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// buildBody encodes the (already variable-resolved) body and returns the
// content type it implies.
func buildBody(b Body) (io.Reader, string, string, error) {
	switch b.Mode {
	case "raw":
		ct := map[string]string{"json": "application/json", "xml": "application/xml", "html": "text/html", "javascript": "application/javascript"}[b.Lang]
		if ct == "" {
			ct = "text/plain"
		}
		return strings.NewReader(b.Raw), ct, b.Raw, nil
	case "urlencoded":
		v := url.Values{}
		for _, kv := range b.Form {
			if kv.Enabled && kv.Key != "" {
				v.Add(kv.Key, kv.Value)
			}
		}
		s := v.Encode()
		return strings.NewReader(s), "application/x-www-form-urlencoded", s, nil
	case "formdata":
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		var preview strings.Builder
		for _, kv := range b.Form {
			if !kv.Enabled || kv.Key == "" {
				continue
			}
			if kv.Type == "file" {
				data, err := os.ReadFile(kv.Value)
				if err != nil {
					return nil, "", "", fmt.Errorf("form file %q: %w", kv.Key, err)
				}
				h := textproto.MIMEHeader{}
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, kv.Key, filepath.Base(kv.Value)))
				h.Set("Content-Type", http.DetectContentType(data))
				pw, _ := w.CreatePart(h)
				pw.Write(data)
				fmt.Fprintf(&preview, "%s: <file %s>\n", kv.Key, kv.Value)
			} else {
				w.WriteField(kv.Key, kv.Value)
				fmt.Fprintf(&preview, "%s: %s\n", kv.Key, kv.Value)
			}
		}
		w.Close()
		return &buf, w.FormDataContentType(), preview.String(), nil
	case "binary":
		if b.File == "" {
			return nil, "", "", nil
		}
		data, err := os.ReadFile(b.File)
		if err != nil {
			return nil, "", "", fmt.Errorf("binary body: %w", err)
		}
		return bytes.NewReader(data), http.DetectContentType(data), fmt.Sprintf("<file %s, %d bytes>", b.File, len(data)), nil
	case "graphql":
		if b.GraphQL == nil {
			return nil, "", "", nil
		}
		vars := strings.TrimSpace(b.GraphQL.Variables)
		if vars == "" {
			vars = "{}"
		}
		s := fmt.Sprintf(`{"query":%s,"variables":%s}`, jsonString(b.GraphQL.Query), vars)
		return strings.NewReader(s), "application/json", s, nil
	}
	return nil, "", "", nil
}

func hasHeader(h []KV, name string) bool {
	for _, kv := range h {
		if kv.Enabled && strings.EqualFold(kv.Key, name) {
			return true
		}
	}
	return false
}

// resolveRequest returns a deep copy with variables substituted everywhere.
func resolveRequest(r *Request, v *Vars) *Request {
	out := *r
	out.URL = substitutePathVars(v.Resolve(strings.TrimSpace(r.URL)), resolveKVs(r.PathVars, v))
	out.Headers = resolveKVs(r.Headers, v)
	out.Body = r.Body
	out.Body.Raw = v.Resolve(r.Body.Raw)
	out.Body.File = v.Resolve(r.Body.File)
	out.Body.Form = resolveKVs(r.Body.Form, v)
	if r.Body.GraphQL != nil {
		out.Body.GraphQL = &GraphQLBody{Query: v.Resolve(r.Body.GraphQL.Query), Variables: v.Resolve(r.Body.GraphQL.Variables)}
	}
	if r.Auth != nil {
		a := &Auth{Type: r.Auth.Type, Params: map[string]string{}}
		for k, val := range r.Auth.Params {
			a.Params[k] = v.Resolve(val)
		}
		out.Auth = a
	}
	return &out
}

// substitutePathVars replaces :name path segments (Postman path variables).
func substitutePathVars(raw string, pv []KV) string {
	if len(pv) == 0 {
		return raw
	}
	pathStart := 0
	if i := strings.Index(raw, "://"); i >= 0 {
		if j := strings.IndexByte(raw[i+3:], '/'); j >= 0 {
			pathStart = i + 3 + j
		} else {
			return raw
		}
	}
	end := len(raw)
	if q := strings.IndexAny(raw[pathStart:], "?#"); q >= 0 {
		end = pathStart + q
	}
	segs := strings.Split(raw[pathStart:end], "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			for _, kv := range pv {
				if kv.Key == s[1:] && kv.Value != "" {
					segs[i] = url.PathEscape(kv.Value)
				}
			}
		}
	}
	return raw[:pathStart] + strings.Join(segs, "/") + raw[end:]
}

func resolveKVs(kvs []KV, v *Vars) []KV {
	out := make([]KV, len(kvs))
	for i, kv := range kvs {
		kv.Key = v.Resolve(kv.Key)
		kv.Value = v.Resolve(kv.Value)
		out[i] = kv
	}
	return out
}

func normalizeURL(raw string) string {
	if raw != "" && !strings.Contains(raw, "://") {
		return "http://" + raw
	}
	return raw
}

// Send performs a fully resolved request. Auth must already be applied except
// for digest, which needs the server's challenge.
func (e *Engine) Send(ctx context.Context, r *Request) (*Response, *SentRequest, error) {
	st := e.store.Workspace().Settings
	follow := st.FollowRedirects
	if r.Settings.FollowRedirects != nil {
		follow = *r.Settings.FollowRedirects
	}
	verify := st.VerifySSL
	if r.Settings.VerifySSL != nil {
		verify = *r.Settings.VerifySSL
	}
	timeout := st.TimeoutMs
	if r.Settings.TimeoutMs > 0 {
		timeout = r.Settings.TimeoutMs
	}
	maxRedirects := r.Settings.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = 10
	}

	u, err := url.Parse(normalizeURL(r.URL))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" {
		return nil, nil, errors.New("invalid URL: missing host")
	}
	tr, err := e.transport(st, verify, u.Hostname())
	if err != nil {
		return nil, nil, err
	}

	body, ct, preview, err := buildBody(r.Body)
	if err != nil {
		return nil, nil, err
	}
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		method = "GET"
	}

	newReq := func() (*http.Request, error) {
		var rd io.Reader
		if bodyBytes != nil {
			rd = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Lightpost/1.0")
		req.Header.Set("Accept", "*/*")
		for _, kv := range r.Headers {
			if kv.Enabled && kv.Key != "" {
				if strings.EqualFold(kv.Key, "host") {
					req.Host = kv.Value
					continue
				}
				req.Header.Add(kv.Key, kv.Value)
			}
		}
		if ct != "" && !hasHeader(r.Headers, "Content-Type") {
			req.Header.Set("Content-Type", ct)
		}
		return req, nil
	}

	req, err := newReq()
	if err != nil {
		return nil, nil, err
	}
	var redirects []string
	client := &http.Client{
		Transport: tr,
		Jar:       jarAdapter{e.jar},
		Timeout:   time.Duration(timeout) * time.Millisecond,
		CheckRedirect: func(nr *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			redirects = append(redirects, nr.URL.String())
			return nil
		},
	}
	if timeout <= 0 {
		client.Timeout = 0
	}

	resp, tm, start, err := doTraced(client, req)
	if err == nil && resp.StatusCode == 401 && r.Auth != nil && r.Auth.Type == "digest" {
		if chal := resp.Header.Get("WWW-Authenticate"); strings.HasPrefix(strings.ToLower(chal), "digest") {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			req, _ = newReq()
			req.Header.Set("Authorization", digestHeader(r.Auth.Params, method, u.RequestURI(), chal, bodyBytes))
			resp, tm, start, err = doTraced(client, req)
		}
	}
	sent := &SentRequest{Method: method, URL: u.String(), Body: truncate(preview, 64<<10)}
	for k, vs := range req.Header {
		for _, v := range vs {
			sent.Headers = append(sent.Headers, KV{Key: k, Value: v, Enabled: true})
		}
	}
	if err != nil {
		return nil, sent, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, sent, fmt.Errorf("reading response: %w", err)
	}
	end := time.Now()
	tm.Total = ms(end.Sub(start))
	if tm.TTFB > 0 {
		tm.Download = tm.Total - tm.TTFB
	}

	out := &Response{Code: resp.StatusCode, Status: http.StatusText(resp.StatusCode), Proto: resp.Proto, Size: len(data), TimeMs: tm.Total, Timings: tm, URL: resp.Request.URL.String(), Redirects: redirects, raw: data}
	hs := 0
	for k, vs := range resp.Header {
		for _, v := range vs {
			out.Headers = append(out.Headers, KV{Key: k, Value: v, Enabled: true})
			hs += len(k) + len(v) + 4
		}
	}
	out.HeaderSize = hs
	if utf8.Valid(data) {
		out.Body = string(data)
	} else {
		out.Body = base64.StdEncoding.EncodeToString(data)
		out.BodyBase64 = true
	}
	for _, c := range resp.Cookies() {
		co := CookieOut{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, HTTPOnly: c.HttpOnly, Secure: c.Secure}
		if co.Domain == "" {
			co.Domain = resp.Request.URL.Hostname()
		}
		if !c.Expires.IsZero() {
			co.Expires = c.Expires.UTC().Format(time.RFC1123)
		}
		out.Cookies = append(out.Cookies, co)
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		pc := resp.TLS.PeerCertificates[0]
		out.TLS = &TLSInfo{Version: tls.VersionName(resp.TLS.Version), Cipher: tls.CipherSuiteName(resp.TLS.CipherSuite), Subject: pc.Subject.CommonName, Issuer: pc.Issuer.CommonName, NotAfter: pc.NotAfter.UTC().Format(time.RFC1123)}
	}
	return out, sent, nil
}

func doTraced(c *http.Client, req *http.Request) (*http.Response, Timings, time.Time, error) {
	var tm Timings
	var dnsStart, connStart, tlsStart time.Time
	start := time.Now()
	trace := &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:           func(httptrace.DNSDoneInfo) { tm.DNS = ms(time.Since(dnsStart)) },
		ConnectStart:      func(string, string) { connStart = time.Now() },
		ConnectDone:       func(string, string, error) { tm.Connect = ms(time.Since(connStart)) },
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { tm.TLS = ms(time.Since(tlsStart)) },
		GotFirstResponseByte: func() {
			tm.TTFB = ms(time.Since(start))
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := c.Do(req)
	return resp, tm, start, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
