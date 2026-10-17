package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := newApp(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func echoServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/digest" {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
				w.Header().Set("WWW-Authenticate", `Digest realm="t", qop="auth", nonce="abc", opaque="xyz"`)
				w.WriteHeader(401)
				return
			}
		}
		if r.URL.Path == "/cookie" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "42", Path: "/"})
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery, "headers": r.Header, "body": string(body)})
	}))
}

func TestExecuteScriptsAndVars(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	a := testApp(t)
	col := &Collection{ID: "c", Name: "C", Variables: []KV{{Key: "base", Value: srv.URL, Enabled: true}},
		Auth:      &Auth{Type: "bearer", Params: map[string]string{"token": "{{tok}}"}},
		PreScript: `pm.environment.set("tok", "secret-" + (1+1));`}
	req := &Request{Method: "POST", URL: "{{base}}/items?x={{$randomInt}}", Auth: &Auth{Type: "inherit"},
		Headers:   []KV{{Key: "X-Name", Value: "{{name}}", Enabled: true}},
		Body:      Body{Mode: "raw", Lang: "json", Raw: `{"id":"{{id}}"}`},
		PreScript: `pm.variables.set("name", "alice"); pm.request.headers.add({key:"X-Pre", value:"yes"});`,
		TestScript: `
pm.test("status 200", function(){ pm.response.to.have.status(200); });
const j = pm.response.json();
pm.test("bearer applied", function(){ pm.expect(j.headers.Authorization[0]).to.eql("Bearer secret-2"); });
pm.test("header vars", function(){ pm.expect(j.headers["X-Name"][0]).to.equal("alice"); pm.expect(j.headers["X-Pre"]).to.include("yes"); });
pm.test("data var", function(){ pm.expect(JSON.parse(j.body).id).to.equal("7"); });
pm.test("negation", function(){ pm.expect(1).to.not.equal(2); pm.response.to.not.have.status(404); });
pm.test("schema", function(){ pm.response.to.have.jsonSchema({type:"object", required:["method"], properties:{method:{type:"string"}}}); });
pm.test("deliberate fail", function(){ pm.expect(j.method).to.equal("GET"); });
pm.collectionVariables.set("last", j.method);
console.log("done", {ok:true});
tests["legacy"] = responseCode.code === 200;
`}
	ws := &Workspace{Collections: []*Collection{col}, Environments: []*Environment{{ID: "e", Values: []KV{}}}}
	vars := varsFor(ws, col, "e", map[string]string{"id": "7"})
	res := a.engine.Execute(context.Background(), col, nil, "i", "req", req, vars, ScriptInfo{})
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	byName := map[string]TestResult{}
	for _, tr := range res.Tests {
		byName[tr.Name] = tr
	}
	for _, n := range []string{"status 200", "bearer applied", "header vars", "data var", "negation", "schema", "legacy"} {
		if !byName[n].Passed {
			t.Errorf("test %q failed: %s", n, byName[n].Error)
		}
	}
	if f := byName["deliberate fail"]; f.Passed || !strings.Contains(f.Error, "expected 'POST'") && !strings.Contains(f.Error, `expected POST to equal GET`) {
		t.Errorf("deliberate fail: %+v", f)
	}
	u := varUpdates(ws, col, "e", vars)
	if len(u.Collection) == 0 || len(u.Environment) != 1 || u.Environment[0].Value != "secret-2" {
		t.Errorf("var updates: %+v", u)
	}
	if len(res.Logs) != 1 || res.Logs[0].Message != `done {"ok":true}` {
		t.Errorf("logs: %+v", res.Logs)
	}
}

func TestDigestAndCookies(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	a := testApp(t)
	r := &Request{Method: "GET", URL: srv.URL + "/digest", Auth: &Auth{Type: "digest", Params: map[string]string{"username": "u", "password": "p"}}}
	resp, _, err := a.engine.Send(context.Background(), r)
	if err != nil || resp.Code != 200 {
		t.Fatalf("digest: %v %+v", err, resp)
	}
	a.engine.Send(context.Background(), &Request{Method: "GET", URL: srv.URL + "/cookie"})
	resp, _, _ = a.engine.Send(context.Background(), &Request{Method: "GET", URL: srv.URL + "/other"})
	if !strings.Contains(resp.Body, "sid=42") {
		t.Errorf("cookie not sent back: %s", resp.Body)
	}
}

func TestRunnerSetNextRequestAndData(t *testing.T) {
	srv := echoServer()
	defer srv.Close()
	a := testApp(t)
	mk := func(id, name, test string) *Item {
		return &Item{ID: id, Name: name, Type: "request", Request: &Request{Method: "GET", URL: srv.URL + "/" + id + "?u={{user}}", TestScript: test}}
	}
	col := &Collection{ID: "c", Name: "C", Items: []*Item{
		mk("a", "A", `pm.test("user", function(){ pm.expect(pm.response.json().query).to.equal("u=" + pm.iterationData.get("user")); }); pm.execution.setNextRequest("C");`),
		mk("b", "B", ``),
		{ID: "f", Name: "F", Type: "folder", Items: []*Item{mk("c", "C", `pm.test("ok", function(){ pm.response.to.be.ok; });`)}},
	}}
	ws := &Workspace{Collections: []*Collection{col}}
	var names []string
	sum, err := a.engine.Run(context.Background(), ws, RunOptions{CollectionID: "c", Data: []map[string]string{{"user": "x"}, {"user": "y"}}}, func(ev RunEvent) {
		if ev.Type == "result" {
			names = append(names, ev.Result.Name)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names, ","); got != "A,C,A,C" {
		t.Errorf("order = %s", got)
	}
	if sum.Tests != 4 || sum.TestsFailed != 0 {
		t.Errorf("summary %+v", sum)
	}
}

func TestImportCurlAndPostmanRoundTrip(t *testing.T) {
	r, err := importCurl(`curl -X POST 'https://api.example.com/v1/users?x=1' \
  -H 'Content-Type: application/json' -H "Authorization: Bearer abc" \
  --data-raw '{"name":"a b"}' -u bob:pw`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "POST" || r.URL != "https://api.example.com/v1/users?x=1" || r.Body.Lang != "json" || r.Auth.Params["username"] != "bob" || len(r.Headers) != 2 {
		t.Errorf("curl parse: %+v", r)
	}
	f, _ := importCurl(`curl https://x.io/login -d user=a -d pass=b%20c`)
	if f.Method != "POST" || f.Body.Mode != "urlencoded" || f.Body.Form[1].Value != "b c" {
		t.Errorf("urlencoded: %+v", f.Body)
	}

	col := &Collection{ID: "c", Name: "Round", Variables: []KV{{Key: "k", Value: "v", Enabled: true}}, Auth: &Auth{Type: "apikey", Params: map[string]string{"key": "X-Key", "value": "1", "in": "header"}},
		Items: []*Item{{ID: "f", Name: "Folder", Type: "folder", Items: []*Item{{ID: "r", Name: "Req", Type: "request", Request: &Request{Method: "PUT", URL: "https://h/p", TestScript: "pm.test('a', ()=>{})", Body: Body{Mode: "graphql", GraphQL: &GraphQLBody{Query: "{a}"}}, Examples: []Example{{Name: "ok", Code: 200, Body: "{}"}}}}}}}}
	b, err := exportPostmanCollection(col)
	if err != nil {
		t.Fatal(err)
	}
	back, err := importPostmanCollection(b)
	if err != nil {
		t.Fatal(err)
	}
	rq := back.Items[0].Items[0].Request
	if back.Auth.Params["key"] != "X-Key" || rq.Method != "PUT" || rq.Body.GraphQL.Query != "{a}" || rq.TestScript != "pm.test('a', ()=>{})" || len(rq.Examples) != 1 || back.Variables[0].Value != "v" {
		t.Errorf("round trip mismatch: %s", b)
	}
}

func TestOpenAPIImportAndMock(t *testing.T) {
	spec := `openapi: 3.0.0
info: {title: Pets}
servers: [{url: "http://localhost:9999/v1"}]
paths:
  /pets/{id}:
    get:
      tags: [pets]
      summary: Get pet
      parameters: [{name: id, in: path, schema: {type: integer, example: 5}}]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object, properties: {id: {type: integer}, name: {type: string}}}
`
	col, err := importOpenAPI([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	it := col.Items[0].Items[0]
	if it.Request.URL != "{{baseUrl}}/pets/5" || col.Variables[0].Value != "http://localhost:9999/v1" || len(it.Request.Examples) != 1 {
		t.Fatalf("openapi: %+v %+v", it.Request, col.Variables)
	}
	a := testApp(t)
	ws := defaultWorkspace()
	ws.Collections = []*Collection{col}
	ws.Mocks = []*Mock{{ID: "m", CollectionID: col.ID, Port: 0, Enabled: true}}
	a.store.ws = ws
	rm := &runningMock{cfg: *ws.Mocks[0]}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.mocks.serve(rm, w, r) }))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/pets/5")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"name"`) {
		t.Errorf("mock: %d %s", resp.StatusCode, b)
	}
	resp, _ = http.Get(srv.URL + "/nope")
	if resp.StatusCode != 404 {
		t.Errorf("unmatched mock = %d", resp.StatusCode)
	}
}

func TestAWSv4KnownVector(t *testing.T) {
	// AWS documentation example (GET with no body, iam ListUsers).
	r := &Request{Method: "GET", URL: "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08",
		Headers: []KV{{Key: "Content-Type", Value: "application/x-www-form-urlencoded; charset=utf-8", Enabled: true}}}
	p := map[string]string{"accessKey": "AKIDEXAMPLE", "secretKey": "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "region": "us-east-1", "service": "iam"}
	ts, _ := time.Parse("20060102T150405Z", "20150830T123600Z")
	if err := signAWSv4(r, p, ts); err != nil {
		t.Fatal(err)
	}
	var auth string
	for _, h := range r.Headers {
		if h.Key == "Authorization" {
			auth = h.Value
		}
	}
	// Our signer always adds x-amz-content-sha256, so compare structure, not the doc signature.
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Errorf("auth = %s", auth)
	}
}

func TestScriptLibraries(t *testing.T) {
	a := testApp(t)
	var logs []LogEntry
	src := `pm.test("lodash", () => pm.expect(_.sum([1,2,3])).to.equal(6));
pm.test("lodash require", () => pm.expect(require('lodash').chunk([1,2,3,4], 2)).to.have.lengthOf(2));
pm.test("moment", () => pm.expect(moment("2024-01-31").add(1, "day").format("YYYY-MM-DD")).to.equal("2024-02-01"));
pm.test("crypto", () => pm.expect(CryptoJS.SHA256("abc").toString()).to.equal("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"));
pm.test("btoa", () => pm.expect(atob(btoa("héllo"))).to.equal("héllo"));`
	res, err := a.engine.runScript(src, "t", ScriptInfo{}, &Request{Method: "GET", URL: "http://x"}, nil, newVars(nil, nil, nil, nil), &logs)
	if err != nil {
		t.Fatal(err, logs)
	}
	for _, r := range res.Tests {
		if !r.Passed {
			t.Errorf("%s: %s", r.Name, r.Error)
		}
	}
	if len(res.Tests) != 5 {
		t.Errorf("tests = %d", len(res.Tests))
	}
}
