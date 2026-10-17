# Lightpost

A lightweight, local-first API client with Postman's feature set. It is a
single 22 MB Go binary that uses the system WebKit view (no Electron, no
account, no cloud).

| | Lightpost | Postman 12.32 (measured on this Mac) |
|---|---|---|
| On disk | 22 MB | 463 MB |
| RAM, window open & idle | ~215 MB total (117 MB app + ~94 MB system WebKit helpers) | ~808 MB (measured with the workspace open at the time) |
| Idle CPU | 0.0% (event-driven, no polling) | — |
| Sign-in required | No | Yes for most features |

See `research/FINDINGS.md` for the REA analysis of Postman that informed this
design.

Third-party JS: `jslib/lodash.min.js` and `jslib/moment.min.js` (MIT, licenses alongside).

## Run

```sh
./build.sh                    # builds ./lightpost and ./Lightpost.app
open Lightpost.app            # native window
./lightpost --browser         # or use your default browser
./lightpost run "My API" -e Dev -d users.csv -n 3   # headless CLI runner
```

Data lives in `~/Library/Application Support/Lightpost` (override with
`LIGHTPOST_DATA`). It is plain JSON: workspace, history, cookies and monitor
runs.

## Features

**Requests**
- All HTTP methods, including custom ones.
- Query params table synced with the URL; `:path` variables.
- Headers with bulk edit.
- Body types: none, raw (JSON/XML/HTML/JS/text with beautify), form-data with files, x-www-form-urlencoded, binary file, and GraphQL with schema introspection.
- Paste a cURL command into the URL bar to import it.

**Auth** (inherited request → folder → collection)
- Basic, Bearer, API Key (header or query), Digest (challenge/response), JWT Bearer (HS256/384/512) and AWS SigV4.
- OAuth 2.0 grants: client credentials, password, authorization code (+PKCE, via a local callback), implicit, and refresh.

**Variables**
- Globals, environment, collection, iteration data and local scopes, with Postman precedence.
- Secret (masked) values.
- `{{$guid}}`, `{{$timestamp}}`, `{{$randomInt}}`, `{{$randomEmail}}` and ~25 other dynamic variables.
- Live highlighting of defined and undefined variables in the URL bar.

**Scripts**
- Pre-request and test scripts at collection, folder and request level, run in an embedded JS engine (goja).
- `pm.*` API: `environment`, `globals`, `collectionVariables`, `variables`, `iterationData`, `request`/`response`, `test`, `expect` (chai-style incl. `.not`, `.deep`, `.jsonSchema`), `sendRequest`, `cookies`, `visualizer`, `execution.setNextRequest`/`skipRequest`.
- Also available: legacy `tests[]` and `postman.*`, `console`, `CryptoJS`, `atob`/`btoa`, lodash (`_`) and `moment`, plus `require('lodash'|'moment'|'crypto-js'|'chai'|'uuid')`. Libraries load lazily, so scripts that don't use them pay nothing.

**Response viewer**
- Pretty view with JSON highlighting and a JSONPath filter.
- Raw view, and Preview (HTML, images, PDF).
- Visualizer: Handlebars-style templates.
- Headers, cookies, test results and console.
- Timeline with DNS/TCP/TLS/TTFB/download timings, the exact request sent, redirects and TLS certificate info.
- Save the response to a file, or save it as an example.

**Collections**
- Folders and drag-and-drop reordering.
- Duplicate, rename, filter and descriptions.
- Saved examples.
- Tabs with unsaved-change markers.
- History.
- Cookie manager.

**Collection runner**
- Iterations, delay and CSV/JSON data files.
- Request selection, `setNextRequest` flow control and stop-on-failure.
- Streaming results with pass/fail filters, and exportable results.

**gRPC**
- Discovery via server reflection or imported `.proto` files.
- Unary, server-streaming, client-streaming and bidirectional calls.
- Metadata, TLS and generated example messages.
- Response headers, trailers and status.

**Realtime clients**
- WebSocket (headers, subprotocols, text/binary).
- Socket.IO v4 (emit/listen).
- Server-Sent Events.

**Mock servers**
- Local HTTP servers built from saved examples.
- Path wildcards.
- `x-mock-response-name`/`-code` headers to pick an example.
- Dynamic variables in bodies.
- Delays and a request log.

**Monitors**
- Scheduled collection runs.
- Run history and success rate.
- macOS notification on failure.

**Docs**: one-click HTML documentation for a collection, including a cURL example for each request.

**Code generation**: cURL, HTTPie, wget, JS fetch, Node axios, Python requests, Go, Java OkHttp, C# HttpClient, PHP and Ruby.

**Import / export**
- Postman v2.0/v2.1 collections, environments and globals.
- OpenAPI 3 / Swagger 2 (JSON or YAML, including example responses).
- cURL.
- Full workspace backup.

**Network**
- Proxy and custom CA.
- Per-host client certificates (mTLS).
- SSL verification and redirect controls.
- Global or per-request timeouts.

**CLI**: `lightpost run` is a newman-compatible runner (`-e`, `-g`, `-d`, `-n`, `--folder`, `--bail`, `--env-var`, `--reporter json`). It exits non-zero on failure, for CI.

**Shortcuts**:
- ⌘↵ send
- ⌘S save
- ⌘T new tab
- ⌘W close tab
- ⌘1–9 switch tabs
- ⌘K filter sidebar
- ⌘O import
- ⌘, settings

## How Postman's cloud-only features map here

Postman runs some "premium" features on its cloud. Lightpost runs them
locally instead:

| Postman (cloud) | Lightpost (local) |
|---|---|
| Hosted mock servers | Local mock servers on a port you choose |
| Cloud monitors | In-app scheduler, or `lightpost run` under cron/launchd/CI |
| Published documentation | Self-contained HTML file |
| Workspace sync / team sharing | Export/import JSON files (commit them to git) |

## Not yet implemented
- MQTT.
- NTLM, Hawk and Akamai EdgeGrid auth.
- faker / tv4 / cheerio / xml2js in the script sandbox (lodash, moment, CryptoJS and chai-style expect are included).
- Real-time team collaboration. Share collections as exported files or through git instead.
- Running two copies of the app at once is unsupported: they would both write `workspace.json` and compete for mock-server ports.

## Architecture
- Go backend (`net/http`). Every request, WebSocket and SSE stream goes through it, so CORS never applies and headers are unrestricted.
- Vanilla JS UI (no framework, ~150 KB), embedded in the binary and served on a random `127.0.0.1` port protected by a per-launch token.
- `webview_go` hosts it in a WKWebView window.
- UI state (open tabs, layout) is saved to `ui.json` by the server, because each launch uses a new random port and browser storage would reset.

## Tests
`go test ./...` covers:
- Scripts and variable scopes.
- Inherited auth and digest auth.
- The cookie jar.
- The runner, including `setNextRequest` and data files.
- cURL, Postman and OpenAPI import.
- Mock matching.
- AWS SigV4 signing.
- Script libraries (lodash, moment, CryptoJS).
- gRPC reflection plus unary and server-streaming calls against a real server.
