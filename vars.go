package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	mrand "math/rand/v2"
	"regexp"
	"strings"
	"time"
)

// Scope is an ordered-irrelevant variable set that remembers deletions so the
// caller can write changes back to the persisted KV list.
type Scope struct {
	Values  map[string]string
	Deleted map[string]bool
	Changed bool
}

func scopeFrom(kvs []KV) *Scope {
	s := &Scope{Values: map[string]string{}, Deleted: map[string]bool{}}
	for _, kv := range kvs {
		if kv.Enabled && kv.Key != "" {
			s.Values[kv.Key] = kv.Value
		}
	}
	return s
}

func (s *Scope) Get(k string) (string, bool) { v, ok := s.Values[k]; return v, ok }
func (s *Scope) Set(k, v string) {
	s.Values[k] = v
	delete(s.Deleted, k)
	s.Changed = true
}
func (s *Scope) Unset(k string) {
	delete(s.Values, k)
	s.Deleted[k] = true
	s.Changed = true
}
func (s *Scope) Clear() {
	for k := range s.Values {
		s.Deleted[k] = true
	}
	s.Values = map[string]string{}
	s.Changed = true
}

// apply writes scope changes back onto a KV list, preserving order and
// disabled entries.
func (s *Scope) apply(kvs []KV) []KV {
	out := make([]KV, 0, len(kvs))
	seen := map[string]bool{}
	for _, kv := range kvs {
		if kv.Enabled && s.Deleted[kv.Key] {
			continue
		}
		if v, ok := s.Values[kv.Key]; ok && kv.Enabled {
			kv.Value = v
		}
		seen[kv.Key] = true
		out = append(out, kv)
	}
	for k, v := range s.Values {
		if !seen[k] {
			out = append(out, KV{Key: k, Value: v, Enabled: true})
		}
	}
	return out
}

// Vars resolves {{name}} with Postman precedence:
// local > iteration data > environment > collection > globals.
type Vars struct {
	Local, Data, Env, Collection, Globals *Scope
}

func newVars(globals, collection, env []KV, data map[string]string) *Vars {
	d := &Scope{Values: map[string]string{}, Deleted: map[string]bool{}}
	for k, v := range data {
		d.Values[k] = v
	}
	return &Vars{Local: scopeFrom(nil), Data: d, Env: scopeFrom(env), Collection: scopeFrom(collection), Globals: scopeFrom(globals)}
}

func (v *Vars) Lookup(k string) (string, bool) {
	for _, s := range []*Scope{v.Local, v.Data, v.Env, v.Collection, v.Globals} {
		if val, ok := s.Get(k); ok {
			return val, true
		}
	}
	return "", false
}

var varRe = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Resolve substitutes variables; nested references resolve up to a fixed depth.
func (v *Vars) Resolve(s string) string {
	for i := 0; i < 5 && strings.Contains(s, "{{"); i++ {
		next := varRe.ReplaceAllStringFunc(s, func(m string) string {
			name := strings.TrimSpace(m[2 : len(m)-2])
			if strings.HasPrefix(name, "$") {
				if dv, ok := dynamicVar(name); ok {
					return dv
				}
			}
			if v != nil {
				if val, ok := v.Lookup(name); ok {
					return val
				}
			}
			return m
		})
		if next == s {
			break
		}
		s = next
	}
	return s
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func uuid4() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

var (
	firstNames = []string{"Ava", "Liam", "Noah", "Emma", "Olivia", "Mia", "Ethan", "Aria", "Kai", "Zoe", "Ravi", "Priya", "Lucas", "Sofia"}
	lastNames  = []string{"Smith", "Patel", "Garcia", "Kim", "Nguyen", "Brown", "Rossi", "Müller", "Silva", "Khan", "Lopez", "Chen"}
	words      = []string{"alpha", "bravo", "cloud", "delta", "ember", "falcon", "galaxy", "harbor", "ion", "jade", "kernel", "lumen", "matrix", "nova"}
	domains    = []string{"example.com", "test.io", "mail.dev", "sample.org"}
	cities     = []string{"London", "Austin", "Berlin", "Tokyo", "Pune", "Toronto", "Lisbon", "Seoul"}
	countries  = []string{"United Kingdom", "United States", "Germany", "Japan", "India", "Canada", "Portugal", "Korea"}
	colors     = []string{"red", "green", "blue", "teal", "orange", "purple", "black", "white"}
)

func pick(a []string) string { return a[mrand.IntN(len(a))] }

// dynamicVar implements the commonly used Postman dynamic variables.
func dynamicVar(name string) (string, bool) {
	now := time.Now()
	switch name {
	case "$guid", "$randomUUID":
		return uuid4(), true
	case "$timestamp":
		return fmt.Sprint(now.Unix()), true
	case "$isoTimestamp":
		return now.UTC().Format("2006-01-02T15:04:05.000Z"), true
	case "$randomInt":
		n, _ := rand.Int(rand.Reader, big.NewInt(1001))
		return n.String(), true
	case "$randomFirstName":
		return pick(firstNames), true
	case "$randomLastName":
		return pick(lastNames), true
	case "$randomFullName", "$randomUserName":
		if name == "$randomUserName" {
			return strings.ToLower(pick(firstNames)) + fmt.Sprint(mrand.IntN(999)), true
		}
		return pick(firstNames) + " " + pick(lastNames), true
	case "$randomEmail", "$randomExampleEmail":
		return strings.ToLower(pick(firstNames)) + "." + strings.ToLower(pick(lastNames)) + "@" + pick(domains), true
	case "$randomWord":
		return pick(words), true
	case "$randomWords":
		return pick(words) + " " + pick(words) + " " + pick(words), true
	case "$randomLoremSentence":
		return "Lorem ipsum dolor sit amet, consectetur adipiscing elit.", true
	case "$randomPhoneNumber":
		return fmt.Sprintf("%03d-%03d-%04d", mrand.IntN(900)+100, mrand.IntN(1000), mrand.IntN(10000)), true
	case "$randomCity":
		return pick(cities), true
	case "$randomCountry":
		return pick(countries), true
	case "$randomColor":
		return pick(colors), true
	case "$randomBoolean":
		return fmt.Sprint(mrand.IntN(2) == 1), true
	case "$randomIP":
		return fmt.Sprintf("%d.%d.%d.%d", mrand.IntN(223)+1, mrand.IntN(256), mrand.IntN(256), mrand.IntN(256)), true
	case "$randomPassword":
		return randHex(8), true
	case "$randomAlphaNumeric":
		return randHex(1)[:1], true
	case "$randomPrice":
		return fmt.Sprintf("%d.%02d", mrand.IntN(1000), mrand.IntN(100)), true
	case "$randomDateFuture":
		return now.Add(time.Duration(mrand.IntN(365*24)+1) * time.Hour).UTC().Format(time.RFC1123), true
	case "$randomDatePast":
		return now.Add(-time.Duration(mrand.IntN(365*24)+1) * time.Hour).UTC().Format(time.RFC1123), true
	case "$randomUrl":
		return "https://" + pick(words) + "." + pick(domains), true
	}
	return "", false
}
