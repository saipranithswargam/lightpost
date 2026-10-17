package main

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Jar is a small persistent, enumerable cookie jar (net/http/cookiejar cannot
// be listed or edited, which the cookie manager needs).
type Jar struct {
	mu      sync.Mutex
	path    string
	Cookies []*StoredCookie
}

type StoredCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires,omitempty"`
	HostOnly bool      `json:"hostOnly"`
	Secure   bool      `json:"secure"`
	HTTPOnly bool      `json:"httpOnly"`
}

func openJar(dir string) *Jar {
	j := &Jar{path: filepath.Join(dir, "cookies.json")}
	readJSON(j.path, &j.Cookies)
	return j
}

func (j *Jar) save() { writeJSONAtomic(j.path, j.Cookies) }

func domainMatch(host, domain string, hostOnly bool) bool {
	host, domain = strings.ToLower(host), strings.ToLower(strings.TrimPrefix(domain, "."))
	if hostOnly {
		return host == domain
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	for _, c := range cookies {
		sc := &StoredCookie{Name: c.Name, Value: c.Value, Path: c.Path, Secure: c.Secure, HTTPOnly: c.HttpOnly}
		if c.Domain == "" {
			sc.Domain, sc.HostOnly = u.Hostname(), true
		} else {
			sc.Domain = strings.TrimPrefix(strings.ToLower(c.Domain), ".")
			if !domainMatch(u.Hostname(), sc.Domain, false) {
				continue
			}
		}
		if sc.Path == "" || !strings.HasPrefix(sc.Path, "/") {
			sc.Path = "/"
		}
		delete := false
		if c.MaxAge < 0 {
			delete = true
		} else if c.MaxAge > 0 {
			sc.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		} else if !c.Expires.IsZero() {
			sc.Expires = c.Expires
			delete = c.Expires.Before(now)
		}
		j.remove(sc.Domain, sc.Path, sc.Name)
		if !delete {
			j.Cookies = append(j.Cookies, sc)
		}
	}
	j.save()
}

func (j *Jar) remove(domain, path, name string) {
	out := j.Cookies[:0]
	for _, c := range j.Cookies {
		if !(c.Domain == domain && c.Path == path && c.Name == name) {
			out = append(out, c)
		}
	}
	j.Cookies = out
}

func (j *Jar) CookiesFor(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	path := u.Path
	if path == "" {
		path = "/"
	}
	var out []*http.Cookie
	for _, c := range j.Cookies {
		if !c.Expires.IsZero() && c.Expires.Before(now) {
			continue
		}
		if c.Secure && u.Scheme != "https" && u.Scheme != "wss" {
			continue
		}
		if !domainMatch(u.Hostname(), c.Domain, c.HostOnly) || !strings.HasPrefix(path, c.Path) {
			continue
		}
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	return out
}

// http.CookieJar requires the method name Cookies, which collides with the
// field; jarAdapter bridges it.
type jarAdapter struct{ j *Jar }

func (a jarAdapter) SetCookies(u *url.URL, c []*http.Cookie) { a.j.SetCookies(u, c) }
func (a jarAdapter) Cookies(u *url.URL) []*http.Cookie       { return a.j.CookiesFor(u) }

func (j *Jar) List() []*StoredCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]*StoredCookie{}, j.Cookies...)
}

func (j *Jar) Replace(cs []*StoredCookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Cookies = cs
	j.save()
}
