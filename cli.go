package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
)

// cliRun is a newman-style headless runner:
//
//	lightpost run collection.json [-e env.json] [-g globals.json] [-d data.csv] [-n 3]
//
// The collection may also be the name or id of a collection in the workspace.
func cliRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	envFile := fs.String("e", "", "environment file (Postman format) or environment name in the workspace")
	globalsFile := fs.String("g", "", "globals file (Postman format)")
	dataFile := fs.String("d", "", "iteration data file (CSV or JSON)")
	iters := fs.Int("n", 0, "iteration count")
	delay := fs.Int("delay", 0, "delay between requests in ms")
	folder := fs.String("folder", "", "run only this folder (name)")
	bail := fs.Bool("bail", false, "stop on first failure")
	insecure := fs.Bool("k", false, "disable TLS verification")
	reporter := fs.String("reporter", "cli", "cli | json")
	timeout := fs.Int("timeout", 0, "request timeout in ms")
	var envVars multiFlag
	fs.Var(&envVars, "env-var", "set environment variable key=value (repeatable)")
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lightpost run <collection.json|name> [flags]")
		fs.PrintDefaults()
		return 2
	}
	target := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	app, err := newApp(dataDir(), false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stored := app.store.Workspace()
	// Work on an in-memory workspace so CLI runs never rewrite saved data.
	ws := defaultWorkspace()
	ws.Settings = stored.Settings
	ws.Globals = stored.Globals
	if *insecure {
		ws.Settings.VerifySSL = false
	}
	if *timeout > 0 {
		ws.Settings.TimeoutMs = *timeout
	}
	var col *Collection
	if b, err := os.ReadFile(target); err == nil {
		res, err := importAny(string(b))
		if err != nil || len(res.Collections) == 0 {
			fmt.Fprintln(os.Stderr, "cannot import collection:", err)
			return 1
		}
		col = res.Collections[0]
	} else {
		for _, c := range stored.Collections {
			if c.ID == target || strings.EqualFold(c.Name, target) {
				col = c
			}
		}
		if col == nil {
			fmt.Fprintf(os.Stderr, "collection %q not found (not a file, not in workspace)\n", target)
			return 1
		}
	}
	ws.Collections = []*Collection{col}

	env := &Environment{ID: "cli-env", Name: "cli"}
	if *envFile != "" {
		if b, err := os.ReadFile(*envFile); err == nil {
			e, _, err := importPostmanEnvironment(b)
			if err != nil {
				fmt.Fprintln(os.Stderr, "environment:", err)
				return 1
			}
			env.Values = e.Values
		} else {
			found := false
			for _, e := range stored.Environments {
				if e.ID == *envFile || strings.EqualFold(e.Name, *envFile) {
					env.Values, found = e.Values, true
				}
			}
			if !found {
				fmt.Fprintf(os.Stderr, "environment %q not found\n", *envFile)
				return 1
			}
		}
	}
	for _, kv := range envVars {
		k, v, _ := strings.Cut(kv, "=")
		env.Values = append(env.Values, KV{Key: k, Value: v, Enabled: true})
	}
	ws.Environments = []*Environment{env}
	if *globalsFile != "" {
		b, err := os.ReadFile(*globalsFile)
		if err == nil {
			if g, _, err := importPostmanEnvironment(b); err == nil {
				ws.Globals = g.Values
			}
		}
	}
	opt := RunOptions{CollectionID: col.ID, EnvID: env.ID, Iterations: *iters, DelayMs: *delay, StopOnError: *bail}
	if *dataFile != "" {
		b, err := os.ReadFile(*dataFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		opt.Data, err = parseDataFile(*dataFile, string(b))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if *folder != "" {
		var find func([]*Item) string
		find = func(items []*Item) string {
			for _, it := range items {
				if it.Type == "folder" {
					if strings.EqualFold(it.Name, *folder) {
						return it.ID
					}
					if id := find(it.Items); id != "" {
						return id
					}
				}
			}
			return ""
		}
		if opt.FolderID = find(col.Items); opt.FolderID == "" {
			fmt.Fprintf(os.Stderr, "folder %q not found\n", *folder)
			return 1
		}
	}
	// The CLI store must not write history into the user's workspace.
	app.engine.store = &Store{dir: os.TempDir(), ws: ws}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	tty := *reporter == "cli"
	var events []RunEvent
	if tty {
		fmt.Printf("\n\033[1m%s\033[0m\n", col.Name)
	}
	lastIter := -1
	sum, err := app.engine.Run(ctx, ws, opt, func(ev RunEvent) {
		if !tty {
			if ev.Type == "result" && ev.Result.Response != nil {
				ev.Result.Response.Body = truncate(ev.Result.Response.Body, 4096)
			}
			events = append(events, ev)
			return
		}
		if ev.Type != "result" {
			return
		}
		r := ev.Result
		if ev.Iteration != lastIter && (opt.Iterations > 1 || len(opt.Data) > 1) {
			fmt.Printf("\n\033[2mIteration %d\033[0m\n", ev.Iteration+1)
			lastIter = ev.Iteration
		}
		if r.Skipped {
			fmt.Printf("→ %s \033[2m(skipped)\033[0m\n", r.Name)
			return
		}
		if r.Error != "" {
			fmt.Printf("→ %s\n  \033[31m✗ %s\033[0m\n", r.Name, r.Error)
		} else {
			fmt.Printf("→ %s\n  %s %s [%d %s, %s, %.0fms]\n", r.Name, r.Sent.Method, r.Sent.URL, r.Response.Code, r.Response.Status, humanSize(r.Response.Size), r.Response.TimeMs)
		}
		for _, t := range r.Tests {
			switch {
			case t.Skipped:
				fmt.Printf("  \033[2m- %s (skipped)\033[0m\n", t.Name)
			case t.Passed:
				fmt.Printf("  \033[32m✓\033[0m %s\n", t.Name)
			default:
				fmt.Printf("  \033[31m✗ %s\033[0m\n    %s\n", t.Name, t.Error)
			}
		}
		for _, l := range r.Logs {
			fmt.Printf("  \033[2m┆ %s\033[0m\n", l.Message)
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !tty {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"collection": col.Name, "summary": sum, "events": events})
	} else {
		fmt.Printf("\n┌─────────────────┬──────────┬──────────┐\n")
		fmt.Printf("│                 │ executed │   failed │\n")
		fmt.Printf("├─────────────────┼──────────┼──────────┤\n")
		fmt.Printf("│ requests        │ %8d │ %8d │\n", sum.Requests, sum.Failed)
		fmt.Printf("│ assertions      │ %8d │ %8d │\n", sum.Tests, sum.TestsFailed)
		fmt.Printf("└─────────────────┴──────────┴──────────┘\n")
		fmt.Printf("total run duration: %dms, average response time: %.0fms\n\n", sum.DurationMs, sum.AvgRespMs)
	}
	if sum.Failed > 0 || sum.TestsFailed > 0 {
		return 1
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}
