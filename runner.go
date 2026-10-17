package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func contextBackground() context.Context { return context.Background() }

// ExecResult is the outcome of running one request through the full pipeline.
type ExecResult struct {
	ItemID     string       `json:"itemId,omitempty"`
	Name       string       `json:"name"`
	Response   *Response    `json:"response,omitempty"`
	Sent       *SentRequest `json:"sent,omitempty"`
	Error      string       `json:"error,omitempty"`
	Tests      []TestResult `json:"tests"`
	Logs       []LogEntry   `json:"logs"`
	Visualizer *Visualizer  `json:"visualizer,omitempty"`
	Next       *NextReq     `json:"-"`
	Skipped    bool         `json:"skipped,omitempty"`
}

// ExecInput identifies what to run. Request is the (possibly unsaved) request
// as edited in the UI; CollectionID/ItemID locate inherited auth and scripts.
type ExecInput struct {
	CollectionID string            `json:"collectionId"`
	ItemID       string            `json:"itemId"`
	Name         string            `json:"name"`
	Request      *Request          `json:"request"`
	EnvID        string            `json:"envId"`
	Data         map[string]string `json:"data,omitempty"`
}

// Execute runs: pre-request scripts (collection → folders → request), variable
// resolution, auth, send, then test scripts in the same order.
func (e *Engine) Execute(ctx context.Context, col *Collection, folders []*Item, itemID, name string, src *Request, vars *Vars, info ScriptInfo) *ExecResult {
	res := &ExecResult{ItemID: itemID, Name: name, Tests: []TestResult{}, Logs: []LogEntry{}}
	req := cloneRequest(src)
	info.RequestName, info.RequestID = name, itemID

	type scr struct{ src, label string }
	var pre, post []scr
	if col != nil {
		pre = append(pre, scr{col.PreScript, "collection pre-request"})
		post = append(post, scr{col.TestScript, "collection tests"})
	}
	for _, f := range folders {
		pre = append(pre, scr{f.PreScript, f.Name + " pre-request"})
		post = append(post, scr{f.TestScript, f.Name + " tests"})
	}
	pre = append(pre, scr{req.PreScript, "pre-request"})
	post = append(post, scr{req.TestScript, "tests"})

	info.EventName = "prerequest"
	for _, s := range pre {
		if strings.TrimSpace(s.src) == "" {
			continue
		}
		sr, err := e.runScript(s.src, s.label, info, req, nil, vars, &res.Logs)
		if sr != nil {
			res.Tests = append(res.Tests, sr.Tests...)
			if sr.Request != nil {
				req.Method, req.URL, req.Headers, req.Body = sr.Request.Method, sr.Request.URL, sr.Request.Headers, sr.Request.Body
			}
			if sr.Next != nil {
				res.Next = sr.Next
			}
			if sr.Skip {
				res.Skipped = true
				return res
			}
		}
		if err != nil {
			res.Error = err.Error()
			return res
		}
	}

	req.Auth = effectiveAuth(req, folders, col)
	resolved := resolveRequest(req, vars)
	if err := applyAuth(resolved); err != nil {
		res.Error = err.Error()
		return res
	}
	resp, sent, err := e.Send(ctx, resolved)
	res.Sent = sent
	if err != nil {
		res.Error = err.Error()
		if isTimeout(err) {
			res.Error = "request timed out: " + err.Error()
		}
		return res
	}
	res.Response = resp

	info.EventName = "test"
	for _, s := range post {
		if strings.TrimSpace(s.src) == "" {
			continue
		}
		sr, err := e.runScript(s.src, s.label, info, resolved, resp, vars, &res.Logs)
		if sr != nil {
			res.Tests = append(res.Tests, sr.Tests...)
			if sr.Visualizer != nil {
				res.Visualizer = sr.Visualizer
			}
			if sr.Next != nil {
				res.Next = sr.Next
			}
		}
		if err != nil {
			res.Tests = append(res.Tests, TestResult{Name: s.label + " script error", Passed: false, Error: err.Error()})
		}
	}
	return res
}

func cloneRequest(r *Request) *Request {
	b, _ := json.Marshal(r)
	var out Request
	json.Unmarshal(b, &out)
	if out.Body.Mode == "" {
		out.Body.Mode = "none"
	}
	return &out
}

// varsFor builds scopes from the workspace for a collection/environment.
func varsFor(ws *Workspace, col *Collection, envID string, data map[string]string) *Vars {
	var colVars, envVals []KV
	if col != nil {
		colVars = col.Variables
	}
	if env := ws.environment(envID); env != nil {
		envVals = env.Values
	}
	return newVars(ws.Globals, colVars, envVals, data)
}

// VarUpdates reports the final scope values so the UI can persist them.
type VarUpdates struct {
	Globals     []KV `json:"globals,omitempty"`
	Environment []KV `json:"environment,omitempty"`
	Collection  []KV `json:"collection,omitempty"`
}

func varUpdates(ws *Workspace, col *Collection, envID string, v *Vars) VarUpdates {
	var u VarUpdates
	if v.Globals.Changed {
		u.Globals = v.Globals.apply(ws.Globals)
	}
	if env := ws.environment(envID); env != nil && v.Env.Changed {
		u.Environment = v.Env.apply(env.Values)
	}
	if col != nil && v.Collection.Changed {
		u.Collection = v.Collection.apply(col.Variables)
	}
	return u
}

type RunOptions struct {
	CollectionID string              `json:"collectionId"`
	FolderID     string              `json:"folderId,omitempty"`
	ItemIDs      []string            `json:"itemIds,omitempty"`
	EnvID        string              `json:"envId"`
	Iterations   int                 `json:"iterations"`
	DelayMs      int                 `json:"delayMs"`
	Data         []map[string]string `json:"data,omitempty"`
	StopOnError  bool                `json:"stopOnError"`
}

type RunEvent struct {
	Type      string      `json:"type"` // start | result | done
	Iteration int         `json:"iteration"`
	Result    *ExecResult `json:"result,omitempty"`
	Total     int         `json:"total,omitempty"`
	Summary   *RunSummary `json:"summary,omitempty"`
	Vars      *VarUpdates `json:"vars,omitempty"`
}

type RunSummary struct {
	Time        int64   `json:"time"`
	Requests    int     `json:"requests"`
	Failed      int     `json:"failedRequests"`
	Tests       int     `json:"tests"`
	TestsFailed int     `json:"testsFailed"`
	DurationMs  int64   `json:"durationMs"`
	AvgRespMs   float64 `json:"avgResponseMs"`
}

// Run executes a collection (or folder) for N iterations, honouring
// pm.execution.setNextRequest. emit is called for every event.
func (e *Engine) Run(ctx context.Context, ws *Workspace, opt RunOptions, emit func(RunEvent)) (*RunSummary, error) {
	col := ws.collection(opt.CollectionID)
	if col == nil {
		return nil, fmt.Errorf("collection %q not found", opt.CollectionID)
	}
	var list []flatItem
	if opt.FolderID != "" {
		chain, f := findPath(col.Items, opt.FolderID)
		if f == nil {
			return nil, fmt.Errorf("folder %q not found", opt.FolderID)
		}
		if f.Type == "folder" {
			flatten(f.Items, append(chain, f), &list)
		} else {
			list = []flatItem{{Folders: chain, Item: f}}
		}
	} else {
		flatten(col.Items, nil, &list)
	}
	if len(opt.ItemIDs) > 0 {
		keep := map[string]bool{}
		for _, id := range opt.ItemIDs {
			keep[id] = true
		}
		filtered := list[:0]
		for _, fi := range list {
			if keep[fi.Item.ID] {
				filtered = append(filtered, fi)
			}
		}
		list = filtered
	}
	iters := opt.Iterations
	if len(opt.Data) > 0 && iters <= 0 {
		iters = len(opt.Data)
	}
	if iters <= 0 {
		iters = 1
	}
	emit(RunEvent{Type: "start", Total: iters * len(list)})

	sum := &RunSummary{Time: time.Now().UnixMilli()}
	start := time.Now()
	vars := varsFor(ws, col, opt.EnvID, nil)
	var totalResp float64
	stopped := false
	for it := 0; it < iters && !stopped; it++ {
		vars.Data = &Scope{Values: map[string]string{}, Deleted: map[string]bool{}}
		if len(opt.Data) > 0 {
			for k, v := range opt.Data[it%len(opt.Data)] {
				vars.Data.Values[k] = v
			}
		}
		vars.Local = scopeFrom(nil)
		for i := 0; i < len(list); {
			if ctx.Err() != nil {
				stopped = true
				break
			}
			fi := list[i]
			info := ScriptInfo{Iteration: it, IterationCount: iters}
			r := e.Execute(ctx, col, fi.Folders, fi.Item.ID, fi.Item.Name, fi.Item.Request, vars, info)
			sum.Requests++
			if r.Error != "" {
				sum.Failed++
			}
			if r.Response != nil {
				totalResp += r.Response.TimeMs
				r.Response.Body = truncate(r.Response.Body, 256<<10)
			}
			for _, t := range r.Tests {
				if t.Skipped {
					continue
				}
				sum.Tests++
				if !t.Passed {
					sum.TestsFailed++
				}
			}
			emit(RunEvent{Type: "result", Iteration: it, Result: r})
			if opt.StopOnError && (r.Error != "" || hasFailure(r.Tests)) {
				stopped = true
				break
			}
			next := i + 1
			if r.Next != nil {
				if r.Next.Stop {
					break
				}
				next = -1
				for j, x := range list {
					if x.Item.Name == r.Next.Name || x.Item.ID == r.Next.Name {
						next = j
						break
					}
				}
				if next < 0 {
					break
				}
			}
			i = next
			if opt.DelayMs > 0 {
				select {
				case <-time.After(time.Duration(opt.DelayMs) * time.Millisecond):
				case <-ctx.Done():
				}
			}
		}
	}
	sum.DurationMs = time.Since(start).Milliseconds()
	if sum.Requests > 0 {
		sum.AvgRespMs = totalResp / float64(sum.Requests)
	}
	u := varUpdates(ws, col, opt.EnvID, vars)
	emit(RunEvent{Type: "done", Summary: sum, Vars: &u})
	return sum, nil
}

func hasFailure(ts []TestResult) bool {
	for _, t := range ts {
		if !t.Passed && !t.Skipped {
			return true
		}
	}
	return false
}

// parseDataFile accepts CSV (header row) or a JSON array of objects.
func parseDataFile(name, content string) ([]map[string]string, error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	if strings.HasSuffix(strings.ToLower(name), ".json") || strings.HasPrefix(strings.TrimSpace(content), "[") {
		var raw []map[string]any
		if err := json.Unmarshal([]byte(content), &raw); err != nil {
			return nil, fmt.Errorf("data file: %w", err)
		}
		out := make([]map[string]string, len(raw))
		for i, row := range raw {
			out[i] = map[string]string{}
			for k, v := range row {
				if s, ok := v.(string); ok {
					out[i][k] = s
				} else {
					b, _ := json.Marshal(v)
					out[i][k] = string(b)
				}
			}
		}
		return out, nil
	}
	r := csv.NewReader(strings.NewReader(content))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("data file: %w", err)
	}
	if len(rows) < 1 {
		return nil, nil
	}
	var out []map[string]string
	for _, row := range rows[1:] {
		m := map[string]string{}
		for i, h := range rows[0] {
			if i < len(row) {
				m[strings.TrimSpace(h)] = row[i]
			}
		}
		out = append(out, m)
	}
	return out, nil
}
