package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
)

// effectiveAuth walks request -> folders (innermost first) -> collection,
// honouring "inherit".
func effectiveAuth(req *Request, folders []*Item, col *Collection) *Auth {
	if req.Auth != nil && req.Auth.Type != "" && req.Auth.Type != "inherit" {
		return req.Auth
	}
	for i := len(folders) - 1; i >= 0; i-- {
		if a := folders[i].Auth; a != nil && a.Type != "" && a.Type != "inherit" {
			return a
		}
	}
	if col != nil && col.Auth != nil && col.Auth.Type != "inherit" {
		return col.Auth
	}
	return nil
}

func setHeader(r *Request, k, v string) {
	for i, kv := range r.Headers {
		if kv.Enabled && strings.EqualFold(kv.Key, k) {
			r.Headers[i].Value = v
			return
		}
	}
	r.Headers = append(r.Headers, KV{Key: k, Value: v, Enabled: true})
}

func addQuery(r *Request, k, v string) {
	sep := "?"
	if strings.Contains(r.URL, "?") {
		sep = "&"
	}
	r.URL += sep + url.QueryEscape(k) + "=" + url.QueryEscape(v)
}

// applyAuth mutates a resolved request. Digest is completed in Engine.Send.
func applyAuth(r *Request) error {
	a := r.Auth
	if a == nil {
		return nil
	}
	p := a.Params
	if p == nil {
		p = map[string]string{}
	}
	switch a.Type {
	case "basic":
		setHeader(r, "Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p["username"]+":"+p["password"])))
	case "bearer":
		if p["token"] != "" {
			setHeader(r, "Authorization", "Bearer "+p["token"])
		}
	case "apikey":
		if p["key"] == "" {
			return nil
		}
		if p["in"] == "query" {
			addQuery(r, p["key"], p["value"])
		} else {
			setHeader(r, p["key"], p["value"])
		}
	case "oauth2":
		if p["accessToken"] == "" {
			return nil
		}
		if p["addTo"] == "query" {
			addQuery(r, "access_token", p["accessToken"])
		} else {
			prefix := p["headerPrefix"]
			if prefix == "" {
				prefix = "Bearer"
			}
			setHeader(r, "Authorization", strings.TrimSpace(prefix+" "+p["accessToken"]))
		}
	case "jwt":
		tok, err := signJWT(p)
		if err != nil {
			return err
		}
		if p["addTo"] == "query" {
			addQuery(r, "token", tok)
		} else {
			prefix := p["headerPrefix"]
			if prefix == "" {
				prefix = "Bearer"
			}
			setHeader(r, "Authorization", prefix+" "+tok)
		}
	case "awsv4":
		return signAWSv4(r, p, time.Now().UTC())
	}
	return nil
}

func digestHeader(p map[string]string, method, uri, challenge string, body []byte) string {
	ch := parseAuthParams(challenge[len("digest"):])
	realm, nonce, qop, opaque, algo := ch["realm"], ch["nonce"], ch["qop"], ch["opaque"], strings.ToUpper(ch["algorithm"])
	hf := func(s string) string {
		if strings.HasPrefix(algo, "SHA-256") {
			h := sha256.Sum256([]byte(s))
			return hex.EncodeToString(h[:])
		}
		h := md5.Sum([]byte(s))
		return hex.EncodeToString(h[:])
	}
	cnonce := randHex(8)
	nc := "00000001"
	ha1 := hf(p["username"] + ":" + realm + ":" + p["password"])
	if strings.HasSuffix(algo, "-SESS") {
		ha1 = hf(ha1 + ":" + nonce + ":" + cnonce)
	}
	q := ""
	for _, o := range strings.Split(qop, ",") {
		o = strings.TrimSpace(o)
		if o == "auth" || (o == "auth-int" && q == "") {
			q = o
		}
	}
	ha2 := hf(method + ":" + uri)
	if q == "auth-int" {
		ha2 = hf(method + ":" + uri + ":" + hf(string(body)))
	}
	var resp string
	if q != "" {
		resp = hf(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + q + ":" + ha2)
	} else {
		resp = hf(ha1 + ":" + nonce + ":" + ha2)
	}
	h := fmt.Sprintf(`Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q`, p["username"], realm, nonce, uri, resp)
	if algo != "" {
		h += ", algorithm=" + ch["algorithm"]
	}
	if q != "" {
		h += fmt.Sprintf(`, qop=%s, nc=%s, cnonce=%q`, q, nc, cnonce)
	}
	if opaque != "" {
		h += fmt.Sprintf(`, opaque=%q`, opaque)
	}
	return h
}

func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		k := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var v string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				v, s = s[1:], ""
			} else {
				v, s = s[1:end+1], s[end+2:]
			}
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				v, s = s, ""
			} else {
				v, s = s[:end], s[end:]
			}
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signJWT(p map[string]string) (string, error) {
	alg := p["algorithm"]
	if alg == "" {
		alg = "HS256"
	}
	var hf func() hash.Hash
	switch alg {
	case "HS256":
		hf = sha256.New
	case "HS384":
		hf = sha512.New384
	case "HS512":
		hf = sha512.New
	default:
		return "", fmt.Errorf("JWT algorithm %s is not supported (use HS256/384/512)", alg)
	}
	header := map[string]any{"alg": alg, "typ": "JWT"}
	if hdr := strings.TrimSpace(p["header"]); hdr != "" {
		if err := json.Unmarshal([]byte(hdr), &header); err != nil {
			return "", fmt.Errorf("JWT header: %w", err)
		}
		header["alg"] = alg
	}
	payload := strings.TrimSpace(p["payload"])
	if payload == "" {
		payload = "{}"
	}
	var pl any
	if err := json.Unmarshal([]byte(payload), &pl); err != nil {
		return "", fmt.Errorf("JWT payload: %w", err)
	}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(pl)
	secret := []byte(p["secret"])
	if p["secretBase64"] == "true" {
		if d, err := base64.StdEncoding.DecodeString(p["secret"]); err == nil {
			secret = d
		}
	}
	signing := b64url(hb) + "." + b64url(pb)
	m := hmac.New(hf, secret)
	m.Write([]byte(signing))
	return signing + "." + b64url(m.Sum(nil)), nil
}

func hmacSHA256(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// signAWSv4 implements AWS Signature Version 4 header signing.
func signAWSv4(r *Request, p map[string]string, now time.Time) error {
	u, err := url.Parse(normalizeURL(r.URL))
	if err != nil {
		return err
	}
	region, service := p["region"], p["service"]
	if region == "" {
		region = "us-east-1"
	}
	if service == "" {
		service = "execute-api"
	}
	body, _, _, err := buildBody(r.Body)
	if err != nil {
		return err
	}
	var payload []byte
	if body != nil {
		payload, _ = io.ReadAll(body)
	}
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	payloadHash := sha256Hex(payload)
	setHeader(r, "X-Amz-Date", amzDate)
	setHeader(r, "X-Amz-Content-Sha256", payloadHash)
	if p["sessionToken"] != "" {
		setHeader(r, "X-Amz-Security-Token", p["sessionToken"])
	}

	hdrs := map[string]string{"host": u.Host}
	for _, kv := range r.Headers {
		if kv.Enabled && kv.Key != "" {
			hdrs[strings.ToLower(kv.Key)] = strings.TrimSpace(kv.Value)
		}
	}
	names := make([]string, 0, len(hdrs))
	for k := range hdrs {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHdrs strings.Builder
	for _, k := range names {
		canonHdrs.WriteString(k + ":" + hdrs[k] + "\n")
	}
	signed := strings.Join(names, ";")

	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var cq []string
	for _, k := range keys {
		vs := q[k]
		sort.Strings(vs)
		for _, v := range vs {
			cq = append(cq, awsEscape(k)+"="+awsEscape(v))
		}
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = "GET"
	}
	canonical := strings.Join([]string{method, path, strings.Join(cq, "&"), canonHdrs.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	k := hmacSHA256([]byte("AWS4"+p["secretKey"]), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, sts))
	setHeader(r, "Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", p["accessKey"], scope, signed, sig))
	return nil
}

// fetchOAuth2Token performs the token request for client_credentials,
// password, authorization_code and refresh_token grants.
func fetchOAuth2Token(e *Engine, p map[string]string) (map[string]any, error) {
	form := url.Values{}
	grant := p["grantType"]
	if grant == "" {
		grant = "client_credentials"
	}
	form.Set("grant_type", grant)
	switch grant {
	case "password":
		form.Set("username", p["username"])
		form.Set("password", p["password"])
	case "authorization_code":
		form.Set("code", p["code"])
		form.Set("redirect_uri", p["redirectUri"])
		if p["codeVerifier"] != "" {
			form.Set("code_verifier", p["codeVerifier"])
		}
	case "refresh_token":
		form.Set("refresh_token", p["refreshToken"])
	}
	if p["scope"] != "" {
		form.Set("scope", p["scope"])
	}
	if p["audience"] != "" {
		form.Set("audience", p["audience"])
	}
	req := &Request{Method: "POST", URL: p["tokenUrl"], Body: Body{Mode: "raw", Raw: "", Lang: "text"}}
	req.Headers = []KV{{Key: "Content-Type", Value: "application/x-www-form-urlencoded", Enabled: true}, {Key: "Accept", Value: "application/json", Enabled: true}}
	if p["clientAuth"] == "body" {
		form.Set("client_id", p["clientId"])
		form.Set("client_secret", p["clientSecret"])
	} else {
		req.Headers = append(req.Headers, KV{Key: "Authorization", Value: "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(p["clientId"])+":"+url.QueryEscape(p["clientSecret"]))), Enabled: true})
	}
	req.Body.Raw = form.Encode()
	resp, _, err := e.Send(contextBackground(), req)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(resp.raw, &out); err != nil {
		v, perr := url.ParseQuery(resp.Body)
		if perr != nil || v.Get("access_token") == "" {
			return nil, fmt.Errorf("token endpoint returned %d: %s", resp.Code, truncate(resp.Body, 500))
		}
		out = map[string]any{}
		for k := range v {
			out[k] = v.Get(k)
		}
	}
	if resp.Code >= 400 {
		return out, fmt.Errorf("token endpoint returned %d", resp.Code)
	}
	return out, nil
}
