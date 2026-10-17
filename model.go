package main

// Data model. Deliberately close to the Postman v2.1 collection format so that
// import/export is a thin mapping, but flattened where v2.1 is awkward (URL is a
// plain string, auth params are a map).

type KV struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Enabled     bool   `json:"enabled"`
	Type        string `json:"type,omitempty"` // "text" | "file" (form-data) | "secret"
	Description string `json:"description,omitempty"`
}

type GraphQLBody struct {
	Query     string `json:"query"`
	Variables string `json:"variables"`
}

type Body struct {
	Mode    string       `json:"mode"` // none | raw | urlencoded | formdata | binary | graphql
	Raw     string       `json:"raw,omitempty"`
	Lang    string       `json:"lang,omitempty"` // json | text | xml | html | javascript
	Form    []KV         `json:"form,omitempty"`
	File    string       `json:"file,omitempty"`
	GraphQL *GraphQLBody `json:"graphql,omitempty"`
}

type Auth struct {
	Type   string            `json:"type"` // inherit | none | basic | bearer | apikey | digest | oauth2 | awsv4 | hawk
	Params map[string]string `json:"params,omitempty"`
}

type RequestSettings struct {
	FollowRedirects *bool `json:"followRedirects,omitempty"`
	MaxRedirects    int   `json:"maxRedirects,omitempty"`
	VerifySSL       *bool `json:"verifySSL,omitempty"`
	TimeoutMs       int   `json:"timeoutMs,omitempty"`
}

type Example struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Method  string `json:"method,omitempty"`
	URL     string `json:"url,omitempty"`
	Code    int    `json:"code"`
	Status  string `json:"status,omitempty"`
	Headers []KV   `json:"headers,omitempty"`
	Body    string `json:"body"`
}

type Request struct {
	Method      string          `json:"method"`
	URL         string          `json:"url"`
	PathVars    []KV            `json:"pathVars,omitempty"`
	Headers     []KV            `json:"headers,omitempty"`
	Body        Body            `json:"body"`
	Auth        *Auth           `json:"auth,omitempty"`
	PreScript   string          `json:"preScript,omitempty"`
	TestScript  string          `json:"testScript,omitempty"`
	Settings    RequestSettings `json:"settings"`
	Examples    []Example       `json:"examples,omitempty"`
	Description string          `json:"description,omitempty"`
}

// Item is a folder (Items set) or a request (Request set).
type Item struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"` // folder | request | realtime
	Items       []*Item  `json:"items,omitempty"`
	Request     *Request `json:"request,omitempty"`
	Auth        *Auth    `json:"auth,omitempty"`
	PreScript   string   `json:"preScript,omitempty"`
	TestScript  string   `json:"testScript,omitempty"`
	Description string   `json:"description,omitempty"`
}

type Collection struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Items       []*Item `json:"items"`
	Auth        *Auth   `json:"auth,omitempty"`
	PreScript   string  `json:"preScript,omitempty"`
	TestScript  string  `json:"testScript,omitempty"`
	Variables   []KV    `json:"variables,omitempty"`
}

type Environment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Values []KV   `json:"values"`
}

type Mock struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CollectionID string `json:"collectionId"`
	Port         int    `json:"port"`
	DelayMs      int    `json:"delayMs,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type Monitor struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CollectionID  string `json:"collectionId"`
	EnvironmentID string `json:"environmentId,omitempty"`
	IntervalSec   int    `json:"intervalSec"`
	Enabled       bool   `json:"enabled"`
	Notify        bool   `json:"notify"`
}

type ClientCert struct {
	Host     string `json:"host"`
	CertPath string `json:"certPath"`
	KeyPath  string `json:"keyPath"`
}

type Settings struct {
	Theme           string       `json:"theme,omitempty"`
	TimeoutMs       int          `json:"timeoutMs,omitempty"`
	VerifySSL       bool         `json:"verifySSL"`
	FollowRedirects bool         `json:"followRedirects"`
	ProxyURL        string       `json:"proxyUrl,omitempty"`
	CACertPath      string       `json:"caCertPath,omitempty"`
	ClientCerts     []ClientCert `json:"clientCerts,omitempty"`
	HistoryLimit    int          `json:"historyLimit,omitempty"`
}

type Workspace struct {
	Collections  []*Collection  `json:"collections"`
	Environments []*Environment `json:"environments"`
	Globals      []KV           `json:"globals"`
	ActiveEnvID  string         `json:"activeEnvId,omitempty"`
	Mocks        []*Mock        `json:"mocks"`
	Monitors     []*Monitor     `json:"monitors"`
	Settings     Settings       `json:"settings"`
}

type HistoryEntry struct {
	ID       string   `json:"id"`
	Time     int64    `json:"time"`
	Request  *Request `json:"request"`
	Status   int      `json:"status"`
	Duration int64    `json:"durationMs"`
}

func defaultWorkspace() *Workspace {
	return &Workspace{
		Collections:  []*Collection{},
		Environments: []*Environment{},
		Globals:      []KV{},
		Mocks:        []*Mock{},
		Monitors:     []*Monitor{},
		Settings:     Settings{VerifySSL: true, FollowRedirects: true, TimeoutMs: 30000, HistoryLimit: 300},
	}
}

func (w *Workspace) collection(id string) *Collection {
	for _, c := range w.Collections {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (w *Workspace) environment(id string) *Environment {
	for _, e := range w.Environments {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// findPath returns the chain of folders leading to item id (exclusive) and the item.
func findPath(items []*Item, id string) ([]*Item, *Item) {
	for _, it := range items {
		if it.ID == id {
			return nil, it
		}
		if it.Type == "folder" {
			if chain, found := findPath(it.Items, id); found != nil {
				return append([]*Item{it}, chain...), found
			}
		}
	}
	return nil, nil
}

// flatten lists request items depth-first with their ancestor folders.
type flatItem struct {
	Folders []*Item
	Item    *Item
}

func flatten(items []*Item, parents []*Item, out *[]flatItem) {
	for _, it := range items {
		if it.Type == "folder" {
			p := append(append([]*Item{}, parents...), it)
			flatten(it.Items, p, out)
		} else if it.Request != nil && it.Type != "realtime" {
			*out = append(*out, flatItem{Folders: append([]*Item{}, parents...), Item: it})
		}
	}
}
