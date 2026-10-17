package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store persists the workspace and history as JSON files in the data directory.
// The UI is the single writer of the workspace; history and monitor runs are
// written by the server.
type Store struct {
	dir     string
	mu      sync.RWMutex
	ws      *Workspace
	history []HistoryEntry
	onSave  func(*Workspace)
}

func dataDir() string {
	if d := os.Getenv("LIGHTPOST_DATA"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Lightpost")
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, ws: defaultWorkspace()}
	if b, err := os.ReadFile(filepath.Join(dir, "workspace.json")); err == nil {
		ws := defaultWorkspace()
		if json.Unmarshal(b, ws) == nil {
			s.ws = ws
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "history.json")); err == nil {
		json.Unmarshal(b, &s.history)
	}
	return s, nil
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Workspace() *Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ws
}

func (s *Store) SetWorkspace(ws *Workspace) error {
	s.mu.Lock()
	s.ws = ws
	err := writeJSONAtomic(filepath.Join(s.dir, "workspace.json"), ws)
	cb := s.onSave
	s.mu.Unlock()
	if cb != nil {
		cb(ws)
	}
	return err
}

func (s *Store) AddHistory(req *Request, status int, dur time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.ws.Settings.HistoryLimit
	if limit <= 0 {
		limit = 300
	}
	cp := *req
	cp.Examples = nil
	s.history = append([]HistoryEntry{{ID: newID(), Time: time.Now().UnixMilli(), Request: &cp, Status: status, Duration: dur.Milliseconds()}}, s.history...)
	if len(s.history) > limit {
		s.history = s.history[:limit]
	}
	writeJSONAtomic(filepath.Join(s.dir, "history.json"), s.history)
}

func (s *Store) History() []HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.history
}

func (s *Store) ClearHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = nil
	writeJSONAtomic(filepath.Join(s.dir, "history.json"), []HistoryEntry{})
}
