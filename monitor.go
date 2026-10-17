package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// MonitorManager runs collections on a schedule locally (Postman runs these
// in its cloud). One goroutine per enabled monitor; it sleeps between runs.
type MonitorManager struct {
	store  *Store
	engine *Engine
	mu     sync.Mutex
	active map[string]*activeMonitor
	runs   map[string][]MonitorRun
	path   string
}

type activeMonitor struct {
	cfg    Monitor
	cancel context.CancelFunc
}

type MonitorRun struct {
	Time     int64       `json:"time"`
	Summary  *RunSummary `json:"summary"`
	Error    string      `json:"error,omitempty"`
	Failures []string    `json:"failures,omitempty"`
}

func newMonitorManager(s *Store, e *Engine) *MonitorManager {
	m := &MonitorManager{store: s, engine: e, active: map[string]*activeMonitor{}, runs: map[string][]MonitorRun{}, path: filepath.Join(s.dir, "monitor-runs.json")}
	readJSON(m.path, &m.runs)
	return m
}

func (m *MonitorManager) Reconcile(ws *Workspace) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]*Monitor{}
	for _, mo := range ws.Monitors {
		if mo.Enabled && mo.IntervalSec > 0 {
			want[mo.ID] = mo
		}
	}
	for id, a := range m.active {
		if w, ok := want[id]; !ok || *w != a.cfg {
			a.cancel()
			delete(m.active, id)
		}
	}
	for id, mo := range want {
		if _, ok := m.active[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		a := &activeMonitor{cfg: *mo, cancel: cancel}
		m.active[id] = a
		go m.loop(ctx, a.cfg)
	}
}

func (m *MonitorManager) loop(ctx context.Context, cfg Monitor) {
	interval := time.Duration(cfg.IntervalSec) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	t := time.NewTimer(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.RunNow(ctx, cfg)
			t.Reset(interval)
		}
	}
}

func (m *MonitorManager) RunNow(ctx context.Context, cfg Monitor) MonitorRun {
	ws := m.store.Workspace()
	run := MonitorRun{Time: time.Now().UnixMilli()}
	sum, err := m.engine.Run(ctx, ws, RunOptions{CollectionID: cfg.CollectionID, EnvID: cfg.EnvironmentID, Iterations: 1}, func(ev RunEvent) {
		if ev.Type == "result" && ev.Result != nil {
			r := ev.Result
			if r.Error != "" {
				run.Failures = append(run.Failures, r.Name+": "+r.Error)
			}
			for _, t := range r.Tests {
				if !t.Passed && !t.Skipped {
					run.Failures = append(run.Failures, r.Name+" › "+t.Name+": "+t.Error)
				}
			}
		}
	})
	run.Summary = sum
	if err != nil {
		run.Error = err.Error()
	}
	m.mu.Lock()
	rs := append([]MonitorRun{run}, m.runs[cfg.ID]...)
	if len(rs) > 100 {
		rs = rs[:100]
	}
	m.runs[cfg.ID] = rs
	writeJSONAtomic(m.path, m.runs)
	m.mu.Unlock()
	if cfg.Notify && (run.Error != "" || len(run.Failures) > 0) {
		msg := fmt.Sprintf("%d failure(s)", len(run.Failures))
		if run.Error != "" {
			msg = run.Error
		}
		exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", msg, "Lightpost monitor: "+cfg.Name)).Run()
	}
	return run
}

func (m *MonitorManager) Runs(id string) []MonitorRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MonitorRun{}, m.runs[id]...)
}
