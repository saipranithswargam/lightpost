package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MockManager runs local mock servers that answer from saved examples,
// replacing Postman's cloud-hosted mocks.
type MockManager struct {
	store   *Store
	mu      sync.Mutex
	running map[string]*runningMock
	logs    map[string][]MockLog
}

type runningMock struct {
	cfg Mock
	srv *http.Server
}

type MockLog struct {
	Time    int64  `json:"time"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Status  int    `json:"status"`
	Matched string `json:"matched,omitempty"`
}

type MockStatus struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newMockManager(s *Store) *MockManager {
	return &MockManager{store: s, running: map[string]*runningMock{}, logs: map[string][]MockLog{}}
}

// Reconcile starts/stops servers to match the workspace configuration.
func (m *MockManager) Reconcile(ws *Workspace) map[string]string {
	errs := map[string]string{}
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]*Mock{}
	for _, mk := range ws.Mocks {
		if mk.Enabled {
			want[mk.ID] = mk
		}
	}
	for id, r := range m.running {
		if w, ok := want[id]; !ok || w.Port != r.cfg.Port {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			r.srv.Shutdown(ctx)
			cancel()
			delete(m.running, id)
		} else {
			r.cfg = *w
		}
	}
	for id, mk := range want {
		if _, ok := m.running[id]; ok {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", mk.Port))
		if err != nil {
			errs[id] = err.Error()
			continue
		}
		rm := &runningMock{cfg: *mk}
		rm.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.serve(rm, w, r) }), ReadHeaderTimeout: 10 * time.Second}
		m.running[id] = rm
		go rm.srv.Serve(ln)
	}
	return errs
}

func (m *MockManager) Status(ws *Workspace) []MockStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MockStatus
	for _, mk := range ws.Mocks {
		st := MockStatus{ID: mk.ID}
		if _, ok := m.running[mk.ID]; ok {
			st.Running, st.URL = true, fmt.Sprintf("http://127.0.0.1:%d", mk.Port)
		}
		out = append(out, st)
	}
	return out
}

func (m *MockManager) Logs(id string) []MockLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MockLog{}, m.logs[id]...)
}

func (m *MockManager) log(id string, l MockLog) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls := append([]MockLog{l}, m.logs[id]...)
	if len(ls) > 200 {
		ls = ls[:200]
	}
	m.logs[id] = ls
}

// pathMatches compares an example path to the incoming one, treating
// :param segments and {{var}} segments as wildcards.
func pathMatches(pattern, path string) (bool, int) {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	qs := strings.Split(strings.Trim(path, "/"), "/")
	// A base-URL variable may hide a path prefix (e.g. {{baseUrl}} = host/v1),
	// so allow the incoming path to carry extra leading segments.
	if len(qs) > len(ps) {
		qs = qs[len(qs)-len(ps):]
	}
	if len(ps) != len(qs) {
		return false, 0
	}
	score := 0
	for i := range ps {
		switch {
		case ps[i] == qs[i]:
			score += 2
		case strings.HasPrefix(ps[i], ":") || (strings.HasPrefix(ps[i], "{{") && strings.HasSuffix(ps[i], "}}")) || (strings.HasPrefix(ps[i], "{") && strings.HasSuffix(ps[i], "}")):
			score++
		default:
			return false, 0
		}
	}
	return true, score
}

func examplePath(raw string) string {
	// Strip {{baseUrl}}-style prefixes and scheme/host.
	s := varRe.ReplaceAllStringFunc(raw, func(v string) string {
		if strings.HasPrefix(raw, v) {
			return ""
		}
		return v
	})
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '/'); j >= 0 {
			s = s[j:]
		} else {
			s = "/"
		}
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

func (m *MockManager) serve(rm *runningMock, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	col := m.store.Workspace().collection(rm.cfg.CollectionID)
	if col == nil {
		http.Error(w, `{"error":"mock collection not found"}`, 404)
		return
	}
	var list []flatItem
	flatten(col.Items, nil, &list)
	wantName := r.Header.Get("x-mock-response-name")
	wantCode, _ := strconv.Atoi(r.Header.Get("x-mock-response-code"))
	var best *Example
	bestScore := -1
	for _, fi := range list {
		req := fi.Item.Request
		for i := range req.Examples {
			ex := &req.Examples[i]
			method := ex.Method
			if method == "" {
				method = req.Method
			}
			if !strings.EqualFold(method, r.Method) {
				continue
			}
			u := ex.URL
			if u == "" {
				u = req.URL
			}
			ok, score := pathMatches(examplePath(u), r.URL.Path)
			if !ok {
				continue
			}
			if wantName != "" && ex.Name != wantName {
				continue
			}
			if wantCode != 0 && ex.Code != wantCode {
				continue
			}
			// exact query match is a tiebreaker
			if q := strings.SplitN(u, "?", 2); len(q) == 2 {
				if eq, err := url.ParseQuery(q[1]); err == nil {
					all := true
					for k := range eq {
						if r.URL.Query().Get(k) != eq.Get(k) {
							all = false
						}
					}
					if all {
						score += 3
					}
				}
			}
			if ex.Code >= 200 && ex.Code < 300 {
				score++
			}
			if score > bestScore {
				best, bestScore = ex, score
			}
		}
	}
	if rm.cfg.DelayMs > 0 {
		time.Sleep(time.Duration(rm.cfg.DelayMs) * time.Millisecond)
	}
	logEntry := MockLog{Time: time.Now().UnixMilli(), Method: r.Method, Path: r.URL.RequestURI()}
	if best == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		fmt.Fprintf(w, `{"error":"mockRequestNotFoundError","message":"No matching example for %s %s"}`, r.Method, r.URL.Path)
		logEntry.Status = 404
		m.log(rm.cfg.ID, logEntry)
		return
	}
	for _, h := range best.Headers {
		if h.Enabled && !strings.EqualFold(h.Key, "content-length") && !strings.EqualFold(h.Key, "content-encoding") && !strings.EqualFold(h.Key, "transfer-encoding") {
			w.Header().Set(h.Key, h.Value)
		}
	}
	code := best.Code
	if code == 0 {
		code = 200
	}
	w.WriteHeader(code)
	// Dynamic variables in example bodies are rendered per request.
	w.Write([]byte((&Vars{}).resolveDynamicOnly(best.Body)))
	logEntry.Status, logEntry.Matched = code, best.Name
	m.log(rm.cfg.ID, logEntry)
}

func (v *Vars) resolveDynamicOnly(s string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.TrimSpace(m[2 : len(m)-2])
		if dv, ok := dynamicVar(name); ok {
			return dv
		}
		return m
	})
}
