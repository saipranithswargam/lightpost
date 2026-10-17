'use strict';
// Collection runner, realtime (WebSocket / Socket.IO / SSE), mock servers and
// monitors.

// ---------- runner ------------------------------------------------------------------
function openRunner(colId, targetId = '') {
  const c = getCol(colId);
  if (!c) return;
  const target = targetId ? findItem(targetId) : null;
  openTab({ kind: 'runner', name: target ? target.item.name : c.name, ref: { colId, folderId: targetId } }, false);
}

function renderRunnerTab(view, t) {
  const col = getCol(t.ref.colId);
  if (!col) { view.append(h('div', { class: 'res-empty' }, 'Collection no longer exists.')); return; }
  const cfg = t.cfg = t.cfg || { iterations: 1, delayMs: 0, data: null, dataName: '', stopOnError: false, keepVars: true, envId: S.ws.activeEnvId, selected: null };
  let items = [];
  if (t.ref.folderId) {
    const loc = findItem(t.ref.folderId);
    if (loc) loc.item.type === 'folder' ? flattenItems(loc.item.items || [], items) : items.push(loc.item);
  } else flattenItems(col.items, items);
  items = items.filter(x => x.type !== 'realtime');
  if (!cfg.selected) cfg.selected = items.map(x => x.id);

  const page = h('div', { class: 'page' });
  const grid = h('div', { class: 'runner-grid' });
  const left = h('div');
  const right = h('div', { style: { minWidth: 0 } });
  const envSel = h('select', { style: { width: '100%' }, onchange: e => (cfg.envId = e.target.value) }, h('option', { value: '' }, 'No environment'), S.ws.environments.map(e => h('option', { value: e.id, selected: e.id === cfg.envId }, e.name)));
  const num = (key, ph) => h('input', { type: 'number', value: cfg[key], placeholder: ph, style: { width: '100%' }, oninput: e => (cfg[key] = parseInt(e.target.value) || 0) });
  const dataLbl = h('span', { class: 'hint' }, cfg.dataName ? `${cfg.dataName} (${cfg.data.length} rows)` : 'None');
  const pickData = async () => {
    try {
      const { path } = await api('/api/pickfile', { method: 'POST', body: {} });
      const { content } = await api('/api/readfile', { method: 'POST', body: { path } });
      cfg.data = await api('/api/parse-data', { method: 'POST', body: { name: path, content } });
      cfg.dataName = path.split('/').pop();
      cfg.iterations = cfg.data.length;
      t.run = null;
      renderView();
    } catch (e) { if (e.message !== 'cancelled') toast(e.message, true); }
  };
  const list = h('div', { class: 'check-list' }, items.map(it => h('label', null,
    h('input', { type: 'checkbox', checked: cfg.selected.includes(it.id), onchange: e => { cfg.selected = e.target.checked ? [...cfg.selected, it.id] : cfg.selected.filter(x => x !== it.id); } }),
    h('span', { class: 'meth m-' + it.request.method }, methodLabel(it.request)), h('span', null, it.name))));
  const runBtn = h('button', { class: 'btn primary', style: { width: '100%', marginTop: '12px' }, onclick: () => (t.run && t.run.running ? t.run.abort.abort() : startRun(t, col, items)) }, t.run && t.run.running ? 'Stop run' : `Run ${t.name}`);
  left.append(h('div', { class: 'card' },
    h('div', { class: 'form-grid', style: { gridTemplateColumns: '110px 1fr' } },
      h('label', null, 'Environment'), envSel,
      h('label', null, 'Iterations'), num('iterations', '1'),
      h('label', null, 'Delay (ms)'), num('delayMs', '0'),
      h('label', null, 'Data file'), h('div', { style: { display: 'flex', gap: '6px', alignItems: 'center' } }, h('button', { class: 'btn sm', onclick: pickData }, 'Select CSV/JSON'), dataLbl, cfg.data ? h('button', { class: 'icon-btn', onclick: () => { cfg.data = null; cfg.dataName = ''; renderView(); } }, '×') : null),
      h('label', null, 'Options'), h('div', null,
        h('label', { style: { display: 'flex', gap: '6px' } }, h('input', { type: 'checkbox', checked: cfg.stopOnError, onchange: e => (cfg.stopOnError = e.target.checked) }), 'Stop run on first failure'),
        h('label', { style: { display: 'flex', gap: '6px' } }, h('input', { type: 'checkbox', checked: cfg.keepVars, onchange: e => (cfg.keepVars = e.target.checked) }), 'Persist variable changes'))),
    h('div', { class: 'section-lbl', style: { display: 'flex' } }, h('span', { class: 'grow' }, `Requests (${items.length})`),
      h('a', { href: '#', onclick: e => { e.preventDefault(); cfg.selected = cfg.selected.length ? [] : items.map(x => x.id); renderView(); } }, 'Toggle all')),
    list, runBtn));
  right.append(runResults(t));
  grid.append(left, right);
  page.append(h('div', { class: 'page-head' }, h('h1', null, 'Runner · ' + t.name)), grid);
  view.append(page);
}

function runResults(t) {
  const box = h('div');
  t.ui.runBox = box;
  drawRunResults(t);
  return box;
}

function drawRunResults(t) {
  const box = t.ui.runBox;
  if (!box) return;
  box.innerHTML = '';
  const run = t.run;
  if (!run) { box.append(h('div', { class: 'res-empty', style: { height: '300px' } }, 'Configure the run and press Run. Results stream in here.')); return; }
  const results = run.results;
  const tests = results.flatMap(r => r.tests.filter(x => !x.skipped));
  const failedTests = tests.filter(x => !x.passed).length;
  const errs = results.filter(r => r.error).length;
  const avg = results.length ? results.reduce((n, r) => n + (r.response ? r.response.time : 0), 0) / results.length : 0;
  box.append(h('div', { class: 'stats' },
    h('div', { class: 'stat' }, h('div', { class: 'v' }, results.length + (run.total ? '/' + run.total : '')), h('div', { class: 'l' }, 'requests')),
    h('div', { class: 'stat' }, h('div', { class: 'v st-2' }, tests.length - failedTests), h('div', { class: 'l' }, 'tests passed')),
    h('div', { class: 'stat' }, h('div', { class: 'v' + (failedTests ? ' st-5' : '') }, failedTests), h('div', { class: 'l' }, 'tests failed')),
    h('div', { class: 'stat' }, h('div', { class: 'v' + (errs ? ' st-5' : '') }, errs), h('div', { class: 'l' }, 'errors')),
    h('div', { class: 'stat' }, h('div', { class: 'v' }, fmtMs(avg)), h('div', { class: 'l' }, 'avg response')),
    run.summary ? h('div', { class: 'stat' }, h('div', { class: 'v' }, fmtMs(run.summary.durationMs)), h('div', { class: 'l' }, 'duration')) : null));
  if (run.total) box.append(h('div', { class: 'progress' }, h('i', { style: { width: Math.min(100, results.length / run.total * 100) + '%' } })));
  const filters = h('div', { class: 'seg', style: { marginBottom: '8px' } }, ['all', 'passed', 'failed'].map(f => h('button', { class: (run.filter || 'all') === f ? 'active' : '', onclick: () => { run.filter = f; drawRunResults(t); } }, f[0].toUpperCase() + f.slice(1))));
  box.append(h('div', { style: { display: 'flex', gap: '8px', alignItems: 'center' } }, filters, h('div', { class: 'grow' }),
    run.summary ? h('button', { class: 'btn sm', onclick: async () => { try { const { path } = await api('/api/savefile', { method: 'POST', body: { name: 'run-results.json', content: JSON.stringify({ collection: t.name, summary: run.summary, results: run.results.map(r => ({ name: r.name, iteration: r.iteration, code: r.response && r.response.code, time: r.response && r.response.time, error: r.error, tests: r.tests })) }, null, 2) } }); toast('Saved to ' + path); } catch (e) { if (e.message !== 'cancelled') toast(e.message, true); } } }, 'Export results') : null));
  let lastIter = -1;
  for (const r of results) {
    const failed = r.error || r.tests.some(x => !x.passed && !x.skipped);
    if (run.filter === 'passed' && failed) continue;
    if (run.filter === 'failed' && !failed) continue;
    if (r.iteration !== lastIter && (run.iterations > 1)) { lastIter = r.iteration; box.append(h('div', { class: 'group-label', style: { paddingLeft: '4px' } }, 'Iteration ' + (r.iteration + 1))); }
    const resp = r.response;
    const item = h('div', { class: 'run-item' },
      h('div', { class: 'hdr' },
        h('span', { class: 'badge ' + (failed ? 'fail' : 'pass') }, failed ? 'FAIL' : 'PASS'),
        h('span', { class: 'meth m-' + (r.sent ? r.sent.method : '') }, r.sent ? r.sent.method : ''),
        h('span', { class: 'nm', title: r.sent && r.sent.url }, r.name, h('span', { class: 'hint mono', style: { marginLeft: '8px' } }, r.sent ? r.sent.url : '')),
        resp ? h('span', { class: 'st-' + String(resp.code)[0], style: { fontSize: '12px' } }, resp.code + ' ' + resp.status) : h('span', { class: 'st-5', style: { fontSize: '12px' } }, 'Error'),
        resp ? h('span', { class: 'hint' }, fmtMs(resp.time) + ' · ' + fmtSize(resp.size)) : null),
      h('div', { class: 'tests' },
        r.error ? h('div', { style: { color: 'var(--err)' } }, r.error) : null,
        r.tests.map(x => h('div', null, h('span', { class: x.skipped ? 'mut' : x.passed ? 'st-2' : 'st-5' }, x.skipped ? '○ ' : x.passed ? '✓ ' : '✗ '), x.name, x.error ? h('span', { class: 'mono', style: { color: 'var(--err)', marginLeft: '8px', fontSize: '12px' } }, x.error) : null)),
        r.logs.map(l => h('div', { class: 'mono mut', style: { fontSize: '12px' } }, '┆ ' + l.message))));
    item.querySelector('.hdr').addEventListener('click', () => {
      if (!resp) return;
      openTab({ kind: 'request', name: r.name + ' (run)', ref: null, draft: Object.assign(newRequest(), { method: r.sent.method, url: r.sent.url, headers: r.sent.headers }), response: r }, true);
    });
    box.append(item);
  }
}

async function startRun(t, col, items) {
  const cfg = t.cfg;
  const ids = items.filter(x => cfg.selected.includes(x.id)).map(x => x.id);
  if (!ids.length) { toast('Select at least one request', true); return; }
  // Unsaved edits in open tabs are not part of the run; save first so the server sees the latest workspace.
  await api('/api/workspace', { method: 'PUT', body: S.ws });
  const abort = new AbortController();
  t.run = { running: true, results: [], abort, iterations: cfg.iterations || (cfg.data ? cfg.data.length : 1), filter: 'all' };
  renderView();
  try {
    const r = await fetch('/api/run', { method: 'POST', signal: abort.signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ collectionId: col.id, folderId: t.ref.folderId || '', itemIds: ids, envId: cfg.envId, iterations: cfg.iterations, delayMs: cfg.delayMs, data: cfg.data || undefined, stopOnError: cfg.stopOnError }) });
    const reader = r.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    const redraw = debounce(() => drawRunResults(t), 60);
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let nl;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl); buf = buf.slice(nl + 1);
        if (!line.trim()) continue;
        const ev = JSON.parse(line);
        if (ev.type === 'start') t.run.total = ev.total;
        else if (ev.type === 'result') { ev.result.iteration = ev.iteration; t.run.results.push(ev.result); redraw(); }
        else if (ev.type === 'done') {
          t.run.summary = ev.summary;
          if (cfg.keepVars && ev.vars) {
            if (ev.vars.globals) S.ws.globals = ev.vars.globals;
            const env = getEnv(cfg.envId); if (env && ev.vars.environment) env.values = ev.vars.environment;
            if (ev.vars.collection) col.variables = ev.vars.collection;
            pushWS();
          }
        } else if (ev.type === 'error') toast(ev.error, true);
      }
    }
  } catch (e) { if (e.name !== 'AbortError') toast(e.message, true); }
  t.run.running = false;
  if (S.active === t.id) renderView();
}

// ---------- realtime ----------------------------------------------------------------
function renderRealtimeTab(view, t) {
  const r = t.draft;
  r.headers = r.headers || []; r.body = r.body || { mode: 'raw', raw: '' };
  t.rt = t.rt || { msgs: [], state: 'idle', sock: null };
  t.ui.sub = t.ui.sub === 'headers' || t.ui.sub === 'message' ? t.ui.sub : 'message';
  const rt = t.rt;
  const root = h('div', { class: 'req-view' });
  const modeSel = h('select', { style: { width: '120px', fontWeight: 600 } }, [['WS', 'WebSocket'], ['SOCKETIO', 'Socket.IO'], ['SSE', 'SSE']].map(([v, l]) => h('option', { value: v, selected: r.method === v }, l)));
  modeSel.addEventListener('change', () => { r.method = modeSel.value; markDirty(t); renderView(); });
  const urlIn = h('input', { value: r.url, placeholder: r.method === 'SSE' ? 'https://example.com/events' : r.method === 'SOCKETIO' ? 'http://localhost:3000' : 'wss://echo.websocket.org', style: { flex: 1, fontFamily: 'var(--mono)' }, spellcheck: 'false', oninput: e => { r.url = e.target.value; markDirty(t); } });
  const connected = rt.state === 'open' || rt.state === 'connecting';
  const btn = h('button', { class: 'btn ' + (connected ? '' : 'primary') + ' send-btn', onclick: () => (connected ? rtDisconnect(t) : rtConnect(t)) }, rt.state === 'connecting' ? 'Cancel' : connected ? 'Disconnect' : 'Connect');
  const head = h('div', { class: 'req-head' },
    h('div', { style: { display: 'flex', alignItems: 'center', gap: '8px' } }, crumbs(t), h('span', { class: 'pill' + (rt.state === 'open' ? ' on' : '') }, h('span', { class: 'd' }), rt.state === 'open' ? 'Connected' : rt.state === 'connecting' ? 'Connecting…' : 'Disconnected'), h('div', { class: 'grow' }),
      h('button', { class: 'btn sm', onclick: () => saveRequestTab(t) }, 'Save')),
    h('div', { class: 'url-bar' }, modeSel, urlIn, btn));
  const subBar = h('div', { class: 'subtabs' });
  const pane = h('div', { style: { flex: 'none' } });
  const drawSub = () => {
    subBar.innerHTML = '';
    [['message', r.method === 'SSE' ? 'Info' : 'Message'], ['headers', 'Headers']].forEach(([k, l]) => subBar.append(h('button', { class: t.ui.sub === k ? 'active' : '', onclick: () => { t.ui.sub = k; drawSub(); } }, l, k === 'headers' && r.headers.filter(x => x.enabled && x.key).length ? h('span', { class: 'cnt' }, r.headers.filter(x => x.enabled && x.key).length) : null)));
    pane.innerHTML = '';
    if (t.ui.sub === 'headers') {
      pane.append(h('div', { style: { padding: '10px 14px' } }, kvTable({ title: 'Handshake headers', rows: r.headers.map(x => ({ ...x })), onChange: rows => { r.headers = rows; markDirty(t); } }),
        r.method === 'WS' ? h('div', { class: 'form-grid', style: { marginTop: '10px' } }, h('label', null, 'Subprotocols'), h('input', { value: r.body.lang || '', placeholder: 'graphql-ws, mqtt (comma separated)', oninput: e => { r.body.lang = e.target.value; markDirty(t); } })) : null));
    } else if (r.method === 'SSE') {
      pane.append(h('div', { class: 'hint', style: { padding: '10px 14px' } }, 'Server-Sent Events: Lightpost opens a GET request with Accept: text/event-stream and lists each event as it arrives.'));
    } else {
      const ta = codeArea(r.body.raw || '', v => { r.body.raw = v; markDirty(t); }, { placeholder: r.method === 'SOCKETIO' ? '{"hello": "world"}  (event arguments; JSON array for multiple)' : 'Message text or JSON' });
      ta.addEventListener('keydown', e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); rtSend(t); } });
      const evIn = r.method === 'SOCKETIO' ? h('input', { value: r.body.event || 'message', placeholder: 'event name', style: { width: '180px' }, oninput: e => { r.body.event = e.target.value; markDirty(t); } }) : null;
      const binCk = r.method === 'WS' ? h('label', { class: 'hint', style: { display: 'flex', gap: '4px', alignItems: 'center' } }, h('input', { type: 'checkbox', checked: !!r.body.binary, onchange: e => { r.body.binary = e.target.checked; markDirty(t); } }), 'Binary (base64)') : null;
      pane.append(h('div', { class: 'rt-compose' }, ta, h('div', { style: { display: 'flex', gap: '8px', alignItems: 'center' } }, evIn, binCk,
        h('button', { class: 'btn sm', onclick: () => { try { r.body.raw = JSON.stringify(JSON.parse(r.body.raw), null, 2); renderView(); } catch { toast('Not valid JSON', true); } } }, 'Beautify'),
        h('div', { class: 'grow' }), h('button', { class: 'btn primary', disabled: rt.state !== 'open' || null, onclick: () => rtSend(t) }, r.method === 'SOCKETIO' ? 'Emit' : 'Send'))));
    }
  };
  drawSub();
  const filterIn = h('input', { placeholder: 'Search messages', style: { width: '200px', fontSize: '12px', padding: '3px 8px' }, value: rt.filter || '', oninput: e => { rt.filter = e.target.value; drawLog(); } });
  const log = h('div', { class: 'rt-log' });
  t.ui.rtLog = log;
  const drawLog = () => {
    log.innerHTML = '';
    const f = (rt.filter || '').toLowerCase();
    const msgs = rt.msgs.filter(m => !f || (m.data || '').toLowerCase().includes(f) || (m.event || '').toLowerCase().includes(f));
    if (!msgs.length) log.append(h('div', { class: 'res-empty' }, rt.state === 'open' ? 'Connected. Messages will appear here.' : 'Connect to start exchanging messages.'));
    for (const m of msgs.slice(-2000)) {
      const sym = { in: '↓', out: '↑', sys: '•', err: '!' }[m.dir];
      let text = m.data || '';
      try { if (/^[\[{]/.test(text.trim()) && text.length < 200000) text = JSON.stringify(JSON.parse(text), null, 2); } catch { /* keep raw */ }
      const row = h('div', { class: 'rt-msg ' + m.dir }, h('span', { class: 'd' }, sym), h('div', { class: 'body' }, m.event && m.event !== 'message' ? h('b', null, m.event + ' ') : null, m.binary ? '[binary base64] ' : null, text), h('span', { class: 't' }, fmtTime(m.time)));
      row.addEventListener('click', () => row.classList.toggle('expanded'));
      row.addEventListener('contextmenu', e => { e.preventDefault(); showMenu(e.clientX, e.clientY, [{ label: 'Copy message', action: () => copyText(m.data) }, m.dir === 'out' ? { label: 'Copy to composer', action: () => { r.body.raw = m.data; renderView(); } } : null]); });
      log.append(row);
    }
    log.scrollTop = log.scrollHeight;
  };
  t.ui.drawLog = drawLog;
  const res = h('div', { class: 'res-pane', style: { borderTop: '1px solid var(--line)' } },
    h('div', { class: 'res-tools' }, h('b', null, 'Messages'), h('span', { class: 'hint' }, rt.msgs.length ? `(${rt.msgs.length})` : ''), h('div', { class: 'grow' }), filterIn, h('button', { class: 'btn sm', onclick: () => { rt.msgs = []; drawLog(); } }, 'Clear')), log);
  root.append(head, subBar, pane, res);
  view.append(root);
  drawLog();
}

function rtPush(t, m) {
  t.rt.msgs.push(Object.assign({ time: Date.now() }, m));
  if (t.rt.msgs.length > 5000) t.rt.msgs.splice(0, 1000);
  if (S.active === t.id && t.ui.drawLog) t.ui.drawLog();
}
function rtConnect(t) {
  const r = t.draft;
  if (!r.url.trim()) { toast('Enter a URL', true); return; }
  const rt = t.rt;
  rt.state = 'connecting';
  renderView();
  const sock = new WebSocket(location.origin.replace(/^http/, 'ws') + '/api/bridge');
  rt.sock = sock;
  sock.onopen = () => sock.send(JSON.stringify({ type: 'connect', mode: { WS: 'ws', SOCKETIO: 'socketio', SSE: 'sse' }[r.method], url: r.url, headers: r.headers, protocols: r.body.lang || '', envId: S.ws.activeEnvId }));
  sock.onmessage = ev => {
    const m = JSON.parse(ev.data);
    switch (m.type) {
      case 'open': rt.state = 'open'; rtPush(t, { dir: 'sys', data: `Connected to ${m.url} (HTTP ${m.status})` }); if (S.active === t.id) renderView(); break;
      case 'message': rtPush(t, { dir: 'in', data: m.data, event: m.event, binary: m.binary }); break;
      case 'sent': rtPush(t, { dir: 'out', data: m.data, event: m.event, binary: m.binary }); break;
      case 'info': rtPush(t, { dir: 'sys', data: m.data }); break;
      case 'error': rtPush(t, { dir: 'err', data: m.data }); break;
      case 'close': rtPush(t, { dir: 'sys', data: `Disconnected${m.code ? ' (' + m.code + ')' : ''}${m.reason ? ': ' + m.reason : ''}` }); break;
    }
  };
  sock.onclose = () => { rt.state = 'idle'; rt.sock = null; if (S.active === t.id) renderView(); };
}
function rtDisconnect(t) {
  const s = t.rt.sock;
  if (!s) return;
  try { s.send(JSON.stringify({ type: 'disconnect' })); } catch { /* already closed */ }
  setTimeout(() => s.close(), 300);
}
function rtSend(t) {
  const r = t.draft, s = t.rt.sock;
  if (!s || t.rt.state !== 'open') return;
  if (r.method === 'SOCKETIO') s.send(JSON.stringify({ type: 'emit', event: r.body.event || 'message', data: r.body.raw || '' }));
  else s.send(JSON.stringify({ type: 'send', data: r.body.raw || '', binary: !!r.body.binary }));
}

// ---------- mocks ---------------------------------------------------------------------
async function refreshMockStatus() {
  try { S.mockStatus = await api('/api/mocks'); } catch { S.mockStatus = []; }
  if (S.panel === 'mocks') renderSidebar();
}
function mockStatus(id) { return (S.mockStatus || []).find(m => m.id === id) || {}; }

function renderMocksPanel(body) {
  body = body || $('#side-body');
  if (!S.ws.mocks.length) {
    body.append(h('div', { class: 'empty' }, 'Mock servers answer requests from your saved examples, locally. No cloud needed.', h('br'), h('button', { class: 'btn', onclick: () => newMock() }, 'Create mock server')));
    return;
  }
  for (const m of S.ws.mocks) {
    const st = mockStatus(m.id);
    const col = getCol(m.collectionId);
    const row = h('div', { class: 'tree-row', style: { flexDirection: 'column', alignItems: 'stretch', padding: '6px 10px' } },
      h('div', { style: { display: 'flex', gap: '6px', alignItems: 'center' } }, h('span', { class: 'pill' + (st.running ? ' on' : '') }, h('span', { class: 'd' })), h('span', { class: 'name', style: { fontWeight: 500 } }, m.name),
        h('button', { class: 'icon-btn more', onclick: e => { e.stopPropagation(); mockMenu(e, m, row); } }, '⋯')),
      h('div', { class: 'hint mono', style: { fontSize: '11px' } }, st.running ? st.url : (m.enabled ? 'failed to start (port in use?)' : 'stopped'), ' · ', col ? col.name : 'missing collection'));
    row.addEventListener('click', () => openTab({ kind: 'mock', name: m.name, ref: { mockId: m.id } }));
    row.addEventListener('contextmenu', e => { e.preventDefault(); mockMenu(e, m, row); });
    body.append(row);
  }
}
function mockMenu(e, m, row) {
  showMenu(e.clientX, e.clientY, [
    { label: m.enabled ? 'Stop' : 'Start', action: () => { m.enabled = !m.enabled; commit(); setTimeout(refreshMockStatus, 400); } },
    { label: 'Copy URL', action: () => copyText('http://127.0.0.1:' + m.port) },
    { label: 'Delete', danger: true, action: async () => { if (await confirmModal('Delete mock server', `Delete "${m.name}"?`)) { S.ws.mocks = S.ws.mocks.filter(x => x !== m); closeTabsWhere(t => t.kind === 'mock' && t.ref.mockId === m.id); commit(); setTimeout(refreshMockStatus, 400); } } }
  ], row);
}
async function newMock(colId) {
  if (!S.ws.collections.length) { toast('Create a collection first', true); return; }
  const nameIn = h('input', { value: 'Mock server', style: { width: '100%' } });
  const colSel = h('select', { style: { width: '100%' } }, S.ws.collections.map(c => h('option', { value: c.id, selected: c.id === colId }, c.name)));
  const used = new Set(S.ws.mocks.map(m => m.port));
  let port = 4010; while (used.has(port)) port++;
  const portIn = h('input', { type: 'number', value: port });
  const delayIn = h('input', { type: 'number', value: 0 });
  const close = openModal({
    title: 'Create mock server',
    body: h('div', null, h('div', { class: 'form-grid', style: { gridTemplateColumns: '130px 1fr' } }, h('label', null, 'Name'), nameIn, h('label', null, 'Collection'), colSel, h('label', null, 'Port'), portIn, h('label', null, 'Response delay (ms)'), delayIn),
      h('p', { class: 'hint' }, 'Requests are matched by method and path against saved examples. Send x-mock-response-name or x-mock-response-code headers to pick a specific example. {{$randomInt}}-style dynamic variables in example bodies are rendered per request.')),
    foot: [h('button', { class: 'btn', onclick: () => close() }, 'Cancel'), h('button', { class: 'btn primary', onclick: () => {
      const m = { id: uid(), name: nameIn.value || 'Mock', collectionId: colSel.value, port: parseInt(portIn.value) || 4010, delayMs: parseInt(delayIn.value) || 0, enabled: true };
      S.ws.mocks.push(m);
      close();
      setPanel('mocks');
      commit();
      setTimeout(refreshMockStatus, 500);
      openTab({ kind: 'mock', name: m.name, ref: { mockId: m.id } });
    } }, 'Create')]
  });
}
function renderMockTab(view, t) {
  const m = S.ws.mocks.find(x => x.id === t.ref.mockId);
  if (!m) { view.append(h('div', { class: 'res-empty' }, 'Mock server deleted.')); return; }
  const st = mockStatus(m.id);
  const col = getCol(m.collectionId);
  let examples = [];
  if (col) { const reqs = []; flattenItems(col.items, reqs); reqs.forEach(it => (it.request.examples || []).forEach(ex => examples.push({ it, ex }))); }
  const page = h('div', { class: 'page' });
  const logs = h('div');
  const url = 'http://127.0.0.1:' + m.port;
  page.append(h('div', { class: 'page-head' }, h('h1', null, m.name), h('span', { class: 'pill' + (st.running ? ' on' : '') }, h('span', { class: 'd' }), st.running ? 'Running' : 'Stopped'), h('div', { class: 'grow' }),
    h('button', { class: 'btn', onclick: () => copyText(url) }, 'Copy URL'),
    h('button', { class: 'btn ' + (m.enabled ? '' : 'primary'), onclick: () => { m.enabled = !m.enabled; commit(); setTimeout(async () => { await refreshMockStatus(); renderView(); }, 400); } }, m.enabled ? 'Stop' : 'Start')),
    h('div', { class: 'card' }, h('div', { class: 'form-grid', style: { gridTemplateColumns: '130px 1fr' } },
      h('label', null, 'URL'), h('code', { class: 'mono' }, url),
      h('label', null, 'Collection'), h('select', { onchange: e => { m.collectionId = e.target.value; commit(); renderView(); } }, S.ws.collections.map(c => h('option', { value: c.id, selected: c.id === m.collectionId }, c.name))),
      h('label', null, 'Port'), h('input', { type: 'number', value: m.port, onchange: e => { m.port = parseInt(e.target.value) || m.port; commit(); setTimeout(async () => { await refreshMockStatus(); renderView(); }, 500); } }),
      h('label', null, 'Delay (ms)'), h('input', { type: 'number', value: m.delayMs || 0, onchange: e => { m.delayMs = parseInt(e.target.value) || 0; commit(); } }))),
    h('div', { class: 'section-lbl' }, `Served examples (${examples.length})`),
    examples.length ? h('table', { class: 'tbl' }, examples.map(({ it, ex }) => h('tr', null, h('td', null, h('span', { class: 'meth m-' + (ex.method || it.request.method) }, ex.method || it.request.method), ' ', examplePathUI(ex.url || it.request.url)), h('td', null, ex.code + ' · ' + ex.name + ' · ' + it.name)))) : h('div', { class: 'hint' }, 'No examples yet. Send a request, then choose "Save as example" from the response menu, or add examples in the request\'s Examples tab.'),
    h('div', { class: 'section-lbl', style: { display: 'flex' } }, h('span', { class: 'grow' }, 'Request log'), h('button', { class: 'btn sm', onclick: () => loadLogs() }, 'Refresh')), logs);
  const loadLogs = async () => {
    const ls = await api('/api/mocks/logs?id=' + m.id);
    logs.innerHTML = '';
    if (!ls.length) logs.append(h('div', { class: 'hint' }, 'No calls yet.'));
    else logs.append(h('table', { class: 'tbl' }, ls.map(l => h('tr', null, h('td', null, fmtTime(l.time) + '  ' + l.method + ' ' + l.path), h('td', null, h('span', { class: 'st-' + String(l.status)[0] }, l.status), ' ', l.matched || '')))));
  };
  loadLogs();
  view.append(page);
}
function examplePathUI(u) { return (u || '').replace(/^\{\{[^}]+\}\}/, '').replace(/^[a-z]+:\/\/[^\/]+/i, '').split('?')[0] || '/'; }

// ---------- monitors ---------------------------------------------------------------------
function renderMonitorsPanel(body) {
  body = body || $('#side-body');
  if (!S.ws.monitors.length) {
    body.append(h('div', { class: 'empty' }, 'Monitors run a collection on a schedule while Lightpost is open, and notify you on failures.', h('br'), h('button', { class: 'btn', onclick: () => newMonitor() }, 'Create monitor')));
    return;
  }
  for (const m of S.ws.monitors) {
    const col = getCol(m.collectionId);
    const row = h('div', { class: 'tree-row', style: { flexDirection: 'column', alignItems: 'stretch', padding: '6px 10px' } },
      h('div', { style: { display: 'flex', gap: '6px', alignItems: 'center' } }, h('span', { class: 'pill' + (m.enabled ? ' on' : '') }, h('span', { class: 'd' })), h('span', { class: 'name', style: { fontWeight: 500 } }, m.name)),
      h('div', { class: 'hint', style: { fontSize: '11px' } }, `every ${humanInterval(m.intervalSec)} · ${col ? col.name : 'missing collection'}`));
    row.addEventListener('click', () => openTab({ kind: 'monitor', name: m.name, ref: { monitorId: m.id } }));
    body.append(row);
  }
}
function humanInterval(s) { return s % 3600 === 0 ? s / 3600 + 'h' : s % 60 === 0 ? s / 60 + 'm' : s + 's'; }
const INTERVALS = [[60, 'Every minute'], [300, 'Every 5 minutes'], [900, 'Every 15 minutes'], [1800, 'Every 30 minutes'], [3600, 'Every hour'], [21600, 'Every 6 hours'], [86400, 'Every day']];
function newMonitor(colId) {
  if (!S.ws.collections.length) { toast('Create a collection first', true); return; }
  const m = { id: uid(), name: 'Monitor', collectionId: colId || S.ws.collections[0].id, environmentId: S.ws.activeEnvId || '', intervalSec: 300, enabled: false, notify: true };
  S.ws.monitors.push(m);
  setPanel('monitors');
  commit();
  openTab({ kind: 'monitor', name: m.name, ref: { monitorId: m.id } });
}
function renderMonitorTab(view, t) {
  const m = S.ws.monitors.find(x => x.id === t.ref.monitorId);
  if (!m) { view.append(h('div', { class: 'res-empty' }, 'Monitor deleted.')); return; }
  const page = h('div', { class: 'page' });
  const runs = h('div');
  const save = () => { commit(); };
  page.append(h('div', { class: 'page-head' },
    h('input', { value: m.name, style: { fontSize: '17px', fontWeight: 600, border: 0, padding: '2px 4px' }, oninput: e => { m.name = e.target.value; t.name = m.name; save(); } }),
    h('span', { class: 'pill' + (m.enabled ? ' on' : '') }, h('span', { class: 'd' }), m.enabled ? 'Active' : 'Paused'), h('div', { class: 'grow' }),
    h('button', { class: 'btn', onclick: async e => { e.target.disabled = true; e.target.textContent = 'Running…'; try { await api('/api/workspace', { method: 'PUT', body: S.ws }); await api('/api/monitors/run', { method: 'POST', body: { id: m.id } }); } catch (err) { toast(err.message, true); } loadRuns(); e.target.disabled = false; e.target.textContent = 'Run now'; } }, 'Run now'),
    h('button', { class: 'btn ' + (m.enabled ? '' : 'primary'), onclick: () => { m.enabled = !m.enabled; save(); renderView(); } }, m.enabled ? 'Pause' : 'Activate'),
    h('button', { class: 'btn danger', onclick: async () => { if (await confirmModal('Delete monitor', `Delete "${m.name}"?`)) { S.ws.monitors = S.ws.monitors.filter(x => x !== m); closeTabsWhere(x => x.id === t.id); commit(); } } }, 'Delete')),
    h('div', { class: 'card' }, h('div', { class: 'form-grid', style: { gridTemplateColumns: '150px 1fr' } },
      h('label', null, 'Collection'), h('select', { onchange: e => { m.collectionId = e.target.value; save(); } }, S.ws.collections.map(c => h('option', { value: c.id, selected: c.id === m.collectionId }, c.name))),
      h('label', null, 'Environment'), h('select', { onchange: e => { m.environmentId = e.target.value; save(); } }, h('option', { value: '' }, 'No environment'), S.ws.environments.map(e => h('option', { value: e.id, selected: e.id === m.environmentId }, e.name))),
      h('label', null, 'Schedule'), h('select', { onchange: e => { m.intervalSec = parseInt(e.target.value); save(); } }, INTERVALS.map(([v, l]) => h('option', { value: v, selected: v === m.intervalSec }, l)), INTERVALS.some(x => x[0] === m.intervalSec) ? null : h('option', { value: m.intervalSec, selected: true }, 'Every ' + humanInterval(m.intervalSec))),
      h('label', null, 'Notify on failure'), h('input', { type: 'checkbox', checked: m.notify, style: { width: 'auto', justifySelf: 'start' }, onchange: e => { m.notify = e.target.checked; save(); } }))),
    h('p', { class: 'hint' }, 'Monitors run inside Lightpost while it is open. For always-on monitoring, schedule the CLI: lightpost run "' + ((getCol(m.collectionId) || {}).name || 'Collection') + '" in cron/launchd/CI.'),
    h('div', { class: 'section-lbl' }, 'Recent runs'), runs);
  const loadRuns = async () => {
    const rs = await api('/api/monitors/runs?id=' + m.id);
    runs.innerHTML = '';
    if (!rs.length) { runs.append(h('div', { class: 'hint' }, 'No runs yet.')); return; }
    const okCount = rs.filter(r => !r.error && !(r.failures || []).length).length;
    runs.append(h('div', { class: 'stats' }, h('div', { class: 'stat' }, h('div', { class: 'v' }, Math.round(okCount / rs.length * 100) + '%'), h('div', { class: 'l' }, 'success rate')),
      h('div', { class: 'stat' }, h('div', { class: 'v' }, fmtMs(rs.reduce((n, r) => n + ((r.summary || {}).avgResponseMs || 0), 0) / rs.length)), h('div', { class: 'l' }, 'avg response')),
      h('div', { class: 'stat' }, h('div', { class: 'v' }, rs.length), h('div', { class: 'l' }, 'runs recorded'))));
    runs.append(h('table', { class: 'tbl' }, rs.map(r => {
      const failed = r.error || (r.failures || []).length;
      const s = r.summary || {};
      return h('tr', null, h('td', null, new Date(r.time).toLocaleString()), h('td', null,
        h('span', { class: 'badge ' + (failed ? 'fail' : 'pass') }, failed ? 'FAIL' : 'PASS'), ` ${s.requests || 0} requests · ${(s.tests || 0) - (s.testsFailed || 0)}/${s.tests || 0} tests · ${fmtMs(s.durationMs || 0)}`,
        r.error ? h('div', { style: { color: 'var(--err)' } }, r.error) : null,
        (r.failures || []).map(f => h('div', { class: 'mono', style: { color: 'var(--err)', fontSize: '12px' } }, f))));
    })));
  };
  loadRuns();
  view.append(page);
}
