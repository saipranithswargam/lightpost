package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"strings"
)

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func curlFor(r *Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -X %s '%s'", r.Method, r.URL)
	for _, h := range r.Headers {
		if h.Enabled {
			fmt.Fprintf(&b, " \\\n  -H '%s: %s'", h.Key, strings.ReplaceAll(h.Value, "'", `'\''`))
		}
	}
	switch r.Body.Mode {
	case "raw":
		fmt.Fprintf(&b, " \\\n  --data-raw '%s'", strings.ReplaceAll(r.Body.Raw, "'", `'\''`))
	case "urlencoded":
		for _, kv := range r.Body.Form {
			if kv.Enabled {
				fmt.Fprintf(&b, " \\\n  --data-urlencode '%s=%s'", kv.Key, kv.Value)
			}
		}
	case "formdata":
		for _, kv := range r.Body.Form {
			if kv.Enabled {
				if kv.Type == "file" {
					fmt.Fprintf(&b, " \\\n  -F '%s=@%s'", kv.Key, kv.Value)
				} else {
					fmt.Fprintf(&b, " \\\n  -F '%s=%s'", kv.Key, kv.Value)
				}
			}
		}
	case "graphql":
		if r.Body.GraphQL != nil {
			v := strings.TrimSpace(r.Body.GraphQL.Variables)
			if v == "" {
				v = "{}"
			}
			fmt.Fprintf(&b, " \\\n  -H 'Content-Type: application/json' \\\n  --data-raw '{\"query\":%s,\"variables\":%s}'", strings.ReplaceAll(jsonString(r.Body.GraphQL.Query), "'", `'\''`), v)
		}
	}
	return b.String()
}

// renderDocs produces a self-contained HTML documentation page for a
// collection (Postman publishes these to its cloud; we write a file).
func renderDocs(c *Collection) string {
	var nav, body strings.Builder
	e := html.EscapeString
	var walk func(items []*Item, depth int)
	walk = func(items []*Item, depth int) {
		for _, it := range items {
			if it.Type == "folder" {
				fmt.Fprintf(&nav, `<div class="nf" style="padding-left:%dpx">%s</div>`, depth*12, e(it.Name))
				fmt.Fprintf(&body, `<section class="folder"><h2 id="%s">%s</h2>`, it.ID, e(it.Name))
				if it.Description != "" {
					fmt.Fprintf(&body, `<div class="desc">%s</div>`, e(it.Description))
				}
				body.WriteString("</section>")
				walk(it.Items, depth+1)
				continue
			}
			r := it.Request
			if r == nil {
				continue
			}
			fmt.Fprintf(&nav, `<a style="padding-left:%dpx" href="#%s"><b class="m %s">%s</b> %s</a>`, depth*12, it.ID, strings.ToLower(r.Method), e(r.Method), e(it.Name))
			fmt.Fprintf(&body, `<section class="req" id="%s"><h3><span class="m %s">%s</span> %s</h3><code class="url">%s</code>`, it.ID, strings.ToLower(r.Method), e(r.Method), e(it.Name), e(r.URL))
			if d := r.Description; d != "" {
				fmt.Fprintf(&body, `<div class="desc">%s</div>`, e(d))
			}
			if r.Auth != nil && r.Auth.Type != "" && r.Auth.Type != "inherit" && r.Auth.Type != "none" {
				fmt.Fprintf(&body, `<p class="lbl">Authorization</p><p>%s</p>`, e(r.Auth.Type))
			}
			if len(r.Headers) > 0 {
				body.WriteString(`<p class="lbl">Headers</p><table>`)
				for _, h := range r.Headers {
					if h.Enabled {
						fmt.Fprintf(&body, `<tr><td>%s</td><td>%s</td><td>%s</td></tr>`, e(h.Key), e(h.Value), e(h.Description))
					}
				}
				body.WriteString(`</table>`)
			}
			switch r.Body.Mode {
			case "raw":
				fmt.Fprintf(&body, `<p class="lbl">Body (%s)</p><pre>%s</pre>`, e(r.Body.Lang), e(r.Body.Raw))
			case "urlencoded", "formdata":
				fmt.Fprintf(&body, `<p class="lbl">Body (%s)</p><table>`, r.Body.Mode)
				for _, kv := range r.Body.Form {
					if kv.Enabled {
						fmt.Fprintf(&body, `<tr><td>%s</td><td>%s</td><td>%s</td></tr>`, e(kv.Key), e(kv.Value), e(kv.Description))
					}
				}
				body.WriteString(`</table>`)
			case "graphql":
				if r.Body.GraphQL != nil {
					fmt.Fprintf(&body, `<p class="lbl">GraphQL</p><pre>%s</pre>`, e(r.Body.GraphQL.Query))
				}
			}
			fmt.Fprintf(&body, `<p class="lbl">Example request</p><pre>%s</pre>`, e(curlFor(r)))
			for _, ex := range r.Examples {
				fmt.Fprintf(&body, `<p class="lbl">Example response: %s <span class="code c%d">%d</span></p><pre>%s</pre>`, e(ex.Name), ex.Code/100, ex.Code, e(prettyJSON(ex.Body)))
			}
			body.WriteString("</section>")
		}
	}
	walk(c.Items, 0)
	vars := ""
	if len(c.Variables) > 0 {
		var vb strings.Builder
		vb.WriteString(`<p class="lbl">Collection variables</p><table>`)
		for _, v := range c.Variables {
			if v.Type == "secret" {
				v.Value = "••••••"
			}
			fmt.Fprintf(&vb, `<tr><td>%s</td><td>%s</td></tr>`, e(v.Key), e(v.Value))
		}
		vb.WriteString(`</table>`)
		vars = vb.String()
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + e(c.Name) + ` – API docs</title><style>
:root{--bg:#fff;--fg:#1f2328;--mut:#656d76;--line:#e5e7eb;--code:#f6f8fa;--acc:#e8590c}
@media (prefers-color-scheme:dark){:root{--bg:#0f1115;--fg:#e6e6e6;--mut:#9aa1ab;--line:#262a33;--code:#171a21}}
*{box-sizing:border-box}body{margin:0;font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:var(--bg);color:var(--fg);display:flex}
nav{width:280px;flex:none;height:100vh;position:sticky;top:0;overflow:auto;border-right:1px solid var(--line);padding:16px 8px}
nav a{display:block;padding:4px 8px;color:var(--fg);text-decoration:none;border-radius:6px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}nav a:hover{background:var(--code)}
.nf{font-weight:600;padding:8px 8px 2px;color:var(--mut)}main{flex:1;max-width:900px;padding:24px 32px}
h1{margin:0 0 8px}section.req{border-bottom:1px solid var(--line);padding:20px 0}
.m{font:600 11px ui-monospace,monospace;padding:2px 6px;border-radius:4px;background:var(--code)}
.get{color:#2f9e44}.post{color:#e67700}.put{color:#1c7ed6}.patch{color:#ae3ec9}.delete{color:#e03131}
code.url,pre{font:12.5px ui-monospace,SFMono-Regular,Menlo,monospace;background:var(--code);border:1px solid var(--line);border-radius:6px}
code.url{display:block;padding:8px 10px;word-break:break-all}pre{padding:10px;overflow:auto;max-height:420px}
.lbl{font-weight:600;color:var(--mut);margin:14px 0 6px;font-size:12px;text-transform:uppercase;letter-spacing:.04em}
table{border-collapse:collapse;width:100%}td{border:1px solid var(--line);padding:5px 8px;font-family:ui-monospace,monospace;font-size:12.5px;vertical-align:top}
.desc{white-space:pre-wrap}.code{padding:1px 6px;border-radius:4px;color:#fff;background:#868e96}.c2{background:#2f9e44}.c4{background:#e67700}.c5{background:#e03131}
@media (max-width:760px){body{display:block}nav{width:auto;height:auto;position:static;border-right:0;border-bottom:1px solid var(--line)}main{padding:16px}}
</style></head><body><nav><div class="nf">` + e(c.Name) + `</div>` + nav.String() + `</nav><main><h1>` + e(c.Name) + `</h1><div class="desc">` + e(c.Description) + `</div>` + vars + body.String() + `<p style="color:var(--mut);margin-top:32px">Generated by Lightpost</p></main></body></html>`
}

func prettyJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
