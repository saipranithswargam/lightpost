package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed ui
var uiFS embed.FS

type App struct {
	store    *Store
	jar      *Jar
	engine   *Engine
	mocks    *MockManager
	monitors *MonitorManager
	token    string
	port     int
	started  time.Time

	oauthMu    sync.Mutex
	oauthFlows map[string]*oauthFlow
}

type oauthFlow struct {
	Params map[string]string
	Result map[string]any
	Error  string
	Done   bool
}

func newApp(dir string, background bool) (*App, error) {
	st, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	jar := openJar(dir)
	eng := newEngine(st, jar)
	a := &App{store: st, jar: jar, engine: eng, mocks: newMockManager(st), token: randHex(16), started: time.Now(), oauthFlows: map[string]*oauthFlow{}}
	a.monitors = newMonitorManager(st, eng)
	st.onSave = func(ws *Workspace) {
		a.mocks.Reconcile(ws)
		a.monitors.Reconcile(ws)
	}
	if !background {
		st.onSave = nil
		return a, nil
	}
	a.mocks.Reconcile(st.Workspace())
	a.monitors.Reconcile(st.Workspace())
	return a, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 200<<20)).Decode(v)
}

// guard rejects requests that do not carry the session token or that arrive
// with a foreign Host header (DNS rebinding), so web pages cannot drive the
// local API.
func (a *App) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/oauth/callback" {
			next.ServeHTTP(w, r)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && t == a.token {
			http.SetCookie(w, &http.Cookie{Name: "lp", Value: a.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie("lp"); err != nil || c.Value != a.token {
			http.Error(w, "unauthorized: open Lightpost from its launcher URL", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(uiFS, "ui")
	static := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /api/workspace", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.store.Workspace())
	})
	mux.HandleFunc("PUT /api/workspace", func(w http.ResponseWriter, r *http.Request) {
		ws := defaultWorkspace()
		if err := decode(r, ws); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.store.SetWorkspace(ws); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "mocks": a.mocks.Status(ws)})
	})

	mux.HandleFunc("POST /api/send", a.handleSend)
	mux.HandleFunc("POST /api/run", a.handleRun)

	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, nonNilH(a.store.History())) })
	mux.HandleFunc("DELETE /api/history", func(w http.ResponseWriter, r *http.Request) {
		a.store.ClearHistory()
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content string `json:"content"`
			URL     string `json:"url"`
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if in.URL != "" {
			resp, _, err := a.engine.Send(r.Context(), &Request{Method: "GET", URL: in.URL, Body: Body{Mode: "none"}})
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			in.Content = string(resp.raw)
		}
		res, err := importAny(in.Content)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("POST /api/parse-data", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Content string }
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		rows, err := parseDataFile(in.Name, in.Content)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, rows)
	})

	mux.HandleFunc("GET /api/export", func(w http.ResponseWriter, r *http.Request) {
		ws := a.store.Workspace()
		id, kind := r.URL.Query().Get("id"), r.URL.Query().Get("kind")
		var b []byte
		var err error
		switch kind {
		case "collection":
			c := ws.collection(id)
			if c == nil {
				writeErr(w, 404, errors.New("collection not found"))
				return
			}
			b, err = exportPostmanCollection(c)
		case "environment":
			e := ws.environment(id)
			if e == nil {
				writeErr(w, 404, errors.New("environment not found"))
				return
			}
			b, err = exportPostmanEnvironment(e, "environment")
		case "globals":
			b, err = exportPostmanEnvironment(&Environment{ID: "globals", Name: "Globals", Values: ws.Globals}, "globals")
		default:
			b, err = json.MarshalIndent(ws, "", " ")
		}
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		c := a.store.Workspace().collection(r.URL.Query().Get("id"))
		if c == nil {
			http.Error(w, "collection not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderDocs(c)))
	})

	mux.HandleFunc("POST /api/savefile", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string `json:"name"`
			Content string `json:"content"`
			Base64  bool   `json:"base64"`
			Kind    string `json:"kind"` // optional: export source
			ID      string `json:"id"`
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		data := []byte(in.Content)
		if in.Base64 {
			d, err := base64.StdEncoding.DecodeString(in.Content)
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			data = d
		}
		path, err := chooseSavePath(in.Name)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, map[string]string{"path": path})
	})
	mux.HandleFunc("POST /api/pickfile", func(w http.ResponseWriter, r *http.Request) {
		path, err := chooseOpenPath()
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]string{"path": path})
	})
	mux.HandleFunc("POST /api/readfile", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		b, err := os.ReadFile(in.Path)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]string{"content": string(b)})
	})
	mux.HandleFunc("POST /api/open-external", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ URL string }
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		openBrowser(in.URL)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/cookies", func(w http.ResponseWriter, r *http.Request) {
		cs := a.jar.List()
		if cs == nil {
			cs = []*StoredCookie{}
		}
		writeJSON(w, cs)
	})
	mux.HandleFunc("PUT /api/cookies", func(w http.ResponseWriter, r *http.Request) {
		var cs []*StoredCookie
		if err := decode(r, &cs); err != nil {
			writeErr(w, 400, err)
			return
		}
		a.jar.Replace(cs)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Params map[string]string `json:"params"`
			EnvID  string            `json:"envId"`
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		p := a.resolveParams(in.Params, in.EnvID)
		if p["grantType"] == "authorization_code" || p["grantType"] == "implicit" {
			a.startOAuthBrowserFlow(w, p)
			return
		}
		tok, err := fetchOAuth2Token(a.engine, p)
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error(), "response": tok})
			return
		}
		writeJSON(w, map[string]any{"token": tok})
	})
	mux.HandleFunc("GET /api/oauth2/result", func(w http.ResponseWriter, r *http.Request) {
		a.oauthMu.Lock()
		f := a.oauthFlows[r.URL.Query().Get("state")]
		a.oauthMu.Unlock()
		if f == nil {
			writeErr(w, 404, errors.New("unknown flow"))
			return
		}
		writeJSON(w, map[string]any{"done": f.Done, "token": f.Result, "error": f.Error})
	})
	mux.HandleFunc("GET /oauth/callback", a.handleOAuthCallback)

	mux.HandleFunc("GET /api/mocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.mocks.Status(a.store.Workspace()))
	})
	mux.HandleFunc("GET /api/mocks/logs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nonNilML(a.mocks.Logs(r.URL.Query().Get("id"))))
	})
	mux.HandleFunc("GET /api/monitors/runs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nonNilMR(a.monitors.Runs(r.URL.Query().Get("id"))))
	})
	mux.HandleFunc("POST /api/monitors/run", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		for _, m := range a.store.Workspace().Monitors {
			if m.ID == in.ID {
				writeJSON(w, a.monitors.RunNow(r.Context(), *m))
				return
			}
		}
		writeErr(w, 404, errors.New("monitor not found"))
	})

	mux.HandleFunc("GET /api/bridge", a.handleBridge)
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		var ru syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
		cpu := time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
		writeJSON(w, map[string]any{"maxRssMB": float64(ru.Maxrss) / (1 << 20), "cpuSeconds": cpu.Seconds(), "uptimeSeconds": time.Since(a.started).Seconds(), "dataDir": a.store.dir})
	})
	return a.guard(mux)
}

func nonNilH(h []HistoryEntry) []HistoryEntry {
	if h == nil {
		return []HistoryEntry{}
	}
	return h
}
func nonNilML(h []MockLog) []MockLog {
	if h == nil {
		return []MockLog{}
	}
	return h
}
func nonNilMR(h []MonitorRun) []MonitorRun {
	if h == nil {
		return []MonitorRun{}
	}
	return h
}

func (a *App) resolveParams(p map[string]string, envID string) map[string]string {
	v := varsFor(a.store.Workspace(), nil, envID, nil)
	out := map[string]string{}
	for k, val := range p {
		out[k] = v.Resolve(val)
	}
	return out
}

func (a *App) handleSend(w http.ResponseWriter, r *http.Request) {
	var in ExecInput
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if in.Request == nil {
		writeErr(w, 400, errors.New("missing request"))
		return
	}
	ws := a.store.Workspace()
	col := ws.collection(in.CollectionID)
	var folders []*Item
	if col != nil && in.ItemID != "" {
		folders, _ = findPath(col.Items, in.ItemID)
	}
	vars := varsFor(ws, col, in.EnvID, in.Data)
	start := time.Now()
	res := a.engine.Execute(r.Context(), col, folders, in.ItemID, in.Name, in.Request, vars, ScriptInfo{IterationCount: 1})
	status := 0
	if res.Response != nil {
		status = res.Response.Code
	}
	a.store.AddHistory(in.Request, status, time.Since(start))
	writeJSON(w, map[string]any{"result": res, "vars": varUpdates(ws, col, in.EnvID, vars)})
}

func (a *App) handleRun(w http.ResponseWriter, r *http.Request) {
	var opt RunOptions
	if err := decode(r, &opt); err != nil {
		writeErr(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	_, err := a.engine.Run(r.Context(), a.store.Workspace(), opt, func(ev RunEvent) {
		enc.Encode(ev)
		if fl != nil {
			fl.Flush()
		}
	})
	if err != nil {
		enc.Encode(map[string]string{"type": "error", "error": err.Error()})
	}
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func (a *App) startOAuthBrowserFlow(w http.ResponseWriter, p map[string]string) {
	state := randHex(12)
	redirect := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", a.port)
	if p["redirectUri"] == "" {
		p["redirectUri"] = redirect
	}
	u, err := url.Parse(p["authUrl"])
	if err != nil || u.Host == "" {
		writeJSON(w, map[string]any{"error": "auth URL is required for this grant type"})
		return
	}
	q := u.Query()
	q.Set("client_id", p["clientId"])
	q.Set("redirect_uri", p["redirectUri"])
	q.Set("state", state)
	if p["scope"] != "" {
		q.Set("scope", p["scope"])
	}
	if p["audience"] != "" {
		q.Set("audience", p["audience"])
	}
	if p["grantType"] == "implicit" {
		q.Set("response_type", "token")
	} else {
		q.Set("response_type", "code")
		if p["pkce"] == "true" {
			p["codeVerifier"] = randHex(32)
			q.Set("code_challenge", pkceChallenge(p["codeVerifier"]))
			q.Set("code_challenge_method", "S256")
		}
	}
	u.RawQuery = q.Encode()
	a.oauthMu.Lock()
	a.oauthFlows[state] = &oauthFlow{Params: p}
	a.oauthMu.Unlock()
	openBrowser(u.String())
	writeJSON(w, map[string]any{"pending": true, "state": state, "authUrl": u.String(), "redirectUri": p["redirectUri"]})
}

const callbackPage = `<!doctype html><meta charset="utf-8"><title>Lightpost</title><body style="font:15px -apple-system,sans-serif;display:grid;place-items:center;height:90vh"><div>%s You can close this tab and return to Lightpost.</div><script>
if (location.hash.length > 1) { fetch('/oauth/callback?' + location.hash.slice(1) + '&fragment=1').then(function(){ document.body.firstChild.textContent = 'Token received.'; }); }
</script>`

func (a *App) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	a.oauthMu.Lock()
	f := a.oauthFlows[state]
	a.oauthMu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if f == nil {
		if q.Get("state") == "" && q.Get("code") == "" && q.Get("error") == "" {
			fmt.Fprintf(w, callbackPage, "Completing sign-in…") // implicit flow: token is in the fragment
			return
		}
		fmt.Fprintf(w, callbackPage, "Unknown or expired authorization request.")
		return
	}
	finish := func(res map[string]any, errMsg string) {
		a.oauthMu.Lock()
		f.Result, f.Error, f.Done = res, errMsg, true
		a.oauthMu.Unlock()
	}
	if e := q.Get("error"); e != "" {
		finish(nil, e+": "+q.Get("error_description"))
		fmt.Fprintf(w, callbackPage, "Authorization failed: "+htmlEsc(e)+".")
		return
	}
	if tok := q.Get("access_token"); tok != "" {
		res := map[string]any{}
		for k := range q {
			if k != "state" && k != "fragment" {
				res[k] = q.Get(k)
			}
		}
		finish(res, "")
		fmt.Fprintf(w, callbackPage, "Token received.")
		return
	}
	p := f.Params
	p["code"] = q.Get("code")
	tok, err := fetchOAuth2Token(a.engine, p)
	if err != nil {
		finish(tok, err.Error())
		fmt.Fprintf(w, callbackPage, "Token exchange failed: "+htmlEsc(err.Error())+".")
		return
	}
	finish(tok, "")
	fmt.Fprintf(w, callbackPage, "Token received.")
}

func htmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func openBrowser(u string) { exec.Command("open", u).Start() }

func osascript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", errors.New("cancelled")
	}
	return strings.TrimSpace(string(out)), nil
}

func chooseSavePath(name string) (string, error) {
	if name == "" {
		name = "export.json"
	}
	return osascript(fmt.Sprintf(`POSIX path of (choose file name with prompt "Save as" default name %s)`, jsonString(name)))
}

func chooseOpenPath() (string, error) {
	return osascript(`POSIX path of (choose file with prompt "Choose a file")`)
}

func (a *App) listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	a.port = ln.Addr().(*net.TCPAddr).Port
	return ln, nil
}

func (a *App) serve(ln net.Listener) *http.Server {
	srv := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	return srv
}

func (a *App) shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
