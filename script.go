package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"
)

//go:embed prelude.js
var preludeSrc string

//go:embed jslib/lodash.min.js
var lodashSrc string

//go:embed jslib/moment.min.js
var momentSrc string

var preludeProg = goja.MustCompile("prelude.js", preludeSrc, true)

// Libraries are compiled once and only evaluated in a VM when a script
// touches them, so scripts that don't use them pay nothing.
func cjsWrap(name, src string) *goja.Program {
	return goja.MustCompile(name, "(function(){var module={exports:{}};var exports=module.exports;var define;\n"+src+"\n;return module.exports;})()", true)
}

var libProgs = map[string]*goja.Program{
	"lodash": cjsWrap("lodash.min.js", lodashSrc),
	"moment": cjsWrap("moment.min.js", momentSrc),
}

type TestResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

type LogEntry struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
}

type Visualizer struct {
	Template string `json:"template"`
	Data     any    `json:"data"`
}

type NextReq struct {
	Name string `json:"name,omitempty"`
	Stop bool   `json:"stop,omitempty"`
}

type scriptResult struct {
	Tests      []TestResult `json:"tests"`
	Request    *scriptReq   `json:"request"`
	Visualizer *Visualizer  `json:"visualizer"`
	Next       *NextReq     `json:"next"`
	Skip       bool         `json:"skip"`
}

type scriptReq struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Headers []KV   `json:"headers"`
	Body    Body   `json:"body"`
}

type ScriptInfo struct {
	EventName      string `json:"eventName"`
	Iteration      int    `json:"iteration"`
	IterationCount int    `json:"iterationCount"`
	RequestName    string `json:"requestName"`
	RequestID      string `json:"requestId"`
}

const scriptTimeout = 15 * time.Second

func hashHex(alg, msg, key string) string {
	var hf func() hash.Hash
	switch alg {
	case "MD5":
		hf = md5.New
	case "SHA1":
		hf = sha1.New
	case "SHA512":
		hf = sha512.New
	default:
		hf = sha256.New
	}
	var h hash.Hash
	if key != "" {
		h = hmac.New(hf, []byte(key))
	} else {
		h = hf()
	}
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

// runScript executes one script source against the request/response context.
// The request in the context may be modified by pre-request scripts.
func (e *Engine) runScript(src, source string, info ScriptInfo, req *Request, resp *Response, vars *Vars, logs *[]LogEntry) (*scriptResult, error) {
	vm := goja.New()
	timer := time.AfterFunc(scriptTimeout, func() { vm.Interrupt("script timed out after 15s") })
	defer timer.Stop()

	scopeOf := func(name string) *Scope {
		switch name {
		case "environment":
			return vars.Env
		case "globals":
			return vars.Globals
		case "collection":
			return vars.Collection
		case "data":
			return vars.Data
		}
		return vars.Local
	}
	vm.Set("__vget", func(s, k string) goja.Value {
		if v, ok := scopeOf(s).Get(k); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	vm.Set("__vset", func(s, k, v string) { scopeOf(s).Set(k, v) })
	vm.Set("__vunset", func(s, k string) { scopeOf(s).Unset(k) })
	vm.Set("__vhas", func(s, k string) bool { _, ok := scopeOf(s).Get(k); return ok })
	vm.Set("__vclear", func(s string) { scopeOf(s).Clear() })
	vm.Set("__vobj", func(s string) string {
		m := map[string]string{}
		if s == "resolved" {
			for _, sc := range []*Scope{vars.Globals, vars.Collection, vars.Env, vars.Data, vars.Local} {
				for k, v := range sc.Values {
					m[k] = v
				}
			}
		} else {
			m = scopeOf(s).Values
		}
		b, _ := json.Marshal(m)
		return string(b)
	})
	vm.Set("__vlookup", func(k string) goja.Value {
		if v, ok := vars.Lookup(k); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	vm.Set("__resolve", func(s string) string { return vars.Resolve(s) })
	vm.Set("__log", func(level, msg string) {
		*logs = append(*logs, LogEntry{Level: level, Message: msg, Source: source})
	})
	vm.Set("__hash", hashHex)
	vm.Set("__uuid", uuid4)
	vm.Set("__hexToB64", func(h string) string { b, _ := hex.DecodeString(h); return base64.StdEncoding.EncodeToString(b) })
	vm.Set("__b64ToHex", func(s string) string {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			b, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		}
		return hex.EncodeToString(b)
	})
	vm.Set("__hexToUtf8", func(h string) string { b, _ := hex.DecodeString(h); return string(b) })
	vm.Set("__utf8ToHex", func(s string) string { return hex.EncodeToString([]byte(s)) })
	vm.Set("__cookie", func(name string) string {
		if resp != nil {
			for _, c := range resp.Cookies {
				if c.Name == name {
					return c.Value
				}
			}
		}
		if u, err := url.Parse(normalizeURL(req.URL)); err == nil {
			for _, c := range e.jar.CookiesFor(u) {
				if c.Name == name {
					return c.Value
				}
			}
		}
		return ""
	})
	vm.Set("__cookies", func() string {
		m := map[string]string{}
		if u, err := url.Parse(normalizeURL(req.URL)); err == nil {
			for _, c := range e.jar.CookiesFor(u) {
				m[c.Name] = c.Value
			}
		}
		b, _ := json.Marshal(m)
		return string(b)
	})
	libs := map[string]goja.Value{}
	vm.Set("__lib", func(name string) goja.Value {
		if v, ok := libs[name]; ok {
			return v
		}
		p, ok := libProgs[name]
		if !ok {
			return goja.Undefined()
		}
		// lodash reads the global "_" while initialising; returning undefined
		// during the load stops the lazy getter from recursing.
		libs[name] = goja.Undefined()
		v, err := vm.RunProgram(p)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		libs[name] = v
		return v
	})
	vm.Set("__send", func(js string) string {
		return e.scriptSendRequest(js, vars)
	})

	ctx := map[string]any{"info": info, "request": map[string]any{"method": req.Method, "url": req.URL, "headers": nonNil(req.Headers), "body": req.Body, "auth": req.Auth}}
	if resp != nil {
		ctx["response"] = resp
	}
	cb, _ := json.Marshal(ctx)
	vm.Set("__ctx", string(cb))
	if _, err := vm.RunProgram(preludeProg); err != nil {
		return nil, fmt.Errorf("script runtime: %w", err)
	}
	_, runErr := vm.RunScript(source, src)
	out, err := vm.RunString("__collect()")
	if err != nil {
		return nil, err
	}
	var res scriptResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		return nil, err
	}
	if runErr != nil {
		msg := runErr.Error()
		if ex, ok := runErr.(*goja.Exception); ok {
			msg = ex.Value().String()
		}
		*logs = append(*logs, LogEntry{Level: "error", Message: msg, Source: source})
		return &res, fmt.Errorf("%s: %s", source, msg)
	}
	return &res, nil
}

func nonNil(kvs []KV) []KV {
	if kvs == nil {
		return []KV{}
	}
	return kvs
}

// scriptSendRequest implements pm.sendRequest synchronously.
func (e *Engine) scriptSendRequest(js string, vars *Vars) string {
	var in struct {
		URL    any             `json:"url"`
		Method string          `json:"method"`
		Header json.RawMessage `json:"header"`
		Body   *struct {
			Mode       string `json:"mode"`
			Raw        string `json:"raw"`
			URLEncoded []KV   `json:"urlencoded"`
			FormData   []KV   `json:"formdata"`
		} `json:"body"`
	}
	fail := func(err error) string {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	if err := json.Unmarshal([]byte(js), &in); err != nil {
		return fail(err)
	}
	r := &Request{Method: in.Method, Body: Body{Mode: "none"}}
	switch u := in.URL.(type) {
	case string:
		r.URL = u
	case map[string]any:
		r.URL, _ = u["raw"].(string)
	}
	if len(in.Header) > 0 {
		var list []KV
		if json.Unmarshal(in.Header, &list) == nil {
			for _, kv := range list {
				r.Headers = append(r.Headers, KV{Key: kv.Key, Value: kv.Value, Enabled: true})
			}
		} else {
			var m map[string]string
			json.Unmarshal(in.Header, &m)
			for k, v := range m {
				r.Headers = append(r.Headers, KV{Key: k, Value: v, Enabled: true})
			}
		}
	}
	if in.Body != nil {
		switch in.Body.Mode {
		case "raw":
			r.Body = Body{Mode: "raw", Raw: in.Body.Raw, Lang: "text"}
		case "urlencoded":
			r.Body = Body{Mode: "urlencoded", Form: enableAll(in.Body.URLEncoded)}
		case "formdata":
			r.Body = Body{Mode: "formdata", Form: enableAll(in.Body.FormData)}
		}
	}
	r = resolveRequest(r, vars)
	resp, _, err := e.Send(contextBackground(), r)
	if err != nil {
		return fail(err)
	}
	b, _ := json.Marshal(map[string]any{"response": resp})
	return string(b)
}

func enableAll(kvs []KV) []KV {
	for i := range kvs {
		kvs[i].Enabled = true
	}
	return kvs
}
