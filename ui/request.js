'use strict';
// HTTP request editor, response viewer, auth/script editors, code generation.

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];

function markDirty(t) {
  renderTabs();
  saveTabs();
}

function crumbs(t) {
  const out = h('div', { class: 'crumbs' });
  if (t.ref) {
    const loc = findItem(t.ref.itemId);
    if (loc) {
      out.append(h('span', null, loc.col.name));
      loc.folders.forEach(f => out.append(h('span', null, '/'), h('span', null, f.name)));
      out.append(h('span', null, '/'));
    }
  }
  const title = h('span', { class: 'title', title: 'Rename' }, t.name);
  title.addEventListener('click', async () => {
    const n = await promptModal('Rename', t.name);
    if (!n) return;
    t.name = n;
    if (t.ref) { const loc = findItem(t.ref.itemId); if (loc) { loc.item.name = n; commit(); } }
    renderTabs(); renderView(); saveTabs();
  });
  out.append(title);
  return out;
}

function renderRequestTab(view, t) {
  const r = t.draft;
  r.headers = r.headers || []; r.body = r.body || { mode: 'none' }; r.settings = r.settings || {}; r.auth = r.auth || { type: 'inherit' };
  t.ui.sub = t.ui.sub || 'params';
  const root = h('div', { class: 'req-view' });

  // URL bar
  const methodSel = h('select', { title: 'Method' }, METHODS.map(m => h('option', { value: m, selected: r.method === m, class: 'm-' + m }, m)));
  if (!METHODS.includes(r.method)) methodSel.append(h('option', { value: r.method, selected: true }, r.method));
  methodSel.append(h('option', { value: '__custom' }, 'Custom…'));
  methodSel.className = 'm-' + r.method;
  methodSel.addEventListener('change', async () => {
    let m = methodSel.value;
    if (m === '__custom') { m = ((await promptModal('Custom method', '', 'e.g. PURGE, PROPFIND')) || '').toUpperCase(); if (!m) { methodSel.value = r.method; return; } methodSel.append(h('option', { value: m }, m)); methodSel.value = m; }
    r.method = m; methodSel.className = 'm-' + m; markDirty(t);
  });

  const urlIn = h('input', { value: r.url, placeholder: 'Enter URL or paste cURL', spellcheck: 'false', autocomplete: 'off' });
  const hl = h('div', { class: 'hl' });
  const wrap = h('div', { class: 'url-wrap' }, hl, urlIn);
  const paint = () => {
    hl.innerHTML = esc(urlIn.value).replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (m, k) => {
      const name = k.replace(/&amp;/g, '&');
      const ok = name.startsWith('$') || varLookup(name, t.ref && t.ref.colId) !== undefined;
      return `<span class="${ok ? 'var-ok' : 'var-bad'}">${m}</span>`;
    }) + ' ';
    hl.scrollLeft = urlIn.scrollLeft;
  };
  t.ui.paint = paint;
  urlIn.addEventListener('scroll', () => (hl.scrollLeft = urlIn.scrollLeft));
  urlIn.addEventListener('keyup', () => (hl.scrollLeft = urlIn.scrollLeft));
  urlIn.addEventListener('input', () => {
    const v = urlIn.value;
    if (/^\s*curl\s/.test(v)) { importCurlInto(t, v); return; }
    r.url = v;
    syncPathVars(r);
    paint(); markDirty(t);
    if (t.ui.sub === 'params') drawSub();
  });
  urlIn.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); sendRequest(t); } });
  urlIn.addEventListener('mouseover', () => {
    const vars = (urlIn.value.match(/\{\{\s*([^{}]+?)\s*\}\}/g) || []).map(m => m.slice(2, -2).trim());
    urlIn.title = vars.length ? vars.map(k => `${k} = ${k.startsWith('$') ? '(dynamic)' : (varLookup(k, t.ref && t.ref.colId) ?? '(undefined)')}`).join('\n') : '';
  });

  const sendBtn = h('button', { class: 'btn primary send-btn', onclick: () => (t.loading ? cancelSend(t) : sendRequest(t)) }, t.loading ? 'Cancel' : 'Send');
  t.ui.sendBtn = sendBtn;
  const head = h('div', { class: 'req-head' },
    h('div', { style: { display: 'flex', alignItems: 'center', gap: '8px' } }, crumbs(t), h('div', { class: 'grow' }),
      h('button', { class: 'btn sm', title: 'Generate code', onclick: () => openCodegen(t) }, '</> Code'),
      h('button', { class: 'btn sm', onclick: () => saveRequestTab(t), title: 'Save (⌘S)' }, 'Save'),
      t.ref ? h('button', { class: 'btn sm', onclick: () => saveRequestTab(t, true) }, 'Save as…') : null),
    h('div', { class: 'url-bar' }, h('div', { class: 'url-group' }, methodSel, wrap), sendBtn));

  // sub tabs
  const subs = [['params', 'Params'], ['auth', 'Authorization'], ['headers', 'Headers'], ['body', 'Body'], ['pre', 'Pre-request Script'], ['tests', 'Tests'], ['settings', 'Settings'], ['examples', 'Examples'], ['docs', 'Docs']];
  const subBar = h('div', { class: 'subtabs' });
  const pane = h('div', { class: 'req-pane' });
  const counts = () => ({
    params: parseQuery(splitURL(r.url).query).length + (r.pathVars || []).length,
    headers: r.headers.filter(x => x.enabled && x.key).length,
    body: r.body.mode !== 'none' ? '●' : '',
    pre: r.preScript ? '●' : '', tests: r.testScript ? '●' : '',
    auth: r.auth.type !== 'inherit' && r.auth.type !== 'none' ? '●' : '',
    examples: (r.examples || []).length
  });
  const drawBar = () => {
    subBar.innerHTML = '';
    const c = counts();
    for (const [k, label] of subs) {
      if (k === 'examples' && !t.ref) continue;
      subBar.append(h('button', { class: t.ui.sub === k ? 'active' : '', onclick: () => { t.ui.sub = k; drawBar(); drawSub(); } }, label, c[k] ? h('span', { class: 'cnt' }, c[k]) : null));
    }
  };
  const changed = () => { markDirty(t); drawBar(); };
  function drawSub() {
    pane.innerHTML = '';
    switch (t.ui.sub) {
      case 'params': {
        const params = parseQuery(splitURL(r.url).query);
        pane.append(kvTable({ title: 'Query Params', rows: params, desc: false, onChange: rows => {
          const { base } = splitURL(r.url);
          const q = buildQuery(rows);
          r.url = base + (q ? '?' + q : '');
          urlIn.value = r.url; paint(); changed();
        } }));
        if ((r.pathVars || []).length) {
          pane.append(h('div', { style: { height: '14px' } }), kvTable({ title: 'Path Variables', fixed: true, rows: r.pathVars, desc: true, onChange: rows => { r.pathVars = rows; changed(); } }));
        }
        break;
      }
      case 'auth':
        pane.append(authEditor(r.auth, a => { r.auth = a; changed(); }, { inheritable: true, colId: t.ref && t.ref.colId, itemId: t.ref && t.ref.itemId }));
        break;
      case 'headers':
        pane.append(kvTable({ title: 'Headers', rows: r.headers.map(x => ({ ...x })), desc: true, placeholderKey: 'Header', onChange: rows => { r.headers = rows; changed(); } }));
        pane.append(h('div', { class: 'hint', style: { marginTop: '8px' } }, 'Auto-generated: User-Agent: Lightpost/1.0 · Accept: */* · Content-Type from body mode · Authorization from the Authorization tab · Cookie from the cookie jar.'));
        break;
      case 'body':
        pane.append(bodyEditor(t, changed));
        break;
      case 'pre':
        pane.append(scriptEditor(r.preScript || '', v => { r.preScript = v; changed(); }, 'pre'));
        break;
      case 'tests':
        pane.append(scriptEditor(r.testScript || '', v => { r.testScript = v; changed(); }, 'test'));
        break;
      case 'settings':
        pane.append(settingsEditor(r.settings, changed));
        break;
      case 'examples':
        pane.append(examplesEditor(t, changed));
        break;
      case 'docs':
        pane.append(h('div', { class: 'section-lbl' }, 'Description'), codeArea(r.description || '', v => { r.description = v; changed(); }, { style: { height: '200px', fontFamily: 'inherit' }, placeholder: 'Describe this request. Shown in generated documentation.' }));
        break;
    }
  }
  drawBar();
  drawSub();

  // split
  const handle = h('div', { class: 'split-handle' });
  const res = h('div', { class: 'res-pane' });
  t.ui.resEl = res;
  pane.style.height = (t.ui.split || store.get('split', 42)) + '%';
  handle.addEventListener('mousedown', e => {
    e.preventDefault();
    const box = root.querySelector('.split').getBoundingClientRect();
    const mv = ev => { const pct = Math.min(85, Math.max(10, ((ev.clientY - box.top) / box.height) * 100)); pane.style.height = pct + '%'; t.ui.split = pct; };
    const up = () => { document.removeEventListener('mousemove', mv); document.removeEventListener('mouseup', up); store.set('split', t.ui.split); };
    document.addEventListener('mousemove', mv); document.addEventListener('mouseup', up);
  });
  root.append(head, subBar, h('div', { class: 'split' }, pane, handle, res));
  view.append(root);
  paint();
  renderResponse(t);
  if (!r.url) setTimeout(() => urlIn.focus(), 0);
}

function refreshUrlHighlights() {
  const t = S.tabs.find(x => x.id === S.active);
  if (t && t.ui && t.ui.paint) t.ui.paint();
}

function syncPathVars(r) {
  const names = pathVarNames(r.url);
  const old = r.pathVars || [];
  r.pathVars = names.map(n => old.find(o => o.key === n) || { key: n, value: '', enabled: true });
  if (!r.pathVars.length) delete r.pathVars;
}

async function importCurlInto(t, cmd) {
  try {
    const res = await api('/api/import', { method: 'POST', body: { content: cmd } });
    if (res.request) {
      t.draft = Object.assign(newRequest(), res.request, { preScript: t.draft.preScript, testScript: t.draft.testScript, examples: t.draft.examples });
      toast('Imported cURL');
      markDirty(t); renderView();
    }
  } catch (e) { toast(e.message, true); }
}

// ---------- body ------------------------------------------------------------------
function bodyEditor(t, changed) {
  const b = t.draft.body;
  const box = h('div', { style: { height: '100%', display: 'flex', flexDirection: 'column' } });
  const modes = [['none', 'none'], ['formdata', 'form-data'], ['urlencoded', 'x-www-form-urlencoded'], ['raw', 'raw'], ['binary', 'binary'], ['graphql', 'GraphQL']];
  const content = h('div', { style: { flex: 1, minHeight: 0 } });
  const langSel = h('select', { class: 'sm' }, [['json', 'JSON'], ['text', 'Text'], ['javascript', 'JavaScript'], ['html', 'HTML'], ['xml', 'XML']].map(([v, l]) => h('option', { value: v, selected: (b.lang || 'json') === v }, l)));
  langSel.addEventListener('change', () => { b.lang = langSel.value; changed(); });
  const beautify = h('button', { class: 'btn sm', onclick: () => {
    if ((b.lang || 'json') === 'json') {
      try { b.raw = JSON.stringify(JSON.parse(b.raw), null, 2); draw(); changed(); } catch (e) { toast('Invalid JSON: ' + e.message, true); }
    } else if (b.lang === 'xml' || b.lang === 'html') { b.raw = formatXML(b.raw); draw(); changed(); }
  } }, 'Beautify');
  const radios = h('div', { class: 'radios' }, modes.map(([v, l]) => h('label', null, h('input', { type: 'radio', name: 'bm-' + t.id, checked: b.mode === v, onchange: () => { b.mode = v; if (v === 'raw' && !b.lang) b.lang = 'json'; if (v === 'graphql' && !b.graphql) b.graphql = { query: '', variables: '' }; draw(); changed(); } }), l)),
    h('span', { class: 'grow' }), b.mode === 'raw' ? [langSel, beautify] : null);
  function draw() {
    radios.querySelectorAll('input').forEach((inp, i) => (inp.checked = modes[i][0] === b.mode));
    radios.lastElementChild && radios.querySelectorAll('select,.btn').forEach(x => (x.style.display = b.mode === 'raw' ? '' : 'none'));
    content.innerHTML = '';
    switch (b.mode) {
      case 'none': content.append(h('div', { class: 'hint', style: { padding: '20px 0' } }, 'This request does not have a body')); break;
      case 'raw': content.append(codeArea(b.raw || '', v => { b.raw = v; changed(); }, { style: { height: '100%' }, placeholder: b.lang === 'json' ? '{\n  "key": "value"\n}' : '' })); break;
      case 'urlencoded': content.append(kvTable({ rows: (b.form || []).map(x => ({ ...x })), desc: true, onChange: rows => { b.form = rows; changed(); } })); break;
      case 'formdata': content.append(kvTable({ rows: (b.form || []).map(x => ({ ...x })), files: true, desc: true, onChange: rows => { b.form = rows; changed(); } })); break;
      case 'binary':
        content.append(h('div', { style: { display: 'flex', gap: '8px', alignItems: 'center' } },
          h('input', { value: b.file || '', placeholder: '/path/to/file', style: { flex: 1, fontFamily: 'var(--mono)' }, oninput: e => { b.file = e.target.value; changed(); } }),
          h('button', { class: 'btn', onclick: async () => { try { const { path } = await api('/api/pickfile', { method: 'POST', body: {} }); b.file = path; draw(); changed(); } catch { /* cancelled */ } } }, 'Select file')));
        break;
      case 'graphql': content.append(graphqlEditor(t, changed)); break;
    }
  }
  draw();
  box.append(radios, content);
  return box;
}

const INTROSPECTION = 'query IntrospectionQuery { __schema { queryType { name } mutationType { name } subscriptionType { name } types { kind name fields { name args { name type { kind name ofType { kind name ofType { kind name } } } } type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } } }';
function gqlTypeName(t) { if (!t) return ''; if (t.kind === 'NON_NULL') return gqlTypeName(t.ofType) + '!'; if (t.kind === 'LIST') return '[' + gqlTypeName(t.ofType) + ']'; return t.name; }
function graphqlEditor(t, changed) {
  const g = t.draft.body.graphql = t.draft.body.graphql || { query: '', variables: '' };
  const q = codeArea(g.query, v => { g.query = v; changed(); }, { placeholder: 'query {\n  viewer { id }\n}' });
  const vars = codeArea(g.variables, v => { g.variables = v; changed(); }, { placeholder: '{\n  "id": 1\n}' });
  const schemaBox = h('div');
  const fetchSchema = async () => {
    schemaBox.innerHTML = '<div class="hint">Fetching schema…</div>';
    const req = clone(t.draft);
    req.body = { mode: 'graphql', graphql: { query: INTROSPECTION, variables: '' } };
    req.preScript = ''; req.testScript = '';
    try {
      const out = await api('/api/send', { method: 'POST', body: { collectionId: t.ref && t.ref.colId, itemId: t.ref && t.ref.itemId, request: req, envId: S.ws.activeEnvId, name: 'introspection' } });
      const res = out.result;
      if (res.error) throw new Error(res.error);
      const schema = JSON.parse(res.response.body).data.__schema;
      const types = Object.fromEntries(schema.types.map(x => [x.name, x]));
      schemaBox.innerHTML = '';
      for (const [label, root] of [['Query', schema.queryType], ['Mutation', schema.mutationType], ['Subscription', schema.subscriptionType]]) {
        if (!root || !types[root.name]) continue;
        const list = h('div', { class: 'schema-list' });
        for (const f of types[root.name].fields || []) {
          const args = (f.args || []).map(a => a.name + ': ' + gqlTypeName(a.type)).join(', ');
          list.append(h('div', { title: 'Insert', onclick: () => {
            const s = q.selectionStart;
            q.setRangeText(`${f.name}${f.args && f.args.length ? '(' + f.args.map(a => a.name + ': $' + a.name).join(', ') + ')' : ''} {\n  \n}`, s, q.selectionEnd, 'end');
            g.query = q.value; changed(); q.focus();
          } }, `${f.name}${args ? '(' + args + ')' : ''}: ${gqlTypeName(f.type)}`));
        }
        schemaBox.append(h('div', { class: 'section-lbl' }, label), list);
      }
    } catch (e) { schemaBox.innerHTML = ''; schemaBox.append(h('div', { class: 'hint', style: { color: 'var(--err)' } }, 'Introspection failed: ' + e.message)); }
  };
  return h('div', { class: 'gql-layout' },
    h('div', { style: { display: 'flex', flexDirection: 'column', minHeight: 0 } }, h('div', { class: 'section-lbl', style: { display: 'flex' } }, h('span', { class: 'grow' }, 'Query'), h('button', { class: 'btn sm', onclick: fetchSchema }, 'Fetch schema')), q),
    h('div', { style: { display: 'flex', flexDirection: 'column', minHeight: 0 } }, h('div', { class: 'section-lbl' }, 'GraphQL Variables'), vars, schemaBox));
}

function settingsEditor(st, changed) {
  const tri = (key, label, help) => {
    const sel = h('select', { onchange: e => { const v = e.target.value; if (v === '') delete st[key]; else st[key] = v === 'on'; changed(); } },
      h('option', { value: '', selected: st[key] == null }, 'Use global setting'), h('option', { value: 'on', selected: st[key] === true }, 'On'), h('option', { value: 'off', selected: st[key] === false }, 'Off'));
    return [h('label', null, label, h('div', { class: 'hint' }, help)), sel];
  };
  return h('div', { class: 'form-grid' },
    tri('followRedirects', 'Automatically follow redirects', 'Follow HTTP 3xx responses as redirects.'),
    tri('verifySSL', 'Enable SSL certificate verification', 'Verify SSL certificates when sending a request.'),
    h('label', null, 'Maximum number of redirects'), h('input', { type: 'number', value: st.maxRedirects || '', placeholder: '10', oninput: e => { st.maxRedirects = parseInt(e.target.value) || 0; changed(); } }),
    h('label', null, 'Request timeout (ms)'), h('input', { type: 'number', value: st.timeoutMs || '', placeholder: 'Use global setting', oninput: e => { st.timeoutMs = parseInt(e.target.value) || 0; changed(); } }));
}

function examplesEditor(t, changed) {
  const r = t.draft;
  const box = h('div');
  const draw = () => {
    box.innerHTML = '';
    box.append(h('div', { class: 'hint', style: { marginBottom: '8px' } }, 'Saved examples document responses and drive local mock servers. Use "Save as example" on a response, or add one manually.'));
    (r.examples || []).forEach((ex, i) => {
      const card = h('div', { class: 'card' },
        h('div', { style: { display: 'flex', gap: '8px', marginBottom: '8px' } },
          h('input', { value: ex.name, placeholder: 'Example name', style: { flex: 1 }, oninput: e => { ex.name = e.target.value; changed(); } }),
          h('input', { value: ex.code, type: 'number', style: { width: '90px' }, oninput: e => { ex.code = parseInt(e.target.value) || 200; changed(); } }),
          h('button', { class: 'btn sm danger', onclick: () => { r.examples.splice(i, 1); changed(); draw(); } }, 'Delete')),
        kvTable({ title: 'Response headers', rows: (ex.headers || []).map(x => ({ ...x })), onChange: rows => { ex.headers = rows; changed(); } }),
        h('div', { class: 'section-lbl' }, 'Response body'),
        codeArea(ex.body || '', v => { ex.body = v; changed(); }, { style: { height: '140px' } }));
      box.append(card);
    });
    box.append(h('button', { class: 'btn', onclick: () => { (r.examples = r.examples || []).push({ id: uid(), name: 'Example ' + ((r.examples || []).length + 1), method: r.method, url: r.url, code: 200, status: 'OK', headers: [{ key: 'Content-Type', value: 'application/json', enabled: true }], body: '{}' }); changed(); draw(); } }, 'Add example'));
  };
  draw();
  return box;
}

// ---------- auth ------------------------------------------------------------------
const AUTH_TYPES = [['inherit', 'Inherit auth from parent'], ['none', 'No Auth'], ['basic', 'Basic Auth'], ['bearer', 'Bearer Token'], ['apikey', 'API Key'], ['digest', 'Digest Auth'], ['oauth2', 'OAuth 2.0'], ['jwt', 'JWT Bearer'], ['awsv4', 'AWS Signature']];

function parentAuth(opts) {
  if (!opts.colId) return null;
  const col = getCol(opts.colId);
  if (opts.itemId) {
    const loc = findItem(opts.itemId);
    if (loc) for (let i = loc.folders.length - 1; i >= 0; i--) { const a = loc.folders[i].auth; if (a && a.type && a.type !== 'inherit') return { a, from: loc.folders[i].name }; }
  }
  return col && col.auth && col.auth.type !== 'inherit' ? { a: col.auth, from: col.name } : null;
}

function authEditor(auth, onChange, opts = {}) {
  auth = auth || { type: 'inherit' };
  auth.params = auth.params || {};
  const box = h('div', { class: 'auth-layout' });
  const left = h('div');
  const right = h('div');
  const types = AUTH_TYPES.filter(([k]) => k !== 'inherit' || opts.inheritable);
  const sel = h('select', { style: { width: '100%' } }, types.map(([k, l]) => h('option', { value: k, selected: auth.type === k }, l)));
  sel.addEventListener('change', () => { auth.type = sel.value; onChange(auth); draw(); });
  left.append(h('div', { class: 'section-lbl' }, 'Auth Type'), sel, h('p', { class: 'hint' }, 'The authorization header is generated when you send the request. Use {{variables}} to keep secrets in environments.'));
  const p = auth.params;
  const field = (key, label, ph, type) => {
    const inp = h(type === 'area' ? 'textarea' : 'input', { value: p[key] || '', placeholder: ph || '', type: type === 'password' ? 'password' : null, spellcheck: 'false', style: type === 'area' ? { height: '90px' } : null, oninput: e => { p[key] = e.target.value; onChange(auth); } });
    if (type === 'area') inp.value = p[key] || '';
    return [h('label', null, label), inp];
  };
  const select = (key, label, options) => [h('label', null, label), h('select', { onchange: e => { p[key] = e.target.value; onChange(auth); draw(); } }, options.map(([v, l]) => h('option', { value: v, selected: (p[key] || options[0][0]) === v }, l)))];
  const check = (key, label) => [h('label', null, label), h('input', { type: 'checkbox', checked: p[key] === 'true', style: { width: 'auto', justifySelf: 'start' }, onchange: e => { p[key] = e.target.checked ? 'true' : ''; onChange(auth); } })];
  function draw() {
    right.innerHTML = '';
    const g = h('div', { class: 'form-grid' });
    switch (auth.type) {
      case 'inherit': {
        const pa = parentAuth(opts);
        right.append(h('p', { class: 'hint' }, pa ? `This request is using ${(AUTH_TYPES.find(x => x[0] === pa.a.type) || [, pa.a.type])[1]} from "${pa.from}".` : 'No auth is set on a parent folder or collection.'));
        return;
      }
      case 'none': right.append(h('p', { class: 'hint' }, 'This request does not use any authorization.')); return;
      case 'basic': g.append(...field('username', 'Username'), ...field('password', 'Password', '', 'password')); break;
      case 'digest': g.append(...field('username', 'Username'), ...field('password', 'Password', '', 'password'), h('p', { class: 'hint full' }, 'Lightpost sends the request, reads the WWW-Authenticate challenge, and retries with a Digest response (MD5, MD5-sess, SHA-256).')); break;
      case 'bearer': g.append(...field('token', 'Token', '{{token}}')); break;
      case 'apikey': g.append(...field('key', 'Key', 'X-API-Key'), ...field('value', 'Value'), ...select('in', 'Add to', [['header', 'Header'], ['query', 'Query Params']])); break;
      case 'jwt':
        g.append(...select('algorithm', 'Algorithm', [['HS256', 'HS256'], ['HS384', 'HS384'], ['HS512', 'HS512']]), ...field('secret', 'Secret', '', 'password'), ...check('secretBase64', 'Secret base64 encoded'),
          ...field('payload', 'Payload', '{\n  "sub": "123",\n  "iat": 1700000000\n}', 'area'), ...field('header', 'Extra headers (JSON)', '{"kid": "key-1"}', 'area'),
          ...select('addTo', 'Add JWT token to', [['header', 'Request Header'], ['query', 'Query Param']]), ...field('headerPrefix', 'Header prefix', 'Bearer'));
        break;
      case 'awsv4': g.append(...field('accessKey', 'Access Key'), ...field('secretKey', 'Secret Key', '', 'password'), ...field('region', 'AWS Region', 'us-east-1'), ...field('service', 'Service Name', 'execute-api'), ...field('sessionToken', 'Session Token')); break;
      case 'oauth2': {
        g.append(h('div', { class: 'section-lbl full' }, 'Current token'), ...field('accessToken', 'Access Token', 'Paste a token or get a new one below'),
          ...field('headerPrefix', 'Header Prefix', 'Bearer'), ...select('addTo', 'Add token to', [['header', 'Request Headers'], ['query', 'Request URL']]));
        g.append(h('div', { class: 'section-lbl full' }, 'Configure new token'),
          ...select('grantType', 'Grant Type', [['client_credentials', 'Client Credentials'], ['authorization_code', 'Authorization Code'], ['password', 'Password Credentials'], ['implicit', 'Implicit'], ['refresh_token', 'Refresh Token']]));
        const gt = p.grantType || 'client_credentials';
        if (gt === 'authorization_code' || gt === 'implicit') {
          g.append(...field('authUrl', 'Auth URL', 'https://example.com/oauth/authorize'), ...field('redirectUri', 'Callback URL', 'Leave empty to use Lightpost\'s local callback'));
          if (gt === 'authorization_code') g.append(...check('pkce', 'Use PKCE (S256)'));
        }
        if (gt !== 'implicit') g.append(...field('tokenUrl', 'Access Token URL', 'https://example.com/oauth/token'));
        g.append(...field('clientId', 'Client ID'), ...(gt !== 'implicit' ? field('clientSecret', 'Client Secret', '', 'password') : []));
        if (gt === 'password') g.append(...field('username', 'Username'), ...field('password', 'Password', '', 'password'));
        if (gt === 'refresh_token') g.append(...field('refreshToken', 'Refresh Token'));
        g.append(...field('scope', 'Scope', 'read write'), ...field('audience', 'Audience'), ...(gt !== 'implicit' ? select('clientAuth', 'Client Authentication', [['header', 'Send as Basic Auth header'], ['body', 'Send client credentials in body']]) : []));
        const status = h('div', { class: 'hint full' });
        const btn = h('button', { class: 'btn primary', onclick: () => getToken(auth, onChange, status, draw) }, 'Get New Access Token');
        g.append(h('div', { class: 'full', style: { display: 'flex', gap: '8px', alignItems: 'center' } }, btn, p.refreshToken && gt !== 'refresh_token' ? h('button', { class: 'btn', onclick: () => getToken(auth, onChange, status, draw, true) }, 'Refresh token') : null), status);
        break;
      }
    }
    right.append(g);
  }
  draw();
  box.append(left, right);
  return box;
}

async function getToken(auth, onChange, status, redraw, refresh) {
  const p = Object.assign({}, auth.params);
  if (refresh) p.grantType = 'refresh_token';
  status.textContent = 'Requesting token…';
  try {
    const out = await api('/api/oauth2/token', { method: 'POST', body: { params: p, envId: S.ws.activeEnvId } });
    let tok = out.token;
    if (out.error) throw new Error(out.error + (out.response ? ' — ' + JSON.stringify(out.response) : ''));
    if (out.pending) {
      status.textContent = 'Complete sign-in in your browser… (callback: ' + out.redirectUri + ')';
      const start = Date.now();
      while (Date.now() - start < 300000) {
        await new Promise(r => setTimeout(r, 1000));
        const st = await api('/api/oauth2/result?state=' + out.state);
        if (st.done) { if (st.error) throw new Error(st.error); tok = st.token; break; }
      }
      if (!tok) throw new Error('timed out waiting for authorization');
    }
    auth.params.accessToken = tok.access_token || tok.id_token || '';
    if (tok.refresh_token) auth.params.refreshToken = tok.refresh_token;
    onChange(auth);
    redraw();
    toast('Access token received' + (tok.expires_in ? ` (expires in ${tok.expires_in}s)` : ''));
  } catch (e) { status.textContent = ''; toast('Token request failed: ' + e.message, true); }
}

// ---------- scripts -----------------------------------------------------------------
const SNIPPETS = {
  pre: [
    ['Set an environment variable', 'pm.environment.set("variable_key", "variable_value");'],
    ['Set a global variable', 'pm.globals.set("variable_key", "variable_value");'],
    ['Set a collection variable', 'pm.collectionVariables.set("variable_key", "variable_value");'],
    ['Get a variable', 'pm.variables.get("variable_key");'],
    ['Add a header', 'pm.request.headers.add({ key: "X-Request-Id", value: pm.variables.replaceIn("{{$guid}}") });'],
    ['Timestamp variable', 'pm.environment.set("ts", Date.now().toString());'],
    ['HMAC signature (CryptoJS)', 'const sig = CryptoJS.HmacSHA256(pm.request.body.raw || "", pm.environment.get("secret")).toString(CryptoJS.enc.Base64);\npm.request.headers.upsert({ key: "X-Signature", value: sig });'],
    ['Send a request', 'pm.sendRequest("https://postman-echo.com/get", function (err, response) {\n  console.log(response.json());\n});'],
    ['Fetch token before request', 'pm.sendRequest({\n  url: pm.environment.get("authUrl"),\n  method: "POST",\n  header: { "Content-Type": "application/json" },\n  body: { mode: "raw", raw: JSON.stringify({ user: "u", pass: "p" }) }\n}, function (err, res) {\n  pm.environment.set("token", res.json().token);\n});']
  ],
  test: [
    ['Status code is 200', 'pm.test("Status code is 200", function () {\n  pm.response.to.have.status(200);\n});'],
    ['Response time below 200ms', 'pm.test("Response time is less than 200ms", function () {\n  pm.expect(pm.response.responseTime).to.be.below(200);\n});'],
    ['Body contains string', 'pm.test("Body matches string", function () {\n  pm.expect(pm.response.text()).to.include("string_you_want_to_search");\n});'],
    ['JSON value check', 'pm.test("Your test name", function () {\n  const jsonData = pm.response.json();\n  pm.expect(jsonData.value).to.eql(100);\n});'],
    ['Content-Type header present', 'pm.test("Content-Type is present", function () {\n  pm.response.to.have.header("Content-Type");\n});'],
    ['Validate JSON schema', 'const schema = {\n  type: "object",\n  required: ["id"],\n  properties: { id: { type: "number" } }\n};\npm.test("Schema is valid", function () {\n  pm.response.to.have.jsonSchema(schema);\n});'],
    ['Save value to environment', 'const jsonData = pm.response.json();\npm.environment.set("id", jsonData.id);'],
    ['Successful POST request', 'pm.test("Successful POST request", function () {\n  pm.expect(pm.response.code).to.be.oneOf([201, 202]);\n});'],
    ['Visualize response as table', 'const template = `\n<table border="1" cellpadding="6">\n  <tr><th>Key</th><th>Value</th></tr>\n  {{#each rows}}<tr><td>{{key}}</td><td>{{value}}</td></tr>{{/each}}\n</table>`;\nconst data = pm.response.json();\npm.visualizer.set(template, { rows: Object.keys(data).map(k => ({ key: k, value: JSON.stringify(data[k]) })) });'],
    ['Set next request (runner)', 'pm.execution.setNextRequest("Request name");'],
    ['Stop the run', 'pm.execution.setNextRequest(null);']
  ]
};
function scriptEditor(value, onChange, kind) {
  const ta = codeArea(value, onChange, { style: { height: '100%', minHeight: '180px' }, placeholder: kind === 'pre' ? '// Runs before the request is sent\n' : '// Runs after the response is received\n' });
  const snip = h('div', { class: 'snippets' }, h('div', { class: 'section-lbl' }, 'Snippets'),
    SNIPPETS[kind].map(([label, code]) => h('a', { onclick: () => {
      const s = ta.selectionStart;
      const pre = ta.value && !ta.value.endsWith('\n') && s === ta.value.length ? '\n' : '';
      ta.setRangeText(pre + code + '\n', s, ta.selectionEnd, 'end');
      onChange(ta.value); ta.focus();
    } }, label)),
    h('p', { class: 'hint' }, 'Available: pm.*, postman.*, console, CryptoJS, atob/btoa, require("crypto-js" | "chai" | "uuid").'));
  return h('div', { class: 'script-layout' }, ta, snip);
}

// ---------- sending ----------------------------------------------------------------
function applyVarUpdates(colId, vars) {
  if (!vars) return;
  let touched = false;
  if (vars.globals) { S.ws.globals = vars.globals; touched = true; }
  const env = activeEnv();
  if (vars.environment && env) { env.values = vars.environment; touched = true; }
  const col = colId && getCol(colId);
  if (vars.collection && col) { col.variables = vars.collection; touched = true; }
  if (touched) pushWS();
}

async function sendRequest(t) {
  if (t.loading) return;
  if (!t.draft.url.trim()) { toast('Enter a request URL', true); return; }
  t.loading = true;
  t.abort = new AbortController();
  if (t.ui.sendBtn) t.ui.sendBtn.textContent = 'Cancel';
  renderResponse(t);
  try {
    const out = await api('/api/send', { method: 'POST', signal: t.abort.signal, body: { collectionId: t.ref && t.ref.colId, itemId: t.ref && t.ref.itemId, name: t.name, request: t.draft, envId: S.ws.activeEnvId } });
    t.response = out.result;
    applyVarUpdates(t.ref && t.ref.colId, out.vars);
    if (S.panel === 'history') loadHistory();
  } catch (e) {
    t.response = e.name === 'AbortError' ? { error: 'Request cancelled', tests: [], logs: [] } : { error: e.message, tests: [], logs: [] };
  } finally {
    t.loading = false;
    if (t.ui.sendBtn) t.ui.sendBtn.textContent = 'Send';
    if (S.active === t.id) { renderResponse(t); refreshUrlHighlights(); }
  }
}
function cancelSend(t) { if (t.abort) t.abort.abort(); }

async function saveRequestTab(t, saveAs) {
  if (t.ref && !saveAs) {
    const loc = findItem(t.ref.itemId);
    if (loc) {
      loc.item.request = clone(t.draft);
      t.saved = JSON.stringify(t.draft);
      commit();
      toast('Saved');
      return;
    }
  }
  // Save dialog: choose collection / folder.
  const nameIn = h('input', { value: t.name.startsWith('Untitled') ? (t.draft.url.replace(/^https?:\/\//, '').split('?')[0] || t.name) : t.name, style: { width: '100%' } });
  const colSel = h('select', { style: { width: '100%' } });
  const folderSel = h('select', { style: { width: '100%' } });
  const fillCols = () => {
    colSel.innerHTML = '';
    S.ws.collections.forEach(c => colSel.append(h('option', { value: c.id, selected: t.ref && t.ref.colId === c.id }, c.name)));
    colSel.append(h('option', { value: '__new' }, '+ New collection…'));
    fillFolders();
  };
  const fillFolders = () => {
    folderSel.innerHTML = '';
    folderSel.append(h('option', { value: '' }, '(collection root)'));
    const c = getCol(colSel.value);
    const walk = (items, depth) => items.forEach(it => { if (it.type === 'folder') { folderSel.append(h('option', { value: it.id }, '  '.repeat(depth) + '📁 ' + it.name)); walk(it.items || [], depth + 1); } });
    if (c) walk(c.items, 0);
  };
  colSel.addEventListener('change', async () => {
    if (colSel.value === '__new') { const c = await newCollection(); fillCols(); if (c) colSel.value = c.id; fillFolders(); } else fillFolders();
  });
  fillCols();
  const close = openModal({
    title: saveAs ? 'Save as' : 'Save request',
    body: h('div', { class: 'form-grid', style: { gridTemplateColumns: '110px 1fr' } }, h('label', null, 'Name'), nameIn, h('label', null, 'Collection'), colSel, h('label', null, 'Folder'), folderSel),
    foot: [h('button', { class: 'btn', onclick: () => close() }, 'Cancel'), h('button', { class: 'btn primary', onclick: () => {
      const c = getCol(colSel.value);
      if (!c) { toast('Choose or create a collection', true); return; }
      const it = { id: uid(), name: nameIn.value.trim() || 'Request', type: t.kind === 'realtime' ? 'realtime' : 'request', request: clone(t.draft) };
      const dest = folderSel.value ? findItem(folderSel.value).item : c;
      (dest.items = dest.items || []).push(it);
      S.expanded.add(c.id); if (folderSel.value) S.expanded.add(folderSel.value);
      t.ref = { colId: c.id, itemId: it.id };
      t.name = it.name;
      t.saved = JSON.stringify(t.draft);
      close();
      commit();
      renderView();
      toast('Saved to ' + c.name);
    } }, 'Save')]
  });
}

// ---------- response --------------------------------------------------------------
function contentType(resp) { const h_ = (resp.headers || []).find(x => x.key.toLowerCase() === 'content-type'); return h_ ? h_.value : ''; }

function renderResponse(t) {
  const el = t.ui.resEl;
  if (!el) return;
  el.innerHTML = '';
  if (t.loading) {
    el.append(h('div', { class: 'res-empty' }, h('div', null, 'Sending request…'), h('button', { class: 'btn', onclick: () => cancelSend(t) }, 'Cancel')));
    return;
  }
  const res = t.response;
  if (!res) {
    el.append(h('div', { class: 'res-empty' }, h('span', { html: '<svg viewBox="0 0 24 24"><path d="M22 2 11 13M22 2l-7 20-4-9-9-4z"/></svg>' }), 'Enter the URL and click Send to get a response'));
    return;
  }
  const resp = res.response;
  t.ui.resSub = t.ui.resSub || 'body';
  if (!resp) {
    const box = h('div', { class: 'err-box' }, h('b', null, 'Could not send request'), h('div', { class: 'mono', style: { marginTop: '6px', whiteSpace: 'pre-wrap' } }, res.error || 'Unknown error'));
    el.append(box);
    if (res.sent) el.append(h('div', { class: 'section-lbl', style: { padding: '0 14px' } }, 'Request'), h('pre', { class: 'code' }, res.sent.method + ' ' + res.sent.url + '\n' + (res.sent.headers || []).map(x => x.key + ': ' + x.value).join('\n')));
    if ((res.logs || []).length || (res.tests || []).length) { el.append(h('div', { class: 'section-lbl', style: { padding: '0 14px' } }, 'Console')); res.logs.forEach(l => el.append(h('div', { class: 'log-row ' + l.level }, h('span', { class: 'src' }, l.source), l.message))); }
    return;
  }
  const tests = (res.tests || []).filter(x => !x.skipped);
  const passed = tests.filter(x => x.passed).length;
  const subs = [['body', 'Body'], ['cookies', 'Cookies', (resp.cookies || []).length], ['headers', 'Headers', (resp.headers || []).length], ['tests', 'Test Results', tests.length ? `${passed}/${tests.length}` : ''], ['console', 'Console', (res.logs || []).length], ['timeline', 'Timeline']];
  const bar = h('div', { class: 'subtabs' });
  const body = h('div', { class: 'res-body' });
  const tm = resp.timings || {};
  const meta = h('div', { class: 'res-meta' },
    h('span', null, 'Status: ', h('b', { class: 'st-' + String(resp.code)[0] }, `${resp.code} ${resp.status}`)),
    h('span', { title: `DNS ${tm.dns || 0}ms · Connect ${tm.connect || 0}ms · TLS ${tm.tls || 0}ms · TTFB ${tm.ttfb || 0}ms · Download ${(tm.download || 0).toFixed(1)}ms` }, 'Time: ', h('b', { class: 'st-2' }, fmtMs(resp.time))),
    h('span', { title: `Headers ${fmtSize(resp.headerSize)} · Body ${fmtSize(resp.size)}` }, 'Size: ', h('b', { class: 'st-2' }, fmtSize(resp.size + resp.headerSize))),
    h('button', { class: 'icon-btn', title: 'More', onclick: e => showMenu(e.clientX, e.clientY, [
      t.ref ? { label: 'Save as example', action: () => saveAsExample(t) } : null,
      { label: 'Save response to file', action: () => saveResponseFile(resp) },
      { label: 'Copy body', action: () => copyText(resp.bodyBase64 ? resp.body : resp.body) },
      { label: 'Clear response', action: () => { t.response = null; renderResponse(t); } }
    ]) }, '⋯'));
  const draw = () => {
    bar.innerHTML = '';
    for (const [k, label, cnt] of subs) {
      const bad = k === 'tests' && tests.length && passed < tests.length;
      bar.append(h('button', { class: t.ui.resSub === k ? 'active' : '', onclick: () => { t.ui.resSub = k; draw(); } }, label, cnt ? h('span', { class: 'cnt' + (bad ? ' bad' : '') }, `(${cnt})`) : null));
    }
    body.innerHTML = '';
    body.style.display = '';
    switch (t.ui.resSub) {
      case 'body': responseBody(t, res, body); break;
      case 'headers':
        body.append(h('table', { class: 'tbl' }, (resp.headers || []).slice().sort((a, b) => a.key.localeCompare(b.key)).map(x => h('tr', null, h('td', null, x.key), h('td', { class: 'mono' }, x.value)))));
        break;
      case 'cookies':
        if (!(resp.cookies || []).length) body.append(h('div', { class: 'res-empty' }, 'No cookies were returned by the server'));
        else body.append(h('table', { class: 'tbl' }, h('tr', null, ['Name', 'Value', 'Domain', 'Path', 'Expires', 'HttpOnly', 'Secure'].map(x => h('th', null, x))),
          resp.cookies.map(c => h('tr', null, h('td', null, c.name), h('td', null, c.value), h('td', null, c.domain), h('td', null, c.path), h('td', null, c.expires || 'Session'), h('td', null, String(c.httpOnly)), h('td', null, String(c.secure))))));
        break;
      case 'tests': {
        const all = res.tests || [];
        if (!all.length) { body.append(h('div', { class: 'res-empty' }, 'No tests for this request. Add some in the Tests tab.')); break; }
        all.forEach(x => body.append(h('div', { class: 'test-row' }, h('span', { class: 'badge ' + (x.skipped ? 'skip' : x.passed ? 'pass' : 'fail') }, x.skipped ? 'SKIP' : x.passed ? 'PASS' : 'FAIL'),
          h('div', null, x.name, x.error ? h('div', { class: 'mono', style: { color: 'var(--err)', fontSize: '12px' } }, x.error) : null))));
        break;
      }
      case 'console':
        if (!(res.logs || []).length) body.append(h('div', { class: 'res-empty' }, 'console.log() output from scripts appears here'));
        (res.logs || []).forEach(l => body.append(h('div', { class: 'log-row ' + l.level }, h('span', { class: 'src' }, l.source), l.message)));
        break;
      case 'timeline': body.append(timeline(res)); break;
    }
  };
  draw();
  el.append(h('div', { class: 'res-bar' }, bar, h('div', { class: 'grow' }), meta), body);
}

function responseBody(t, res, body) {
  const resp = res.response;
  const ct = contentType(resp);
  const isImg = /^image\//.test(ct);
  const isPdf = /pdf/.test(ct);
  const isHtml = /html/.test(ct);
  const modes = [['pretty', 'Pretty'], ['raw', 'Raw'], ['preview', 'Preview']];
  if (res.visualizer) modes.push(['visualize', 'Visualize']);
  t.ui.bodyMode = t.ui.bodyMode && modes.some(m => m[0] === t.ui.bodyMode) ? t.ui.bodyMode : (res.visualizer ? 'visualize' : (isImg || isPdf) ? 'preview' : 'pretty');
  body.style.display = 'flex';
  body.style.flexDirection = 'column';
  const seg = h('div', { class: 'seg' });
  const filterIn = h('input', { placeholder: 'Filter JSON: $.data[0].name', style: { width: '240px', fontSize: '12px', padding: '3px 8px', fontFamily: 'var(--mono)' }, value: t.ui.jsonFilter || '' });
  const wrapCk = h('label', { class: 'hint', style: { display: 'flex', gap: '4px', alignItems: 'center' } }, h('input', { type: 'checkbox', checked: t.ui.wrap !== false, onchange: e => { t.ui.wrap = e.target.checked; draw(); } }), 'Wrap');
  const content = h('div', { style: { flex: 1, overflow: 'auto', minHeight: 0 } });
  const tools = h('div', { class: 'res-tools' }, seg, h('div', { class: 'grow' }), filterIn, wrapCk, h('button', { class: 'btn sm', onclick: () => copyText(resp.body) }, 'Copy'));
  filterIn.addEventListener('input', debounce(() => { t.ui.jsonFilter = filterIn.value; draw(); }, 200));
  function draw() {
    seg.innerHTML = '';
    modes.forEach(([k, l]) => seg.append(h('button', { class: t.ui.bodyMode === k ? 'active' : '', onclick: () => { t.ui.bodyMode = k; draw(); } }, l)));
    content.innerHTML = '';
    filterIn.style.display = t.ui.bodyMode === 'pretty' ? '' : 'none';
    const wrapCls = 'code' + (t.ui.wrap === false ? ' nowrap' : '');
    if (resp.bodyBase64 && t.ui.bodyMode !== 'preview') {
      content.append(h('div', { class: 'hint', style: { padding: '10px 14px' } }, `Binary response (${ct || 'unknown type'}, ${fmtSize(resp.size)}). Use Preview, or save to file.`), h('button', { class: 'btn', style: { margin: '0 14px' }, onclick: () => saveResponseFile(resp) }, 'Save to file'));
      return;
    }
    switch (t.ui.bodyMode) {
      case 'pretty': {
        let { text, lang } = prettyBody(resp.body, ct);
        if (lang === 'json' && t.ui.jsonFilter) {
          try { text = JSON.stringify(jsonPath(JSON.parse(resp.body), t.ui.jsonFilter), null, 2) ?? 'undefined'; } catch (e) { text = 'Filter error: ' + e.message; }
        }
        const pre = h('pre', { class: wrapCls });
        if (lang === 'json' && text.length < 3e6) pre.innerHTML = highlightJSON(text); else pre.textContent = text;
        content.append(pre);
        break;
      }
      case 'raw': content.append(h('pre', { class: wrapCls }, resp.body)); break;
      case 'preview': {
        if (isImg && resp.bodyBase64) content.append(h('div', { style: { padding: '14px' } }, h('img', { src: `data:${ct};base64,${resp.body}`, style: { maxWidth: '100%' } })));
        else if (isPdf && resp.bodyBase64) content.append(h('iframe', { class: 'preview', src: `data:application/pdf;base64,${resp.body}` }));
        else if (resp.bodyBase64) content.append(h('div', { class: 'res-empty' }, 'No preview available for this content type'));
        else {
          const f = h('iframe', { class: 'preview', sandbox: '' });
          f.srcdoc = isHtml ? resp.body : `<pre style="font:12px monospace;white-space:pre-wrap">${esc(resp.body)}</pre>`;
          content.append(f);
        }
        break;
      }
      case 'visualize': {
        const v = res.visualizer;
        let html;
        try { html = renderTemplate(v.template, v.data); } catch (e) { html = '<pre>Visualizer error: ' + esc(e.message) + '</pre>'; }
        const f = h('iframe', { class: 'preview', sandbox: 'allow-scripts' });
        f.srcdoc = `<!doctype html><meta charset="utf-8"><style>body{font:13px -apple-system,sans-serif;margin:12px}table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:4px 8px}</style><script>var pm={getData:function(cb){cb(null,${JSON.stringify(v.data).replace(/</g, '\\u003c')})}};<\/script>${html}`;
        content.append(f);
        break;
      }
    }
  }
  draw();
  body.append(tools, content);
}

// Small JSONPath subset: $.a.b[0].c, $..name not supported, [*] maps.
function jsonPath(obj, path) {
  path = path.trim().replace(/^\$\.?/, '');
  if (!path) return obj;
  const tokens = path.match(/[^.[\]]+|\[(\d+|\*)\]/g) || [];
  let cur = [obj], multi = false;
  for (const tk of tokens) {
    const key = tk.startsWith('[') ? tk.slice(1, -1) : tk;
    if (key === '*') { multi = true; cur = cur.flatMap(x => (x && typeof x === 'object' ? Object.values(x) : [])); continue; }
    cur = cur.map(x => (x == null ? undefined : x[key]));
  }
  return multi ? cur : cur[0];
}

function timeline(res) {
  const r = res.response, tm = r.timings || {};
  const total = tm.total || r.time || 1;
  const phases = [['DNS Lookup', 0, tm.dns], ['TCP Handshake', tm.dns, tm.connect], ['SSL Handshake', (tm.dns || 0) + (tm.connect || 0), tm.tls], ['Waiting (TTFB)', (tm.dns || 0) + (tm.connect || 0) + (tm.tls || 0), Math.max(0, (tm.ttfb || 0) - (tm.dns || 0) - (tm.connect || 0) - (tm.tls || 0))], ['Download', tm.ttfb, tm.download]];
  const box = h('div', { class: 'timeline' });
  phases.forEach(([label, start, dur]) => {
    dur = dur || 0;
    box.append(h('div', { class: 'tl-row' }, h('span', null, label), h('div', { class: 'tl-bar' }, h('i', { style: { left: ((start || 0) / total * 100) + '%', width: Math.max(0.5, dur / total * 100) + '%' } })), h('span', { class: 'mono', style: { textAlign: 'right' } }, dur.toFixed(1) + ' ms')));
  });
  box.append(h('div', { class: 'tl-row', style: { fontWeight: 600 } }, h('span', null, 'Total'), h('span'), h('span', { class: 'mono', style: { textAlign: 'right' } }, total.toFixed(1) + ' ms')));
  const s = res.sent || {};
  box.append(h('div', { class: 'section-lbl' }, 'Request'),
    h('pre', { class: 'code', style: { background: 'var(--code)', borderRadius: '6px', margin: 0 } }, `${s.method} ${s.url}\n` + (s.headers || []).map(x => `${x.key}: ${x.value}`).join('\n') + (s.body ? '\n\n' + s.body : '')),
    h('div', { class: 'section-lbl' }, 'Response'),
    h('pre', { class: 'code', style: { background: 'var(--code)', borderRadius: '6px', margin: 0 } }, `${r.proto} ${r.code} ${r.status}\n` + (r.headers || []).map(x => `${x.key}: ${x.value}`).join('\n')));
  if (r.redirects && r.redirects.length) box.append(h('div', { class: 'section-lbl' }, 'Redirects'), h('pre', { class: 'code' }, r.redirects.join('\n')));
  if (r.tls) box.append(h('div', { class: 'section-lbl' }, 'TLS'), h('table', { class: 'tbl' }, Object.entries({ Protocol: r.tls.version, Cipher: r.tls.cipher, 'Certificate CN': r.tls.subject, Issuer: r.tls.issuer, 'Valid until': r.tls.notAfter }).map(([k, v]) => h('tr', null, h('td', null, k), h('td', null, v)))));
  return box;
}

async function saveResponseFile(resp) {
  const ct = contentType(resp);
  const ext = /json/.test(ct) ? 'json' : /html/.test(ct) ? 'html' : /xml/.test(ct) ? 'xml' : /png/.test(ct) ? 'png' : /jpe?g/.test(ct) ? 'jpg' : /pdf/.test(ct) ? 'pdf' : 'txt';
  try {
    const { path } = await api('/api/savefile', { method: 'POST', body: { name: 'response.' + ext, content: resp.body, base64: !!resp.bodyBase64 } });
    toast('Saved to ' + path);
  } catch (e) { if (e.message !== 'cancelled') toast(e.message, true); }
}

async function saveAsExample(t) {
  const loc = t.ref && findItem(t.ref.itemId);
  if (!loc) { toast('Save the request to a collection first', true); return; }
  const name = await promptModal('Save as example', (t.response.response.code) + ' ' + t.response.response.status);
  if (!name) return;
  const r = t.response.response;
  const ex = { id: uid(), name, method: t.draft.method, url: t.draft.url, code: r.code, status: r.status, headers: (r.headers || []).filter(x => !/^(content-length|date|connection|transfer-encoding|content-encoding)$/i.test(x.key)), body: r.bodyBase64 ? '' : r.body };
  (loc.item.request.examples = loc.item.request.examples || []).push(ex);
  t.draft.examples = clone(loc.item.request.examples);
  const saved = JSON.parse(t.saved || '{}'); saved.examples = clone(loc.item.request.examples); t.saved = JSON.stringify(saved);
  commit();
  toast('Example saved. Mock servers for this collection will serve it.');
}

// ---------- code generation ----------------------------------------------------------
function openCodegen(t) {
  let lang = store.get('codeLang', 'cURL');
  let resolve = store.get('codeResolve', false);
  const pre = h('pre', { class: 'code' });
  const langs = h('div', { class: 'langs' });
  const build = () => {
    const req = clone(t.draft);
    // Apply inherited auth so the snippet matches what Lightpost sends.
    if (!req.auth || req.auth.type === 'inherit') { const pa = parentAuth({ colId: t.ref && t.ref.colId, itemId: t.ref && t.ref.itemId }); req.auth = pa ? clone(pa.a) : { type: 'none' }; }
    if (req.pathVars) req.pathVars.forEach(pv => { if (pv.value) req.url = req.url.replace(new RegExp('/:' + pv.key + '(?=/|\\?|$)'), '/' + pv.value); });
    if (resolve) {
      const colId = t.ref && t.ref.colId;
      const rv = s => resolveVars(s || '', colId);
      req.url = rv(req.url);
      req.headers.forEach(x => { x.key = rv(x.key); x.value = rv(x.value); });
      if (req.body.raw) req.body.raw = rv(req.body.raw);
      (req.body.form || []).forEach(x => { x.value = rv(x.value); });
      if (req.auth && req.auth.params) for (const k in req.auth.params) req.auth.params[k] = rv(req.auth.params[k]);
    }
    return prepareForCode(req);
  };
  const draw = () => {
    langs.innerHTML = '';
    Object.keys(CODEGEN).forEach(k => langs.append(h('button', { class: k === lang ? 'active' : '', onclick: () => { lang = k; store.set('codeLang', k); draw(); } }, k)));
    pre.textContent = CODEGEN[lang](build());
  };
  draw();
  const close = openModal({
    title: 'Code snippet', wide: true,
    body: h('div', { class: 'code-tabs' }, langs, h('div', null, h('label', { class: 'hint', style: { display: 'flex', gap: '6px', marginBottom: '8px', alignItems: 'center' } }, h('input', { type: 'checkbox', checked: resolve, onchange: e => { resolve = e.target.checked; store.set('codeResolve', resolve); draw(); } }), 'Resolve variables with the active environment'), pre)),
    foot: [h('button', { class: 'btn', onclick: () => copyText(pre.textContent) }, 'Copy snippet'), h('button', { class: 'btn primary', onclick: () => close() }, 'Done')]
  });
}
