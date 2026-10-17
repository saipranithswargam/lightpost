# Postman 12.32.4: REA static analysis findings

Command (read-only, against the installed app):
`rea analyze-javascript-application /Applications/Postman.app/Contents/Resources/app.asar --integrity-policy record-and-continue --max-semantic-nodes 20000`
Raw Evidence: `postman.json` (106 MB, not committed).

## Why Postman is heavy (observed)
- Electron 37.10.3 runtime: 207 MB `Electron Framework.framework` + 147 MB `app.asar` + 105 MB `Resources/data`. That is 463 MB on disk.
- 15 renderer entry points (requester, console, scratchpad, tester_sandbox, auth, loader, migrators, …) plus 4 workers.
- 393 parsed JS files, 11,835 bundled modules, 349 distinct npm packages. These include Sentry, winston, `systeminformation`, `playwright-core`, `@azure/msal-node`, `@1password/sdk`, `node-pty` (terminal), nedb, and React/i18n UI stacks.
- 5 native addons: file watcher, pty, keytar (keychain), and two N-API prebuilds.
- Measured with Postman running: about 808 MB RSS across its processes.

## Feature surface implied by the bundled engines
| Area | Postman package(s) | Lightpost equivalent |
|---|---|---|
| HTTP execution | postman-runtime, postman-request, undici | Go net/http + httptrace |
| Collection format | postman-collection (v2.1, OSS) | import/export mapping |
| Scripts | postman-sandbox, faker, lodash, chai | goja + built-in pm/expect shim |
| Visualizer | handlebars + `__pmVisualizerConsole` bridge | sandboxed iframe + mini template |
| Cookies | @postman/tough-cookie | persistent Go jar |
| Auth helpers | openid-client, jsonwebtoken, sshpk, node-forge | Go stdlib (basic/bearer/apikey/digest/oauth2/awsv4/jwt) |
| WebSocket | postman-ws, @postman/runtime.websocket-client | gorilla/websocket bridge |
| Socket.IO / MQTT | socket.io-client v2/v4, runtime.mqtt-client | not yet (phase 2) |
| gRPC | @postman/grpc-js, grpc-reflection-js, proto-loader | not yet (phase 2) |
| Secrets | keytar | "secret" variable type (masked) |

No Postman code was copied. Only module names and counts from the Evidence were used.
