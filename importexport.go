package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------- Postman v2.x collection ------------------------------------------

type pmKV struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Disabled    bool   `json:"disabled,omitempty"`
	Type        string `json:"type,omitempty"`
	Src         any    `json:"src,omitempty"`
	Description any    `json:"description,omitempty"`
}

type pmAuth map[string]any

type pmEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec any    `json:"exec"`
		Type string `json:"type,omitempty"`
	} `json:"script"`
}

type pmBody struct {
	Mode       string `json:"mode,omitempty"`
	Raw        string `json:"raw,omitempty"`
	URLEncoded []pmKV `json:"urlencoded,omitempty"`
	FormData   []pmKV `json:"formdata,omitempty"`
	File       *struct {
		Src string `json:"src"`
	} `json:"file,omitempty"`
	GraphQL *struct {
		Query     string `json:"query"`
		Variables string `json:"variables"`
	} `json:"graphql,omitempty"`
	Options *struct {
		Raw *struct {
			Language string `json:"language"`
		} `json:"raw,omitempty"`
	} `json:"options,omitempty"`
}

type pmRequest struct {
	Method      string  `json:"method"`
	Header      []pmKV  `json:"header"`
	URL         any     `json:"url"`
	Body        *pmBody `json:"body,omitempty"`
	Auth        pmAuth  `json:"auth,omitempty"`
	Description any     `json:"description,omitempty"`
}

type pmResponse struct {
	Name            string     `json:"name"`
	OriginalRequest *pmRequest `json:"originalRequest,omitempty"`
	Status          string     `json:"status"`
	Code            int        `json:"code"`
	Header          []pmKV     `json:"header"`
	Body            string     `json:"body"`
}

type pmItem struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name"`
	Item        []*pmItem       `json:"item,omitempty"`
	Request     json.RawMessage `json:"request,omitempty"`
	Response    []pmResponse    `json:"response,omitempty"`
	Event       []pmEvent       `json:"event,omitempty"`
	Auth        pmAuth          `json:"auth,omitempty"`
	Description any             `json:"description,omitempty"`
}

type pmCollection struct {
	Info struct {
		ID          string `json:"_postman_id,omitempty"`
		Name        string `json:"name"`
		Description any    `json:"description,omitempty"`
		Schema      string `json:"schema"`
	} `json:"info"`
	Item     []*pmItem `json:"item"`
	Auth     pmAuth    `json:"auth,omitempty"`
	Event    []pmEvent `json:"event,omitempty"`
	Variable []pmKV    `json:"variable,omitempty"`
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any:
		if c, ok := t["content"].(string); ok {
			return c
		}
	case float64, bool:
		return fmt.Sprint(t)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func fromPmKVs(in []pmKV) []KV {
	out := make([]KV, 0, len(in))
	for _, kv := range in {
		k := KV{Key: kv.Key, Value: str(kv.Value), Enabled: !kv.Disabled, Description: str(kv.Description)}
		if kv.Type == "file" {
			k.Type = "file"
			if s, ok := kv.Src.(string); ok {
				k.Value = s
			} else if arr, ok := kv.Src.([]any); ok && len(arr) > 0 {
				k.Value = str(arr[0])
			}
		} else if kv.Type == "secret" {
			k.Type = "secret"
		}
		out = append(out, k)
	}
	return out
}

func toPmKVs(in []KV) []pmKV {
	out := make([]pmKV, 0, len(in))
	for _, kv := range in {
		p := pmKV{Key: kv.Key, Value: kv.Value, Disabled: !kv.Enabled}
		if kv.Description != "" {
			p.Description = kv.Description
		}
		if kv.Type == "file" {
			p.Type, p.Src, p.Value = "file", kv.Value, nil
		} else if kv.Type != "" {
			p.Type = kv.Type
		} else {
			p.Type = "text"
		}
		out = append(out, p)
	}
	return out
}

func scriptText(events []pmEvent, listen string) string {
	for _, e := range events {
		if e.Listen == listen {
			switch ex := e.Script.Exec.(type) {
			case string:
				return ex
			case []any:
				lines := make([]string, len(ex))
				for i, l := range ex {
					lines[i] = str(l)
				}
				return strings.Join(lines, "\n")
			}
		}
	}
	return ""
}

func toPmEvents(pre, test string) []pmEvent {
	var out []pmEvent
	add := func(listen, src string) {
		if strings.TrimSpace(src) == "" {
			return
		}
		var e pmEvent
		e.Listen = listen
		e.Script.Type = "text/javascript"
		e.Script.Exec = strings.Split(src, "\n")
		out = append(out, e)
	}
	add("prerequest", pre)
	add("test", test)
	return out
}

var pmAuthKeyMap = map[string]map[string]string{
	"oauth2": {"accessTokenUrl": "tokenUrl", "grant_type": "grantType", "addTokenTo": "addTo", "authUrl": "authUrl", "redirect_uri": "redirectUri", "client_authentication": "clientAuth", "refreshToken": "refreshToken"},
	"jwt":    {"addTokenTo": "addTo", "isSecretBase64Encoded": "secretBase64"},
}

func fromPmAuth(a pmAuth) *Auth {
	if a == nil {
		return nil
	}
	t, _ := a["type"].(string)
	if t == "" {
		return nil
	}
	if t == "noauth" {
		return &Auth{Type: "none"}
	}
	out := &Auth{Type: t, Params: map[string]string{}}
	if list, ok := a[t].([]any); ok {
		for _, e := range list {
			m, _ := e.(map[string]any)
			k := str(m["key"])
			if mk, ok := pmAuthKeyMap[t][k]; ok {
				k = mk
			}
			out.Params[k] = str(m["value"])
		}
	} else if m, ok := a[t].(map[string]any); ok { // v2.0 style
		for k, v := range m {
			out.Params[k] = str(v)
		}
	}
	if t == "oauth2" {
		switch out.Params["grantType"] {
		case "password_credentials":
			out.Params["grantType"] = "password"
		case "authorization_code_with_pkce":
			out.Params["grantType"] = "authorization_code"
			out.Params["pkce"] = "true"
		}
		if out.Params["addTo"] == "queryParams" {
			out.Params["addTo"] = "query"
		}
		if out.Params["clientAuth"] == "body" || out.Params["clientAuth"] == "header" {
		} else {
			delete(out.Params, "clientAuth")
		}
	}
	if t == "jwt" && out.Params["addTo"] == "queryParams" {
		out.Params["addTo"] = "query"
	}
	return out
}

func toPmAuth(a *Auth) pmAuth {
	if a == nil || a.Type == "" || a.Type == "inherit" {
		return nil
	}
	if a.Type == "none" {
		return pmAuth{"type": "noauth"}
	}
	rev := map[string]string{}
	for pk, ok := range pmAuthKeyMap[a.Type] {
		rev[ok] = pk
	}
	keys := make([]string, 0, len(a.Params))
	for k := range a.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var list []map[string]any
	for _, k := range keys {
		v := a.Params[k]
		if a.Type == "oauth2" && k == "addTo" && v == "query" {
			v = "queryParams"
		}
		if a.Type == "oauth2" && k == "grantType" && v == "password" {
			v = "password_credentials"
		}
		if pk, ok := rev[k]; ok {
			k = pk
		}
		list = append(list, map[string]any{"key": k, "value": v, "type": "string"})
	}
	return pmAuth{"type": a.Type, a.Type: list}
}

func urlString(u any) string {
	switch t := u.(type) {
	case string:
		return t
	case map[string]any:
		if raw, ok := t["raw"].(string); ok {
			return raw
		}
		var b strings.Builder
		if p, ok := t["protocol"].(string); ok {
			b.WriteString(p + "://")
		}
		if h, ok := t["host"].([]any); ok {
			parts := make([]string, len(h))
			for i, x := range h {
				parts[i] = str(x)
			}
			b.WriteString(strings.Join(parts, "."))
		} else if h, ok := t["host"].(string); ok {
			b.WriteString(h)
		}
		if p, ok := t["path"].([]any); ok {
			for _, x := range p {
				b.WriteString("/" + str(x))
			}
		}
		return b.String()
	}
	return ""
}

func fromPmRequest(raw json.RawMessage) *Request {
	r := &Request{Method: "GET", Body: Body{Mode: "none"}}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		r.URL = s
		return r
	}
	var pr pmRequest
	if json.Unmarshal(raw, &pr) != nil {
		return r
	}
	return convertPmRequest(&pr)
}

func convertPmRequest(pr *pmRequest) *Request {
	r := &Request{Method: strings.ToUpper(pr.Method), URL: urlString(pr.URL), Headers: fromPmKVs(pr.Header), Body: Body{Mode: "none"}, Auth: fromPmAuth(pr.Auth), Description: str(pr.Description)}
	if um, ok := pr.URL.(map[string]any); ok {
		if vs, ok := um["variable"].([]any); ok {
			for _, v := range vs {
				m, _ := v.(map[string]any)
				r.PathVars = append(r.PathVars, KV{Key: str(m["key"]), Value: str(m["value"]), Enabled: true, Description: str(m["description"])})
			}
		}
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	if r.Auth == nil {
		r.Auth = &Auth{Type: "inherit"}
	}
	if b := pr.Body; b != nil {
		switch b.Mode {
		case "raw":
			r.Body = Body{Mode: "raw", Raw: b.Raw, Lang: "text"}
			if b.Options != nil && b.Options.Raw != nil {
				r.Body.Lang = b.Options.Raw.Language
			} else if js := strings.TrimSpace(b.Raw); strings.HasPrefix(js, "{") || strings.HasPrefix(js, "[") {
				r.Body.Lang = "json"
			}
		case "urlencoded":
			r.Body = Body{Mode: "urlencoded", Form: fromPmKVs(b.URLEncoded)}
		case "formdata":
			r.Body = Body{Mode: "formdata", Form: fromPmKVs(b.FormData)}
		case "file":
			r.Body = Body{Mode: "binary"}
			if b.File != nil {
				r.Body.File = b.File.Src
			}
		case "graphql":
			r.Body = Body{Mode: "graphql", GraphQL: &GraphQLBody{}}
			if b.GraphQL != nil {
				r.Body.GraphQL.Query, r.Body.GraphQL.Variables = b.GraphQL.Query, b.GraphQL.Variables
			}
		}
	}
	return r
}

func fromPmItems(items []*pmItem) []*Item {
	out := make([]*Item, 0, len(items))
	for _, pi := range items {
		it := &Item{ID: newID(), Name: pi.Name, Description: str(pi.Description)}
		if pi.Item != nil || len(pi.Request) == 0 {
			it.Type = "folder"
			it.Items = fromPmItems(pi.Item)
			it.Auth = fromPmAuth(pi.Auth)
			it.PreScript, it.TestScript = scriptText(pi.Event, "prerequest"), scriptText(pi.Event, "test")
		} else {
			it.Type = "request"
			it.Request = fromPmRequest(pi.Request)
			if it.Request.Description == "" {
				it.Request.Description = it.Description
			}
			it.Request.PreScript, it.Request.TestScript = scriptText(pi.Event, "prerequest"), scriptText(pi.Event, "test")
			for _, ex := range pi.Response {
				e := Example{ID: newID(), Name: ex.Name, Code: ex.Code, Status: ex.Status, Headers: fromPmKVs(ex.Header), Body: ex.Body, Method: it.Request.Method, URL: it.Request.URL}
				if ex.OriginalRequest != nil {
					e.URL, e.Method = urlString(ex.OriginalRequest.URL), strings.ToUpper(ex.OriginalRequest.Method)
				}
				if e.Code == 0 {
					e.Code = 200
				}
				it.Request.Examples = append(it.Request.Examples, e)
			}
		}
		out = append(out, it)
	}
	return out
}

func importPostmanCollection(b []byte) (*Collection, error) {
	var pc pmCollection
	if err := json.Unmarshal(b, &pc); err != nil {
		return nil, err
	}
	if pc.Info.Name == "" && pc.Item == nil {
		return nil, errors.New("not a Postman collection")
	}
	return &Collection{
		ID: newID(), Name: pc.Info.Name, Description: str(pc.Info.Description),
		Items: fromPmItems(pc.Item), Auth: fromPmAuth(pc.Auth),
		PreScript: scriptText(pc.Event, "prerequest"), TestScript: scriptText(pc.Event, "test"),
		Variables: fromPmKVs(pc.Variable),
	}, nil
}

func toPmRequest(r *Request) *pmRequest {
	pr := &pmRequest{Method: r.Method, Header: toPmKVs(r.Headers), URL: map[string]any{"raw": r.URL}, Auth: toPmAuth(r.Auth)}
	if u, err := url.Parse(r.URL); err == nil && u.Host != "" {
		m := map[string]any{"raw": r.URL, "protocol": u.Scheme, "host": strings.Split(u.Hostname(), "."), "path": strings.Split(strings.TrimPrefix(u.Path, "/"), "/")}
		if u.Port() != "" {
			m["port"] = u.Port()
		}

		var q []map[string]any
		for _, part := range strings.Split(u.RawQuery, "&") {
			if part == "" {
				continue
			}
			k, v, _ := strings.Cut(part, "=")
			q = append(q, map[string]any{"key": k, "value": v})
		}
		if q != nil {
			m["query"] = q
		}
		pr.URL = m
	}
	if len(r.PathVars) > 0 {
		var pv []map[string]any
		for _, kv := range r.PathVars {
			pv = append(pv, map[string]any{"key": kv.Key, "value": kv.Value})
		}
		pr.URL.(map[string]any)["variable"] = pv
	}
	if r.Description != "" {
		pr.Description = r.Description
	}
	switch r.Body.Mode {
	case "raw":
		pr.Body = &pmBody{Mode: "raw", Raw: r.Body.Raw}
		pr.Body.Options = &struct {
			Raw *struct {
				Language string `json:"language"`
			} `json:"raw,omitempty"`
		}{Raw: &struct {
			Language string `json:"language"`
		}{Language: r.Body.Lang}}
	case "urlencoded":
		pr.Body = &pmBody{Mode: "urlencoded", URLEncoded: toPmKVs(r.Body.Form)}
	case "formdata":
		pr.Body = &pmBody{Mode: "formdata", FormData: toPmKVs(r.Body.Form)}
	case "binary":
		pr.Body = &pmBody{Mode: "file", File: &struct {
			Src string `json:"src"`
		}{Src: r.Body.File}}
	case "graphql":
		if r.Body.GraphQL != nil {
			pr.Body = &pmBody{Mode: "graphql", GraphQL: &struct {
				Query     string `json:"query"`
				Variables string `json:"variables"`
			}{r.Body.GraphQL.Query, r.Body.GraphQL.Variables}}
		}
	}
	return pr
}

func toPmItems(items []*Item) []*pmItem {
	out := make([]*pmItem, 0, len(items))
	for _, it := range items {
		pi := &pmItem{ID: it.ID, Name: it.Name}
		if it.Description != "" {
			pi.Description = it.Description
		}
		if it.Type == "folder" {
			pi.Item = toPmItems(it.Items)
			pi.Auth = toPmAuth(it.Auth)
			pi.Event = toPmEvents(it.PreScript, it.TestScript)
		} else if it.Request != nil {
			pi.Request, _ = json.Marshal(toPmRequest(it.Request))
			pi.Event = toPmEvents(it.Request.PreScript, it.Request.TestScript)
			pi.Response = []pmResponse{}
			for _, ex := range it.Request.Examples {
				or := toPmRequest(&Request{Method: ex.Method, URL: ex.URL})
				pi.Response = append(pi.Response, pmResponse{Name: ex.Name, Code: ex.Code, Status: ex.Status, Header: toPmKVs(ex.Headers), Body: ex.Body, OriginalRequest: or})
			}
		}
		out = append(out, pi)
	}
	return out
}

func exportPostmanCollection(c *Collection) ([]byte, error) {
	var pc pmCollection
	pc.Info.ID, pc.Info.Name, pc.Info.Schema = c.ID, c.Name, "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
	if c.Description != "" {
		pc.Info.Description = c.Description
	}
	pc.Item = toPmItems(c.Items)
	pc.Auth = toPmAuth(c.Auth)
	pc.Event = toPmEvents(c.PreScript, c.TestScript)
	pc.Variable = toPmKVs(c.Variables)
	return json.MarshalIndent(pc, "", "\t")
}

// ---------- Postman environment -------------------------------------------

type pmEnv struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Values []struct {
		Key     string `json:"key"`
		Value   any    `json:"value"`
		Enabled *bool  `json:"enabled,omitempty"`
		Type    string `json:"type,omitempty"`
	} `json:"values"`
	Scope string `json:"_postman_variable_scope,omitempty"`
}

func importPostmanEnvironment(b []byte) (*Environment, bool, error) {
	var pe pmEnv
	if err := json.Unmarshal(b, &pe); err != nil {
		return nil, false, err
	}
	if pe.Values == nil {
		return nil, false, errors.New("not a Postman environment")
	}
	env := &Environment{ID: newID(), Name: pe.Name}
	for _, v := range pe.Values {
		kv := KV{Key: v.Key, Value: str(v.Value), Enabled: v.Enabled == nil || *v.Enabled}
		if v.Type == "secret" {
			kv.Type = "secret"
		}
		env.Values = append(env.Values, kv)
	}
	return env, pe.Scope == "globals", nil
}

func exportPostmanEnvironment(e *Environment, scope string) ([]byte, error) {
	type val struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}
	out := map[string]any{"id": e.ID, "name": e.Name, "_postman_variable_scope": scope}
	vals := []val{}
	for _, kv := range e.Values {
		t := "default"
		if kv.Type == "secret" {
			t = "secret"
		}
		vals = append(vals, val{kv.Key, kv.Value, t, kv.Enabled})
	}
	out["values"] = vals
	return json.MarshalIndent(out, "", "\t")
}

// ---------- cURL ----------------------------------------------------------

func shellSplit(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inTok := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && (s[i+1] == '\n' || s[i+1] == '\r'):
			i++
			if i+1 < len(s) && s[i] == '\r' && s[i+1] == '\n' {
				i++
			}
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inTok = true
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			i += 2
			for ; i < len(s) && s[i] != '\''; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						cur.WriteByte('\n')
					case 't':
						cur.WriteByte('\t')
					case 'r':
						cur.WriteByte('\r')
					default:
						cur.WriteByte(s[i])
					}
					continue
				}
				cur.WriteByte(s[i])
			}
			inTok = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double quote")
			}
			inTok = true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inTok = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteByte(c)
			inTok = true
		}
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out, nil
}

func importCurl(cmd string) (*Request, error) {
	args, err := shellSplit(strings.TrimSpace(cmd))
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || args[0] != "curl" {
		return nil, errors.New("command must start with curl")
	}
	r := &Request{Method: "", Body: Body{Mode: "none"}, Auth: &Auth{Type: "inherit"}}
	var data []string
	var form []KV
	getMode := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		// --flag=value form
		if strings.HasPrefix(a, "--") && strings.Contains(a, "=") {
			k, v, _ := strings.Cut(a, "=")
			a = k
			args = append(args[:i+1], append([]string{v}, args[i+1:]...)...)
		}
		switch a {
		case "-X", "--request":
			r.Method = strings.ToUpper(next())
		case "-H", "--header":
			h := next()
			k, v, _ := strings.Cut(h, ":")
			r.Headers = append(r.Headers, KV{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v), Enabled: true})
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii":
			data = append(data, next())
		case "--data-urlencode":
			v := next()
			if k, val, ok := strings.Cut(v, "="); ok {
				data = append(data, k+"="+url.QueryEscape(val))
			} else {
				data = append(data, url.QueryEscape(v))
			}
		case "--json":
			data = append(data, next())
			r.Headers = append(r.Headers, KV{Key: "Content-Type", Value: "application/json", Enabled: true}, KV{Key: "Accept", Value: "application/json", Enabled: true})
		case "-F", "--form", "--form-string":
			k, v, _ := strings.Cut(next(), "=")
			kv := KV{Key: k, Value: v, Enabled: true}
			if strings.HasPrefix(v, "@") && a != "--form-string" {
				kv.Type, kv.Value = "file", strings.TrimPrefix(strings.SplitN(v, ";", 2)[0], "@")
			}
			form = append(form, kv)
		case "-u", "--user":
			u, p, _ := strings.Cut(next(), ":")
			r.Auth = &Auth{Type: "basic", Params: map[string]string{"username": u, "password": p}}
		case "-A", "--user-agent":
			r.Headers = append(r.Headers, KV{Key: "User-Agent", Value: next(), Enabled: true})
		case "-b", "--cookie":
			r.Headers = append(r.Headers, KV{Key: "Cookie", Value: next(), Enabled: true})
		case "-e", "--referer":
			r.Headers = append(r.Headers, KV{Key: "Referer", Value: next(), Enabled: true})
		case "-I", "--head":
			r.Method = "HEAD"
		case "-G", "--get":
			getMode = true
		case "--url":
			r.URL = next()
		case "-k", "--insecure":
			f := false
			r.Settings.VerifySSL = &f
		case "-L", "--location":
			t := true
			r.Settings.FollowRedirects = &t
		case "--compressed", "-s", "--silent", "-v", "--verbose", "-i", "--include", "-S", "--show-error", "-f", "--fail":
		case "-o", "--output", "-m", "--max-time", "--connect-timeout", "-x", "--proxy", "-w", "--write-out", "--retry":
			next()
		default:
			if !strings.HasPrefix(a, "-") && r.URL == "" {
				r.URL = a
			}
		}
	}
	if getMode && len(data) > 0 {
		sep := "?"
		if strings.Contains(r.URL, "?") {
			sep = "&"
		}
		r.URL += sep + strings.Join(data, "&")
		data = nil
	}
	if len(form) > 0 {
		r.Body = Body{Mode: "formdata", Form: form}
	} else if len(data) > 0 {
		raw := strings.Join(data, "&")
		ct := ""
		for _, h := range r.Headers {
			if strings.EqualFold(h.Key, "content-type") {
				ct = strings.ToLower(h.Value)
			}
		}
		if ct == "" || strings.Contains(ct, "x-www-form-urlencoded") {
			if vals, err := url.ParseQuery(raw); err == nil && !strings.HasPrefix(strings.TrimSpace(raw), "{") {
				r.Body = Body{Mode: "urlencoded"}
				keys := make([]string, 0)
				for _, part := range strings.Split(raw, "&") {
					k, _, _ := strings.Cut(part, "=")
					k, _ = url.QueryUnescape(k)
					keys = append(keys, k)
				}
				seen := map[string]int{}
				for _, k := range keys {
					r.Body.Form = append(r.Body.Form, KV{Key: k, Value: vals[k][seen[k]], Enabled: true})
					seen[k]++
				}
				// drop the explicit header; the body mode implies it
				r.Headers = filterHeader(r.Headers, "content-type")
			} else {
				r.Body = Body{Mode: "raw", Raw: raw, Lang: "json"}
			}
		} else {
			lang := "text"
			switch {
			case strings.Contains(ct, "json"):
				lang = "json"
			case strings.Contains(ct, "xml"):
				lang = "xml"
			}
			r.Body = Body{Mode: "raw", Raw: raw, Lang: lang}
		}
	}
	if r.Method == "" {
		if r.Body.Mode != "none" {
			r.Method = "POST"
		} else {
			r.Method = "GET"
		}
	}
	if r.URL == "" {
		return nil, errors.New("no URL found in curl command")
	}
	return r, nil
}

func filterHeader(h []KV, name string) []KV {
	out := h[:0]
	for _, kv := range h {
		if !strings.EqualFold(kv.Key, name) {
			out = append(out, kv)
		}
	}
	return out
}

// ---------- OpenAPI 3 / Swagger 2 -----------------------------------------

func importOpenAPI(b []byte) (*Collection, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse OpenAPI: %w", err)
	}
	doc = normalizeYAML(doc).(map[string]any)
	_, isOAS3 := doc["openapi"]
	_, isSwagger := doc["swagger"]
	if !isOAS3 && !isSwagger {
		return nil, errors.New("not an OpenAPI/Swagger document")
	}
	info, _ := doc["info"].(map[string]any)
	col := &Collection{ID: newID(), Name: str(info["title"]), Description: str(info["description"]), Items: []*Item{}}
	if col.Name == "" {
		col.Name = "Imported API"
	}
	base := ""
	if isOAS3 {
		if servers, ok := doc["servers"].([]any); ok && len(servers) > 0 {
			s, _ := servers[0].(map[string]any)
			base = str(s["url"])
			if vars, ok := s["variables"].(map[string]any); ok {
				for k, v := range vars {
					vm, _ := v.(map[string]any)
					base = strings.ReplaceAll(base, "{"+k+"}", str(vm["default"]))
				}
			}
		}
	} else {
		scheme := "https"
		if sc, ok := doc["schemes"].([]any); ok && len(sc) > 0 {
			scheme = str(sc[0])
		}
		base = scheme + "://" + str(doc["host"]) + str(doc["basePath"])
	}
	col.Variables = []KV{{Key: "baseUrl", Value: strings.TrimRight(base, "/"), Enabled: true}}

	folders := map[string]*Item{}
	paths, _ := doc["paths"].(map[string]any)
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)
	methods := []string{"get", "post", "put", "patch", "delete", "head", "options"}
	for _, p := range pathKeys {
		pi, _ := paths[p].(map[string]any)
		commonParams, _ := pi["parameters"].([]any)
		for _, m := range methods {
			op, ok := pi[m].(map[string]any)
			if !ok {
				continue
			}
			name := str(op["summary"])
			if name == "" {
				name = str(op["operationId"])
			}
			if name == "" {
				name = strings.ToUpper(m) + " " + p
			}
			// {id} -> :id path variables
			urlPath := p
			for {
				s := strings.IndexByte(urlPath, '{')
				e := strings.IndexByte(urlPath, '}')
				if s < 0 || e < s {
					break
				}
				urlPath = urlPath[:s] + ":" + urlPath[s+1:e] + urlPath[e+1:]
			}
			req := &Request{Method: strings.ToUpper(m), Body: Body{Mode: "none"}, Auth: &Auth{Type: "inherit"}, Description: str(op["description"])}
			var query []string
			params, _ := op["parameters"].([]any)
			for _, prm := range append(append([]any{}, commonParams...), params...) {
				pm, _ := prm.(map[string]any)
				if ref, ok := pm["$ref"].(string); ok {
					pm = resolveRef(doc, ref)
				}
				in, pname := str(pm["in"]), str(pm["name"])
				ex := exampleFor(doc, pm)
				switch in {
				case "query":
					query = append(query, pname+"="+ex)
				case "header":
					req.Headers = append(req.Headers, KV{Key: pname, Value: ex, Enabled: true})
				case "path":
					if ex != "" {
						urlPath = strings.Replace(urlPath, ":"+pname, ex, 1)
					}
				case "body":
					if schema, ok := pm["schema"].(map[string]any); ok {
						bb, _ := json.MarshalIndent(sampleFromSchema(doc, schema, 0), "", "  ")
						req.Body = Body{Mode: "raw", Raw: string(bb), Lang: "json"}
					}
				}
			}
			req.URL = "{{baseUrl}}" + urlPath
			if len(query) > 0 {
				req.URL += "?" + strings.Join(query, "&")
			}
			if rb, ok := op["requestBody"].(map[string]any); ok {
				if ref, ok := rb["$ref"].(string); ok {
					rb = resolveRef(doc, ref)
				}
				content, _ := rb["content"].(map[string]any)
				if mt, ok := content["application/json"].(map[string]any); ok {
					var sample any
					if ex, ok := mt["example"]; ok {
						sample = ex
					} else if schema, ok := mt["schema"].(map[string]any); ok {
						sample = sampleFromSchema(doc, schema, 0)
					}
					bb, _ := json.MarshalIndent(sample, "", "  ")
					req.Body = Body{Mode: "raw", Raw: string(bb), Lang: "json"}
				} else if mt, ok := content["application/x-www-form-urlencoded"].(map[string]any); ok {
					req.Body = Body{Mode: "urlencoded", Form: formFromSchema(doc, mt)}
				} else if mt, ok := content["multipart/form-data"].(map[string]any); ok {
					req.Body = Body{Mode: "formdata", Form: formFromSchema(doc, mt)}
				}
			}
			// examples from responses for mocks/docs
			if resps, ok := op["responses"].(map[string]any); ok {
				codes := make([]string, 0, len(resps))
				for c := range resps {
					codes = append(codes, c)
				}
				sort.Strings(codes)
				for _, c := range codes {
					rm, _ := resps[c].(map[string]any)
					if ref, ok := rm["$ref"].(string); ok {
						rm = resolveRef(doc, ref)
					}
					code := 200
					fmt.Sscan(c, &code)
					var body any
					if content, ok := rm["content"].(map[string]any); ok {
						if mt, ok := content["application/json"].(map[string]any); ok {
							if ex, ok := mt["example"]; ok {
								body = ex
							} else if schema, ok := mt["schema"].(map[string]any); ok {
								body = sampleFromSchema(doc, schema, 0)
							}
						}
					} else if schema, ok := rm["schema"].(map[string]any); ok {
						body = sampleFromSchema(doc, schema, 0)
					}
					ex := Example{ID: newID(), Name: str(rm["description"]), Code: code, Method: req.Method, URL: req.URL}
					if ex.Name == "" {
						ex.Name = c
					}
					if body != nil {
						bb, _ := json.MarshalIndent(body, "", "  ")
						ex.Body = string(bb)
						ex.Headers = []KV{{Key: "Content-Type", Value: "application/json", Enabled: true}}
					}
					req.Examples = append(req.Examples, ex)
				}
			}
			item := &Item{ID: newID(), Name: name, Type: "request", Request: req}
			tag := ""
			if tags, ok := op["tags"].([]any); ok && len(tags) > 0 {
				tag = str(tags[0])
			}
			if tag == "" {
				col.Items = append(col.Items, item)
				continue
			}
			f := folders[tag]
			if f == nil {
				f = &Item{ID: newID(), Name: tag, Type: "folder", Auth: &Auth{Type: "inherit"}}
				folders[tag] = f
				col.Items = append(col.Items, f)
			}
			f.Items = append(f.Items, item)
		}
	}
	return col, nil
}

func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalizeYAML(x)
		}
		return t
	case map[any]any:
		m := map[string]any{}
		for k, x := range t {
			m[fmt.Sprint(k)] = normalizeYAML(x)
		}
		return m
	case []any:
		for i, x := range t {
			t[i] = normalizeYAML(x)
		}
	}
	return v
}

func resolveRef(doc map[string]any, ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return map[string]any{}
	}
	var cur any = doc
	for _, p := range strings.Split(ref[2:], "/") {
		p = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return map[string]any{}
		}
		cur = m[p]
	}
	m, _ := cur.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func exampleFor(doc map[string]any, p map[string]any) string {
	if ex, ok := p["example"]; ok {
		return str(ex)
	}
	schema, _ := p["schema"].(map[string]any)
	if schema == nil {
		schema = p
	}
	if ex, ok := schema["example"]; ok {
		return str(ex)
	}
	if d, ok := schema["default"]; ok {
		return str(d)
	}
	if str(p["in"]) == "path" {
		return ""
	}
	return ""
}

func formFromSchema(doc map[string]any, mt map[string]any) []KV {
	schema, _ := mt["schema"].(map[string]any)
	if ref, ok := schema["$ref"].(string); ok {
		schema = resolveRef(doc, ref)
	}
	props, _ := schema["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []KV
	for _, k := range keys {
		ps, _ := props[k].(map[string]any)
		kv := KV{Key: k, Value: str(sampleFromSchema(doc, ps, 0)), Enabled: true}
		if str(ps["format"]) == "binary" {
			kv.Type, kv.Value = "file", ""
		}
		out = append(out, kv)
	}
	return out
}

func sampleFromSchema(doc map[string]any, s map[string]any, depth int) any {
	if s == nil || depth > 6 {
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		return sampleFromSchema(doc, resolveRef(doc, ref), depth+1)
	}
	if ex, ok := s["example"]; ok {
		return ex
	}
	if d, ok := s["default"]; ok {
		return d
	}
	if en, ok := s["enum"].([]any); ok && len(en) > 0 {
		return en[0]
	}
	for _, k := range []string{"allOf", "oneOf", "anyOf"} {
		if list, ok := s[k].([]any); ok && len(list) > 0 {
			if k != "allOf" {
				m, _ := list[0].(map[string]any)
				return sampleFromSchema(doc, m, depth+1)
			}
			merged := map[string]any{}
			for _, x := range list {
				m, _ := x.(map[string]any)
				if v, ok := sampleFromSchema(doc, m, depth+1).(map[string]any); ok {
					for kk, vv := range v {
						merged[kk] = vv
					}
				}
			}
			return merged
		}
	}
	switch str(s["type"]) {
	case "object", "":
		props, ok := s["properties"].(map[string]any)
		if !ok {
			if str(s["type"]) == "" {
				return nil
			}
			return map[string]any{}
		}
		out := map[string]any{}
		for k, v := range props {
			m, _ := v.(map[string]any)
			out[k] = sampleFromSchema(doc, m, depth+1)
		}
		return out
	case "array":
		m, _ := s["items"].(map[string]any)
		return []any{sampleFromSchema(doc, m, depth+1)}
	case "integer":
		return 0
	case "number":
		return 0.0
	case "boolean":
		return true
	case "string":
		switch str(s["format"]) {
		case "date-time":
			return "2024-01-01T00:00:00Z"
		case "date":
			return "2024-01-01"
		case "email":
			return "user@example.com"
		case "uuid":
			return "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		case "uri", "url":
			return "https://example.com"
		}
		return "string"
	}
	return nil
}

// importAny detects the format of an uploaded/pasted document.
type ImportResult struct {
	Collections  []*Collection  `json:"collections,omitempty"`
	Environments []*Environment `json:"environments,omitempty"`
	Globals      []KV           `json:"globals,omitempty"`
	Request      *Request       `json:"request,omitempty"`
	Kind         string         `json:"kind"`
}

func importAny(content string) (*ImportResult, error) {
	t := strings.TrimSpace(content)
	if strings.HasPrefix(t, "curl ") || t == "curl" {
		r, err := importCurl(t)
		if err != nil {
			return nil, err
		}
		return &ImportResult{Request: r, Kind: "curl"}, nil
	}
	var probe map[string]any
	if json.Unmarshal([]byte(t), &probe) == nil {
		if info, ok := probe["info"].(map[string]any); ok && strings.Contains(str(info["schema"]), "getpostman") || probe["item"] != nil {
			c, err := importPostmanCollection([]byte(t))
			if err != nil {
				return nil, err
			}
			return &ImportResult{Collections: []*Collection{c}, Kind: "postman-collection"}, nil
		}
		if _, ok := probe["values"]; ok {
			env, isGlobals, err := importPostmanEnvironment([]byte(t))
			if err != nil {
				return nil, err
			}
			if isGlobals {
				return &ImportResult{Globals: env.Values, Kind: "postman-globals"}, nil
			}
			return &ImportResult{Environments: []*Environment{env}, Kind: "postman-environment"}, nil
		}
		if _, ok := probe["collections"]; ok { // Lightpost workspace backup
			var ws Workspace
			if err := json.Unmarshal([]byte(t), &ws); err != nil {
				return nil, err
			}
			return &ImportResult{Collections: ws.Collections, Environments: ws.Environments, Globals: ws.Globals, Kind: "lightpost-workspace"}, nil
		}
	}
	c, err := importOpenAPI([]byte(t))
	if err == nil {
		return &ImportResult{Collections: []*Collection{c}, Kind: "openapi"}, nil
	}
	return nil, errors.New("unrecognised format: expected a Postman collection/environment, OpenAPI/Swagger (JSON or YAML), or a cURL command")
}
