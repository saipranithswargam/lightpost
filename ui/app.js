'use strict';
// App core: workspace state, sidebar, tabs, environments, import/export,
// settings and cookies. Request editing lives in request.js; runner, realtime,
// mocks and monitors in panels.js.

const S = {
  ws: null,
  tabs: [],
  active: null,
  panel: 'collections',
  expanded: new Set(),
  history: [],
  mockStatus: [],
  filter: ''
};

const newRequest = () => ({ method: 'GET', url: '', headers: [], body: { mode: 'none' }, auth: { type: 'inherit' }, settings: {}, preScript: '', testScript: '' });

// ---------- workspace persistence ----------------------------------------------
const pushWS = debounce(async () => {
  try {
    const r = await api('/api/workspace', { method: 'PUT', body: S.ws });
    if (r.mocks) { S.mockStatus = r.mocks; if (S.panel === 'mocks') renderSidebar(); }
  } catch (e) { toast('Save failed: ' + e.message, true); }
}, 300);
function commit({ sidebar = true } = {}) {
  pushWS();
  if (sidebar) renderSidebar();
  renderTabs();
}
// Only plain UI choices are persisted; t.ui also holds DOM nodes and closures.
const persistableTab = t => ({ ...t, response: undefined, rt: undefined, run: undefined, abort: undefined, loading: undefined, ui: { sub: t.ui && t.ui.sub, resSub: t.ui && t.ui.resSub, split: t.ui && t.ui.split } });
const saveTabs = debounce(() => {
  store.set('tabs', S.tabs.map(persistableTab));
  store.set('active', S.active);
}, 400);

// ---------- tree helpers ------------------------------------------------------------
function findItem(id, items, col, folders = []) {
  for (const c of items ? [] : S.ws.collections) {
    const r = findItem(id, c.items, c, []);
    if (r) return r;
  }
  if (!items) return null;
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (it.id === id) return { col, item: it, parent: items, index: i, folders };
    if (it.type === 'folder' && it.items) {
      const r = findItem(id, it.items, col, [...folders, it]);
      if (r) return r;
    }
  }
  return null;
}
const getCol = id => S.ws.collections.find(c => c.id === id);
const getEnv = id => S.ws.environments.find(e => e.id === id);
const activeEnv = () => getEnv(S.ws.activeEnvId);

function reIdItems(items) {
  for (const it of items || []) {
    it.id = uid();
    if (it.items) reIdItems(it.items);
    if (it.request && it.request.examples) it.request.examples.forEach(e => (e.id = uid()));
  }
  return items;
}

// Variable lookup in the UI mirrors the server precedence (minus script locals).
function varLookup(name, colId) {
  const pick = kvs => { const v = (kvs || []).find(x => x.enabled && x.key === name); return v ? v.value : undefined; };
  const env = activeEnv();
  const col = colId && getCol(colId);
  let v = env && pick(env.values);
  if (v === undefined && col) v = pick(col.variables);
  if (v === undefined) v = pick(S.ws.globals);
  return v;
}
function resolveVars(s, colId) {
  for (let i = 0; i < 5 && /\{\{/.test(s); i++) {
    const n = s.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (m, k) => { const v = varLookup(k, colId); return v === undefined ? m : v; });
    if (n === s) break;
    s = n;
  }
  return s;
}

// ---------- rail / sidebar ------------------------------------------------------------
const PANELS = {
  collections: { title: 'Collections', render: renderCollectionsPanel, actions: () => [iconAction('+', 'New collection', newCollection), iconAction('⇩', 'Import', openImport)] },
  environments: { title: 'Environments', render: renderEnvPanel, actions: () => [iconAction('+', 'New environment', newEnvironment), iconAction('⇩', 'Import', openImport)] },
  history: { title: 'History', render: renderHistoryPanel, actions: () => [iconAction('⌫', 'Clear history', clearHistory)] },
  mocks: { title: 'Mock servers', render: () => renderMocksPanel(), actions: () => [iconAction('+', 'New mock server', () => newMock())] },
  monitors: { title: 'Monitors', render: () => renderMonitorsPanel(), actions: () => [iconAction('+', 'New monitor', () => newMonitor())] }
};
function iconAction(sym, title, fn) { return h('button', { class: 'icon-btn', title, onclick: fn }, sym); }

function setPanel(p) {
  S.panel = p;
  store.set('panel', p);
  $$('#rail [data-panel]').forEach(b => b.classList.toggle('active', b.dataset.panel === p));
  if (p === 'history') loadHistory();
  if (p === 'mocks') refreshMockStatus();
  renderSidebar();
}

function renderSidebar() {
  const p = PANELS[S.panel];
  $('#side-title').textContent = p.title;
  const acts = $('#side-actions');
  acts.innerHTML = '';
  acts.append(...p.actions());
  const body = $('#side-body');
  const scroll = body.scrollTop;
  body.innerHTML = '';
  p.render(body);
  body.scrollTop = scroll;
}

const ICON = {
  folder: '<svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>',
  col: '<svg viewBox="0 0 24 24"><path d="M4 5h16v14H4z"/><path d="M4 9h16"/></svg>',
  env: '<svg viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h10"/></svg>'
};

function methodLabel(r, type) {
  if (type === 'realtime') return { WS: 'WS', SOCKETIO: 'SIO', SSE: 'SSE', GRPC: 'gRPC' }[r.method] || 'WS';
  return ({ DELETE: 'DEL', OPTIONS: 'OPT' })[r.method] || r.method;
}

function matchesFilter(it) {
  if (!S.filter) return true;
  const f = S.filter.toLowerCase();
  if (it.name.toLowerCase().includes(f)) return true;
  if (it.request && (it.request.url || '').toLowerCase().includes(f)) return true;
  return (it.items || []).some(matchesFilter);
}

let dragId = null;
function renderCollectionsPanel(body) {
  const cols = S.ws.collections;
  if (!cols.length) {
    body.append(h('div', { class: 'empty' }, 'No collections yet.', h('br'),
      h('button', { class: 'btn', onclick: newCollection }, 'Create collection'), ' ',
      h('button', { class: 'btn', onclick: openImport }, 'Import')));
    return;
  }
  const activeTab = S.tabs.find(t => t.id === S.active);
  const activeRef = activeTab && activeTab.ref && (activeTab.ref.itemId || activeTab.ref.colId);
  for (const c of cols) {
    if (S.filter && !c.name.toLowerCase().includes(S.filter.toLowerCase()) && !c.items.some(matchesFilter)) continue;
    const open = S.expanded.has(c.id) || !!S.filter;
    const row = h('div', { class: 'tree-row' + (open ? ' open' : '') + (activeTab && activeTab.kind === 'collection' && activeTab.ref.colId === c.id ? ' active' : ''), style: { paddingLeft: '8px' } },
      h('span', { class: 'chev' }, '›'), h('span', { html: ICON.col }), h('span', { class: 'name', style: { fontWeight: 500 } }, c.name),
      h('button', { class: 'icon-btn more', onclick: e => { e.stopPropagation(); colMenu(e, c, row); } }, '⋯'));
    row.addEventListener('click', () => toggleExpand(c.id));
    row.addEventListener('dblclick', () => openCollectionTab(c.id));
    row.addEventListener('contextmenu', e => { e.preventDefault(); colMenu(e, c, row); });
    dropTarget(row, { colId: c.id, into: true });
    body.append(row);
    if (open) renderItems(body, c, c.items, 1, activeRef);
  }
}

function renderItems(body, col, items, depth, activeRef) {
  if (!items.length && depth === 1) body.append(h('div', { class: 'hint', style: { padding: '2px 0 6px ' + (depth * 14 + 28) + 'px' } }, 'This collection is empty. ', h('a', { href: '#', onclick: e => { e.preventDefault(); addRequest(col.id, null); } }, 'Add a request')));
  for (const it of items) {
    if (!matchesFilter(it)) continue;
    const pad = { paddingLeft: depth * 14 + 8 + 'px' };
    if (it.type === 'folder') {
      const open = S.expanded.has(it.id) || !!S.filter;
      const row = h('div', { class: 'tree-row' + (open ? ' open' : '') + (activeRef === it.id ? ' active' : ''), style: pad, draggable: 'true' },
        h('span', { class: 'chev' }, '›'), h('span', { html: ICON.folder }), h('span', { class: 'name' }, it.name),
        h('button', { class: 'icon-btn more', onclick: e => { e.stopPropagation(); folderMenu(e, col, it, row); } }, '⋯'));
      row.addEventListener('click', () => toggleExpand(it.id));
      row.addEventListener('dblclick', () => openFolderTab(col.id, it.id));
      row.addEventListener('contextmenu', e => { e.preventDefault(); folderMenu(e, col, it, row); });
      dragSource(row, it.id);
      dropTarget(row, { colId: col.id, itemId: it.id, into: true });
      body.append(row);
      if (open) renderItems(body, col, it.items || [], depth + 1, activeRef);
    } else {
      const r = it.request || newRequest();
      const ml = methodLabel(r, it.type);
      const row = h('div', { class: 'tree-row' + (activeRef === it.id ? ' active' : ''), style: pad, draggable: 'true' },
        h('span', { class: 'chev' }), h('span', { class: 'meth m-' + (it.type === 'realtime' ? ml : r.method) }, ml), h('span', { class: 'name' }, it.name),
        h('button', { class: 'icon-btn more', onclick: e => { e.stopPropagation(); reqMenu(e, col, it, row); } }, '⋯'));
      row.addEventListener('click', () => openItem(col.id, it.id));
      row.addEventListener('contextmenu', e => { e.preventDefault(); reqMenu(e, col, it, row); });
      dragSource(row, it.id);
      dropTarget(row, { colId: col.id, itemId: it.id, into: false });
      body.append(row);
    }
  }
}

function toggleExpand(id) {
  S.expanded.has(id) ? S.expanded.delete(id) : S.expanded.add(id);
  store.set('expanded', [...S.expanded]);
  renderSidebar();
}

function dragSource(row, id) {
  row.addEventListener('dragstart', e => { dragId = id; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', id); });
  row.addEventListener('dragend', () => { dragId = null; $$('.drop-into,.drop-before').forEach(x => x.classList.remove('drop-into', 'drop-before')); });
}
function dropTarget(row, target) {
  row.addEventListener('dragover', e => {
    if (!dragId || dragId === target.itemId) return;
    e.preventDefault();
    row.classList.add(target.into ? 'drop-into' : 'drop-before');
  });
  row.addEventListener('dragleave', () => row.classList.remove('drop-into', 'drop-before'));
  row.addEventListener('drop', e => {
    e.preventDefault();
    row.classList.remove('drop-into', 'drop-before');
    if (!dragId) return;
    moveItem(dragId, target);
  });
}
function moveItem(id, target) {
  const src = findItem(id);
  if (!src) return;
  // Refuse dropping a folder into its own subtree.
  if (src.item.type === 'folder' && target.itemId && findItem(target.itemId, src.item.items || [], src.col)) return;
  src.parent.splice(src.index, 1);
  const col = getCol(target.colId);
  if (target.into) {
    const dest = target.itemId ? findItem(target.itemId).item : col;
    (dest.items = dest.items || []).push(src.item);
    S.expanded.add(target.itemId || col.id);
  } else {
    const t = findItem(target.itemId);
    t.parent.splice(t.index, 0, src.item);
  }
  S.tabs.forEach(t => { if (t.ref && t.ref.itemId === id) t.ref.colId = target.colId; });
  commit();
}

function colMenu(e, c, row) {
  showMenu(e.clientX, e.clientY, [
    { label: 'Overview & settings', action: () => openCollectionTab(c.id) },
    { label: 'Add request', action: () => addRequest(c.id, null) },
    { label: 'Add WebSocket / Socket.IO / SSE / gRPC request', action: () => addRealtime(c.id, null) },
    { label: 'Add folder', action: () => addFolder(c.id, null) },
    '-',
    { label: 'Run collection', action: () => openRunner(c.id) },
    { label: 'Create mock server', action: () => newMock(c.id) },
    { label: 'Create monitor', action: () => newMonitor(c.id) },
    { label: 'View documentation', action: () => openDocs(c.id) },
    '-',
    { label: 'Rename', action: async () => { const n = await promptModal('Rename collection', c.name); if (n) { c.name = n; commit(); } } },
    { label: 'Duplicate', action: () => { const d = clone(c); d.id = uid(); d.name += ' Copy'; reIdItems(d.items); S.ws.collections.push(d); commit(); } },
    { label: 'Export (Postman v2.1)', action: () => exportFile('collection', c.id, c.name + '.postman_collection.json') },
    { label: 'Delete', danger: true, action: async () => { if (await confirmModal('Delete collection', `Delete "${c.name}" and everything in it?`)) { S.ws.collections = S.ws.collections.filter(x => x !== c); closeTabsWhere(t => t.ref && t.ref.colId === c.id); commit(); } } }
  ], row);
}
function folderMenu(e, col, f, row) {
  showMenu(e.clientX, e.clientY, [
    { label: 'Folder settings', action: () => openFolderTab(col.id, f.id) },
    { label: 'Add request', action: () => addRequest(col.id, f.id) },
    { label: 'Add WebSocket / SSE request', action: () => addRealtime(col.id, f.id) },
    { label: 'Add folder', action: () => addFolder(col.id, f.id) },
    { label: 'Run folder', action: () => openRunner(col.id, f.id) },
    '-',
    { label: 'Rename', action: async () => { const n = await promptModal('Rename folder', f.name); if (n) { f.name = n; commit(); } } },
    { label: 'Duplicate', action: () => { const loc = findItem(f.id); const d = clone(f); d.id = uid(); d.name += ' Copy'; reIdItems(d.items); loc.parent.splice(loc.index + 1, 0, d); commit(); } },
    { label: 'Delete', danger: true, action: async () => { if (await confirmModal('Delete folder', `Delete "${f.name}" and its contents?`)) { const loc = findItem(f.id); loc.parent.splice(loc.index, 1); commit(); } } }
  ], row);
}
function reqMenu(e, col, it, row) {
  showMenu(e.clientX, e.clientY, [
    { label: 'Open in new tab', action: () => openItem(col.id, it.id, true) },
    { label: 'Rename', action: async () => { const n = await promptModal('Rename request', it.name); if (n) { it.name = n; S.tabs.forEach(t => { if (t.ref && t.ref.itemId === it.id) t.name = n; }); commit(); renderView(); } } },
    { label: 'Duplicate', action: () => { const loc = findItem(it.id); const d = clone(it); d.id = uid(); d.name += ' Copy'; loc.parent.splice(loc.index + 1, 0, d); commit(); } },
    it.type !== 'realtime' ? { label: 'Copy as cURL', action: () => copyText(CODEGEN['cURL'](prepareForCode(it.request))) } : null,
    it.type !== 'realtime' ? { label: 'Run this request', action: () => openRunner(col.id, it.id) } : null,
    '-',
    { label: 'Delete', danger: true, action: async () => { if (await confirmModal('Delete request', `Delete "${it.name}"?`)) { const loc = findItem(it.id); loc.parent.splice(loc.index, 1); closeTabsWhere(t => t.ref && t.ref.itemId === it.id); commit(); } } }
  ], row);
}

async function newCollection() {
  const name = await promptModal('New collection', 'New Collection');
  if (!name) return;
  const c = { id: uid(), name, description: '', items: [], auth: { type: 'none' }, variables: [] };
  S.ws.collections.push(c);
  S.expanded.add(c.id);
  setPanel('collections');
  commit();
  return c;
}
async function addRequest(colId, folderId) {
  const name = await promptModal('New request', 'New Request');
  if (!name) return;
  const it = { id: uid(), name, type: 'request', request: newRequest() };
  const dest = folderId ? findItem(folderId).item : getCol(colId);
  (dest.items = dest.items || []).push(it);
  S.expanded.add(folderId || colId);
  commit();
  openItem(colId, it.id);
}
async function addRealtime(colId, folderId) {
  const name = await promptModal('New WebSocket / Socket.IO / SSE / gRPC request', 'Realtime');
  if (!name) return;
  const it = { id: uid(), name, type: 'realtime', request: { method: 'WS', url: '', headers: [], body: { mode: 'raw', raw: '' } } };
  const dest = folderId ? findItem(folderId).item : getCol(colId);
  (dest.items = dest.items || []).push(it);
  S.expanded.add(folderId || colId);
  commit();
  openItem(colId, it.id);
}
async function addFolder(colId, parentId) {
  const name = await promptModal('New folder', 'New Folder');
  if (!name) return;
  const dest = parentId ? findItem(parentId).item : getCol(colId);
  (dest.items = dest.items || []).push({ id: uid(), name, type: 'folder', items: [], auth: { type: 'inherit' } });
  S.expanded.add(parentId || colId);
  commit();
}

// ---------- environments panel -----------------------------------------------------
function renderEnvPanel(body) {
  const row = (label, active, onclick, menu, isActiveEnv) => {
    const r = h('div', { class: 'tree-row' + (active ? ' active' : '') },
      h('span', { html: ICON.env }), h('span', { class: 'name' }, label),
      isActiveEnv ? h('span', { class: 'badge pass' }, 'active') : null,
      menu ? h('button', { class: 'icon-btn more', onclick: e => { e.stopPropagation(); menu(e, r); } }, '⋯') : null);
    r.addEventListener('click', onclick);
    if (menu) r.addEventListener('contextmenu', e => { e.preventDefault(); menu(e, r); });
    return r;
  };
  const at = S.tabs.find(t => t.id === S.active);
  body.append(row('Globals', at && at.kind === 'globals', () => openTab({ kind: 'globals', name: 'Globals', ref: {} }), (e, r) => showMenu(e.clientX, e.clientY, [
    { label: 'Export globals', action: () => exportFile('globals', '', 'globals.postman_globals.json') }], r)));
  body.append(h('div', { class: 'group-label' }, 'Environments'));
  const envs = S.ws.environments.filter(e => !S.filter || e.name.toLowerCase().includes(S.filter.toLowerCase()));
  if (!envs.length) body.append(h('div', { class: 'empty' }, 'No environments.', h('br'), h('button', { class: 'btn', onclick: newEnvironment }, 'Create environment')));
  for (const env of envs) {
    body.append(row(env.name, at && at.kind === 'env' && at.ref.envId === env.id, () => openTab({ kind: 'env', name: env.name, ref: { envId: env.id } }), (e, r) => showMenu(e.clientX, e.clientY, [
      { label: 'Set active', action: () => { S.ws.activeEnvId = env.id; renderEnvSelect(); commit(); } },
      { label: 'Rename', action: async () => { const n = await promptModal('Rename environment', env.name); if (n) { env.name = n; commit(); renderEnvSelect(); } } },
      { label: 'Duplicate', action: () => { const d = clone(env); d.id = uid(); d.name += ' Copy'; S.ws.environments.push(d); commit(); renderEnvSelect(); } },
      { label: 'Export', action: () => exportFile('environment', env.id, env.name + '.postman_environment.json') },
      { label: 'Delete', danger: true, action: async () => { if (await confirmModal('Delete environment', `Delete "${env.name}"?`)) { S.ws.environments = S.ws.environments.filter(x => x !== env); if (S.ws.activeEnvId === env.id) S.ws.activeEnvId = ''; closeTabsWhere(t => t.kind === 'env' && t.ref.envId === env.id); commit(); renderEnvSelect(); } } }
    ], r), S.ws.activeEnvId === env.id));
  }
}
async function newEnvironment() {
  const name = await promptModal('New environment', 'New Environment');
  if (!name) return;
  const env = { id: uid(), name, values: [] };
  S.ws.environments.push(env);
  commit();
  renderEnvSelect();
  setPanel('environments');
  openTab({ kind: 'env', name, ref: { envId: env.id } });
}

function renderEnvEditor(view, tab) {
  const isGlobals = tab.kind === 'globals';
  const env = isGlobals ? null : getEnv(tab.ref.envId);
  if (!isGlobals && !env) { view.append(h('div', { class: 'res-empty' }, 'Environment was deleted.')); return; }
  const values = isGlobals ? S.ws.globals : env.values;
  const page = h('div', { class: 'page' });
  const title = h('h1', null, isGlobals ? 'Globals' : env.name);
  const actions = h('div', { class: 'page-head' }, title, h('div', { class: 'grow' }),
    isGlobals ? null : h('button', { class: 'btn', onclick: () => { S.ws.activeEnvId = S.ws.activeEnvId === env.id ? '' : env.id; renderEnvSelect(); commit(); renderView(); } }, S.ws.activeEnvId === env.id ? 'Deactivate' : 'Set active'),
    h('button', { class: 'btn', onclick: () => exportFile(isGlobals ? 'globals' : 'environment', isGlobals ? '' : env.id, (isGlobals ? 'globals.postman_globals' : env.name + '.postman_environment') + '.json') }, 'Export'));
  page.append(actions, h('p', { class: 'hint' }, isGlobals ? 'Global variables are available in every collection and environment.' : 'Use {{variable}} anywhere in a request. Scripts can update values with pm.environment.set().'));
  const rows = values.map(v => ({ ...v }));
  page.append(kvTableWithType(rows, nr => {
    if (isGlobals) S.ws.globals = nr; else env.values = nr;
    pushWS();
  }));
  view.append(page);
}

// KV table variant with a "secret" toggle column for variables.
function kvTableWithType(rows, onChange) {
  return kvTable({ rows, onChange, secrets: true, title: 'Variables', placeholderKey: 'Add variable' });
}

function renderEnvSelect() {
  const sel = $('#env-select');
  sel.innerHTML = '';
  sel.append(h('option', { value: '' }, 'No environment'));
  for (const e of S.ws.environments) sel.append(h('option', { value: e.id, selected: e.id === S.ws.activeEnvId }, e.name));
}
$('#env-select').addEventListener('change', e => { S.ws.activeEnvId = e.target.value; commit({ sidebar: S.panel === 'environments' }); refreshUrlHighlights(); });
$('#env-eye').addEventListener('click', e => {
  const old = $('.popover');
  if (old) { old.remove(); return; }
  const env = activeEnv();
  const pop = h('div', { class: 'popover' });
  const section = (title, kvs, edit) => {
    pop.append(h('h4', null, title, edit ? h('a', { href: '#', onclick: ev => { ev.preventDefault(); pop.remove(); edit(); } }, 'Edit') : null));
    const live = (kvs || []).filter(x => x.key);
    if (!live.length) pop.append(h('div', { class: 'hint', style: { padding: '0 14px 8px' } }, 'No variables'));
    else pop.append(h('table', { class: 'tbl' }, live.map(v => h('tr', { style: v.enabled ? null : { opacity: .5 } }, h('td', null, v.key), h('td', null, v.type === 'secret' ? '••••••' : v.value)))));
  };
  section(env ? env.name : 'No active environment', env && env.values, env && (() => openTab({ kind: 'env', name: env.name, ref: { envId: env.id } })));
  section('Globals', S.ws.globals, () => openTab({ kind: 'globals', name: 'Globals', ref: {} }));
  document.body.append(pop);
  const r = e.currentTarget.getBoundingClientRect();
  pop.style.top = r.bottom + 6 + 'px';
  pop.style.left = Math.max(8, r.right - 420) + 'px';
  setTimeout(() => document.addEventListener('mousedown', function off(ev) { if (!pop.contains(ev.target)) { pop.remove(); document.removeEventListener('mousedown', off); } }), 0);
});

// ---------- history -----------------------------------------------------------------
async function loadHistory() {
  try { S.history = await api('/api/history'); } catch { S.history = []; }
  if (S.panel === 'history') renderSidebar();
}
function renderHistoryPanel(body) {
  const list = S.history.filter(x => !S.filter || (x.request.url || '').toLowerCase().includes(S.filter.toLowerCase()));
  if (!list.length) { body.append(h('div', { class: 'empty' }, 'Requests you send will appear here.')); return; }
  let day = '';
  for (const e of list) {
    const d = new Date(e.time).toDateString();
    if (d !== day) { day = d; body.append(h('div', { class: 'group-label' }, d === new Date().toDateString() ? 'Today' : d)); }
    const row = h('div', { class: 'tree-row', title: e.request.url },
      h('span', { class: 'meth m-' + e.request.method }, methodLabel(e.request)),
      h('span', { class: 'name mono', style: { fontSize: '12px' } }, e.request.url || '(empty)'),
      h('span', { class: 'st-' + String(e.status)[0], style: { fontSize: '11px' } }, e.status || 'ERR'));
    row.addEventListener('click', () => openTab({ kind: 'request', name: e.request.url ? e.request.url.replace(/^https?:\/\//, '').slice(0, 40) : 'Untitled', draft: clone(e.request), ref: null }));
    body.append(row);
  }
}
async function clearHistory() {
  if (!(await confirmModal('Clear history', 'Remove all request history?', 'Clear'))) return;
  await api('/api/history', { method: 'DELETE' });
  S.history = [];
  renderSidebar();
}

// ---------- tabs -------------------------------------------------------------------------
function tabKey(t) { return t.kind + ':' + (t.ref ? (t.ref.itemId || t.ref.envId || t.ref.colId || t.ref.mockId || t.ref.monitorId || '') + (t.ref.folderId || '') : t.id); }

function openTab(spec, forceNew) {
  const key = tabKey(spec);
  const existing = !forceNew && spec.ref && S.tabs.find(t => tabKey(t) === key);
  if (existing) { activate(existing.id); return existing; }
  const t = Object.assign({ id: uid(), ui: {} }, spec);
  if (t.kind === 'request' || t.kind === 'realtime') {
    if (!t.draft) t.draft = newRequest();
    t.saved = t.ref ? JSON.stringify(t.draft) : null;
  }
  // Replace an unmodified scratch tab, like Postman's preview tab.
  const cur = S.tabs.find(x => x.id === S.active);
  const idx = cur ? S.tabs.indexOf(cur) + 1 : S.tabs.length;
  S.tabs.splice(idx, 0, t);
  activate(t.id);
  return t;
}
function openItem(colId, itemId, forceNew) {
  const loc = findItem(itemId);
  if (!loc) return;
  const kind = loc.item.type === 'realtime' ? 'realtime' : 'request';
  openTab({ kind, name: loc.item.name, ref: { colId: loc.col.id, itemId }, draft: clone(loc.item.request || newRequest()) }, forceNew);
}
function openCollectionTab(colId) { const c = getCol(colId); if (c) openTab({ kind: 'collection', name: c.name, ref: { colId } }); }
function openFolderTab(colId, folderId) { const f = findItem(folderId); if (f) openTab({ kind: 'folder', name: f.item.name, ref: { colId, folderId, itemId: folderId } }); }

function activate(id) {
  S.active = id;
  renderTabs();
  renderView();
  if (S.panel === 'collections' || S.panel === 'environments') renderSidebar();
  saveTabs();
}
function isDirty(t) { return (t.kind === 'request' || t.kind === 'realtime') && (t.saved == null ? !!(t.draft.url || t.draft.body.raw) : JSON.stringify(t.draft) !== t.saved); }

async function closeTab(id) {
  const t = S.tabs.find(x => x.id === id);
  if (!t) return;
  if (isDirty(t) && t.ref) {
    const ok = await confirmModal('Unsaved changes', `"${t.name}" has unsaved changes. Close anyway?`, 'Discard');
    if (!ok) return;
  }
  if (t.rt && t.rt.sock) t.rt.sock.close();
  if (t.run && t.run.abort) t.run.abort.abort();
  const i = S.tabs.indexOf(t);
  S.tabs.splice(i, 1);
  if (S.active === id) S.active = (S.tabs[i] || S.tabs[i - 1] || {}).id || null;
  renderTabs();
  renderView();
  saveTabs();
}
function closeTabsWhere(pred) {
  S.tabs = S.tabs.filter(t => !pred(t));
  if (!S.tabs.find(t => t.id === S.active)) S.active = S.tabs.length ? S.tabs[S.tabs.length - 1].id : null;
  renderTabs();
  renderView();
}

function tabLabel(t) {
  if (t.kind === 'request') return [h('span', { class: 'meth m-' + t.draft.method }, methodLabel(t.draft)), h('span', { class: 'tname' }, t.name)];
  if (t.kind === 'realtime') return [h('span', { class: 'meth m-' + methodLabel(t.draft, 'realtime') }, methodLabel(t.draft, 'realtime')), h('span', { class: 'tname' }, t.name)];
  const icon = { collection: ICON.col, folder: ICON.folder, env: ICON.env, globals: ICON.env }[t.kind];
  const prefix = { runner: 'Runner · ', mock: 'Mock · ', monitor: 'Monitor · ' }[t.kind] || '';
  return [icon ? h('span', { html: icon, style: { display: 'inline-flex', color: 'var(--mut)' } }) : null, h('span', { class: 'tname' }, prefix + t.name)];
}
function renderTabs() {
  const bar = $('#tabs');
  bar.innerHTML = '';
  for (const t of S.tabs) {
    const el = h('div', { class: 'tab' + (t.id === S.active ? ' active' : ''), title: t.name },
      tabLabel(t),
      isDirty(t) ? h('span', { class: 'dot', title: 'Unsaved changes' }) : null,
      h('span', { class: 'x', onclick: e => { e.stopPropagation(); closeTab(t.id); } }, '×'));
    el.addEventListener('click', () => activate(t.id));
    el.addEventListener('auxclick', e => { if (e.button === 1) closeTab(t.id); });
    el.addEventListener('contextmenu', e => {
      e.preventDefault();
      showMenu(e.clientX, e.clientY, [
        { label: 'Duplicate tab', action: () => openTab({ kind: t.kind, name: t.name + ' Copy', draft: t.draft && clone(t.draft), ref: null }, true) },
        { label: 'Close other tabs', action: () => closeTabsWhere(x => x.id !== t.id && !isDirty(x)) },
        { label: 'Close all tabs', action: () => closeTabsWhere(x => !isDirty(x)) }
      ]);
    });
    bar.append(el);
  }
  const act = bar.querySelector('.tab.active');
  if (act) act.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

function renderView() {
  const view = $('#view');
  view.innerHTML = '';
  const t = S.tabs.find(x => x.id === S.active);
  if (!t) {
    view.append(h('div', { class: 'res-empty' },
      h('span', { html: '<svg viewBox="0 0 24 24"><path d="M13 2 4 14h6l-1 8 9-12h-6z"/></svg>' }),
      h('div', { style: { fontSize: '15px', color: 'var(--fg)' } }, 'Lightpost'),
      h('div', null, 'Lightweight API client. ⌘T new request · ⌘↵ send · ⌘S save'),
      h('div', { style: { display: 'flex', gap: '8px', marginTop: '6px' } },
        h('button', { class: 'btn primary', onclick: () => openTab({ kind: 'request', name: 'Untitled Request', ref: null }) }, 'New request'),
        h('button', { class: 'btn', onclick: () => openTab({ kind: 'realtime', name: 'Untitled WebSocket', ref: null, draft: { method: 'WS', url: '', headers: [], body: { mode: 'raw', raw: '' } } }) }, 'New WebSocket'),
        h('button', { class: 'btn', onclick: () => openTab({ kind: 'realtime', name: 'Untitled gRPC', ref: null, draft: { method: 'GRPC', url: '', headers: [], body: { mode: 'raw', raw: '{}' }, grpc: { method: '', tls: false } } }) }, 'New gRPC'),
        h('button', { class: 'btn', onclick: openImport }, 'Import'))));
    return;
  }
  switch (t.kind) {
    case 'request': renderRequestTab(view, t); break;
    case 'realtime': renderRealtimeTab(view, t); break;
    case 'collection': case 'folder': renderContainerTab(view, t); break;
    case 'env': case 'globals': renderEnvEditor(view, t); break;
    case 'runner': renderRunnerTab(view, t); break;
    case 'mock': renderMockTab(view, t); break;
    case 'monitor': renderMonitorTab(view, t); break;
  }
}

// ---------- collection / folder settings -------------------------------------------
function renderContainerTab(view, t) {
  const col = getCol(t.ref.colId);
  const target = t.kind === 'collection' ? col : (findItem(t.ref.folderId) || {}).item;
  if (!target) { view.append(h('div', { class: 'res-empty' }, 'This item no longer exists.')); return; }
  t.ui.sub = t.ui.sub || 'overview';
  const page = h('div', { class: 'req-view' });
  const head = h('div', { class: 'req-head' },
    h('div', { class: 'page-head', style: { marginBottom: 0 } },
      h('h1', { style: { fontSize: '17px', margin: 0 } }, target.name),
      h('div', { class: 'grow' }),
      h('button', { class: 'btn', onclick: () => openRunner(col.id, t.kind === 'folder' ? target.id : '') }, 'Run'),
      t.kind === 'collection' ? h('button', { class: 'btn', onclick: () => openDocs(col.id) }, 'Docs') : null,
      t.kind === 'collection' ? h('button', { class: 'btn', onclick: () => exportFile('collection', col.id, col.name + '.postman_collection.json') }, 'Export') : null));
  const subs = t.kind === 'collection' ? ['overview', 'authorization', 'pre-request', 'tests', 'variables'] : ['overview', 'authorization', 'pre-request', 'tests'];
  const tabsEl = h('div', { class: 'subtabs' });
  const body = h('div', { class: 'page', style: { flex: 1 } });
  const draw = () => {
    tabsEl.innerHTML = '';
    subs.forEach(s => tabsEl.append(h('button', { class: t.ui.sub === s ? 'active' : '', onclick: () => { t.ui.sub = s; draw(); } }, s[0].toUpperCase() + s.slice(1))));
    body.innerHTML = '';
    const save = () => pushWS();
    switch (t.ui.sub) {
      case 'overview': {
        const nameIn = h('input', { value: target.name, style: { width: '100%', fontSize: '15px' }, oninput: e => { target.name = e.target.value; t.name = target.name; save(); renderTabs(); renderSidebar(); } });
        const desc = codeArea(target.description || '', v => { target.description = v; save(); }, { style: { height: '220px', fontFamily: 'inherit' }, placeholder: 'Add a description (shown in generated docs)' });
        let reqs = [];
        flattenItems(target.items || [], reqs);
        body.append(h('div', { class: 'form-grid', style: { gridTemplateColumns: '1fr', maxWidth: '820px' } }, nameIn, desc),
          h('div', { class: 'stats', style: { marginTop: '14px' } },
            h('div', { class: 'stat' }, h('div', { class: 'v' }, reqs.length), h('div', { class: 'l' }, 'requests')),
            h('div', { class: 'stat' }, h('div', { class: 'v' }, reqs.filter(r => r.request && r.request.testScript).length), h('div', { class: 'l' }, 'with tests')),
            h('div', { class: 'stat' }, h('div', { class: 'v' }, reqs.reduce((n, r) => n + ((r.request && r.request.examples) || []).length, 0)), h('div', { class: 'l' }, 'saved examples'))));
        break;
      }
      case 'authorization':
        body.append(authEditor(target.auth || { type: t.kind === 'collection' ? 'none' : 'inherit' }, a => { target.auth = a; save(); }, { inheritable: t.kind === 'folder', colId: col.id }));
        break;
      case 'pre-request':
        body.append(scriptEditor(target.preScript || '', v => { target.preScript = v; save(); }, 'pre'));
        body.style.display = 'flex';
        break;
      case 'tests':
        body.append(scriptEditor(target.testScript || '', v => { target.testScript = v; save(); }, 'test'));
        break;
      case 'variables':
        body.append(h('p', { class: 'hint' }, 'Collection variables are shared by every request in this collection. Environment values take precedence.'),
          kvTable({ rows: (col.variables || []).map(v => ({ ...v })), onChange: r => { col.variables = r; save(); }, title: 'Collection variables', desc: true }));
        break;
    }
  };
  draw();
  page.append(head, tabsEl, body);
  view.append(page);
}
function flattenItems(items, out) { for (const it of items) { if (it.type === 'folder') flattenItems(it.items || [], out); else out.push(it); } }

// ---------- import / export -------------------------------------------------------------
function openImport() {
  const ta = h('textarea', { class: 'code-area', style: { height: '160px' }, placeholder: 'Paste a cURL command, Postman collection/environment JSON, or OpenAPI/Swagger (JSON or YAML)…' });
  const urlIn = h('input', { placeholder: 'https://example.com/openapi.json', style: { flex: 1 } });
  const zone = h('div', { class: 'drop-zone' }, 'Drop files here, or ', h('a', { href: '#', onclick: async e => { e.preventDefault(); try { const { path } = await api('/api/pickfile', { method: 'POST', body: {} }); const { content } = await api('/api/readfile', { method: 'POST', body: { path } }); await doImport({ content }, close); } catch (err) { if (err.message !== 'cancelled') toast(err.message, true); } } }, 'choose a file'));
  zone.addEventListener('dragover', e => { e.preventDefault(); zone.classList.add('over'); });
  zone.addEventListener('dragleave', () => zone.classList.remove('over'));
  zone.addEventListener('drop', async e => {
    e.preventDefault(); zone.classList.remove('over');
    for (const f of e.dataTransfer.files) await doImport({ content: await f.text() }, close);
  });
  const close = openModal({
    title: 'Import', wide: true,
    body: [zone, ta, h('div', { style: { display: 'flex', gap: '8px', marginTop: '10px' } }, urlIn, h('button', { class: 'btn', onclick: () => urlIn.value && doImport({ url: urlIn.value }, close) }, 'Import from link'))],
    foot: [h('button', { class: 'btn', onclick: () => close() }, 'Cancel'), h('button', { class: 'btn primary', onclick: () => ta.value.trim() && doImport({ content: ta.value }, close) }, 'Import')]
  });
}
async function doImport(src, close) {
  try {
    const r = await api('/api/import', { method: 'POST', body: src });
    if (r.request) {
      close && close();
      openTab({ kind: 'request', name: r.request.url.replace(/^https?:\/\//, '').slice(0, 40) || 'Imported request', draft: Object.assign(newRequest(), r.request), ref: null });
      toast('Imported cURL command');
      return;
    }
    (r.collections || []).forEach(c => { S.ws.collections.push(c); S.expanded.add(c.id); });
    (r.environments || []).forEach(e => S.ws.environments.push(e));
    if (r.globals) S.ws.globals = mergeKV(S.ws.globals, r.globals);
    commit();
    renderEnvSelect();
    close && close();
    const n = (r.collections || []).length, m = (r.environments || []).length;
    toast(`Imported ${n ? n + ' collection' + (n > 1 ? 's' : '') : ''}${n && m ? ' and ' : ''}${m ? m + ' environment' + (m > 1 ? 's' : '') : ''}${r.globals ? 'globals' : ''}`);
    if (n) setPanel('collections'); else if (m) setPanel('environments');
  } catch (e) { toast(e.message, true); }
}
function mergeKV(a, b) { const out = [...a]; for (const kv of b) { const i = out.findIndex(x => x.key === kv.key); if (i >= 0) out[i] = kv; else out.push(kv); } return out; }

async function exportFile(kind, id, name) {
  try {
    const r = await api('/api/export?kind=' + kind + '&id=' + encodeURIComponent(id), { raw: true });
    const content = await r.text();
    const { path } = await api('/api/savefile', { method: 'POST', body: { name: name.replace(/[\/:]/g, '-'), content } });
    toast('Saved to ' + path);
  } catch (e) { if (e.message !== 'cancelled') toast(e.message, true); }
}
async function openDocs(colId) {
  const r = await api('/api/docs?id=' + colId, { raw: true });
  const html = await r.text();
  const c = getCol(colId);
  const frame = h('iframe', { class: 'preview', style: { height: '70vh', borderRadius: '6px' }, sandbox: '' });
  frame.srcdoc = html;
  const close = openModal({
    title: 'Documentation · ' + c.name, wide: true, body: frame,
    foot: [h('button', { class: 'btn', onclick: async () => { try { const { path } = await api('/api/savefile', { method: 'POST', body: { name: c.name + ' docs.html', content: html } }); toast('Saved to ' + path); } catch (e) { if (e.message !== 'cancelled') toast(e.message, true); } } }, 'Save as HTML'), h('button', { class: 'btn primary', onclick: () => close() }, 'Done')]
  });
}

function copyText(s) {
  const done = () => toast('Copied to clipboard');
  if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(s).then(done, () => fallbackCopy(s, done));
  else fallbackCopy(s, done);
}
function fallbackCopy(s, done) {
  const ta = h('textarea', { style: { position: 'fixed', opacity: 0 } });
  ta.value = s; document.body.append(ta); ta.select();
  try { document.execCommand('copy'); done(); } catch { toast('Copy failed', true); }
  ta.remove();
}

// ---------- settings / cookies ----------------------------------------------------------
function applyTheme() {
  const t = S.ws.settings.theme || 'system';
  if (t === 'system') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', t);
}
async function openSettings() {
  const st = S.ws.settings;
  const f = (label, el) => [h('label', null, label), el];
  const ck = (key) => h('input', { type: 'checkbox', checked: st[key], style: { width: 'auto' }, onchange: e => { st[key] = e.target.checked; pushWS(); } });
  const txt = (key, ph, num) => h('input', { value: st[key] || '', placeholder: ph || '', type: num ? 'number' : 'text', oninput: e => { st[key] = num ? (parseInt(e.target.value) || 0) : e.target.value; pushWS(); } });
  const theme = h('select', { onchange: e => { st.theme = e.target.value; applyTheme(); pushWS(); } }, ['system', 'light', 'dark'].map(v => h('option', { value: v, selected: (st.theme || 'system') === v }, v[0].toUpperCase() + v.slice(1))));
  const certs = h('div', { class: 'full' });
  const drawCerts = () => {
    certs.innerHTML = '';
    certs.append(h('div', { class: 'section-lbl' }, 'Client certificates (mTLS)'));
    (st.clientCerts = st.clientCerts || []).forEach((c, i) => certs.append(h('div', { style: { display: 'flex', gap: '6px', marginBottom: '6px' } },
      h('input', { value: c.host, placeholder: 'host (e.g. api.example.com)', oninput: e => { c.host = e.target.value; pushWS(); } }),
      h('input', { value: c.certPath, placeholder: 'cert.pem path', style: { flex: 1 }, oninput: e => { c.certPath = e.target.value; pushWS(); } }),
      h('input', { value: c.keyPath, placeholder: 'key.pem path', style: { flex: 1 }, oninput: e => { c.keyPath = e.target.value; pushWS(); } }),
      h('button', { class: 'icon-btn', onclick: () => { st.clientCerts.splice(i, 1); pushWS(); drawCerts(); } }, '×'))));
    certs.append(h('button', { class: 'btn sm', onclick: () => { st.clientCerts.push({ host: '', certPath: '', keyPath: '' }); drawCerts(); } }, 'Add certificate'));
  };
  drawCerts();
  const statsEl = h('div', { class: 'hint full' }, 'Loading resource usage…');
  api('/api/stats').then(s => { statsEl.textContent = `Lightpost process: peak memory ${s.maxRssMB.toFixed(1)} MB · CPU time ${s.cpuSeconds.toFixed(2)} s over ${Math.round(s.uptimeSeconds / 60)} min · data in ${s.dataDir}`; }).catch(() => {});
  const close = openModal({
    title: 'Settings', wide: true,
    body: h('div', { class: 'form-grid' },
      f('Theme', theme),
      f('Request timeout (ms, 0 = none)', txt('timeoutMs', '30000', true)),
      f('SSL certificate verification', ck('verifySSL')),
      f('Follow redirects', ck('followRedirects')),
      f('Proxy URL', txt('proxyUrl', 'http://user:pass@proxy:8080 (empty = system env)')),
      f('Custom CA certificate (PEM path)', txt('caCertPath', '/path/to/ca.pem')),
      f('History entries kept', txt('historyLimit', '300', true)),
      certs,
      h('div', { class: 'section-lbl full' }, 'Data'),
      h('div', { class: 'full', style: { display: 'flex', gap: '8px' } },
        h('button', { class: 'btn', onclick: () => exportFile('workspace', '', 'lightpost-workspace.json') }, 'Export entire workspace'),
        h('button', { class: 'btn', onclick: () => { close(); openImport(); } }, 'Import data')),
      statsEl),
    foot: [h('button', { class: 'btn primary', onclick: () => close() }, 'Done')]
  });
}

async function openCookies() {
  let cookies = await api('/api/cookies');
  const body = h('div');
  const save = async () => { await api('/api/cookies', { method: 'PUT', body: cookies }); draw(); };
  const draw = () => {
    body.innerHTML = '';
    const byDomain = {};
    cookies.forEach(c => (byDomain[c.domain] = byDomain[c.domain] || []).push(c));
    const domains = Object.keys(byDomain).sort();
    if (!domains.length) body.append(h('div', { class: 'empty' }, 'No cookies yet. Cookies set by responses are stored here and sent automatically.'));
    for (const d of domains) {
      body.append(h('div', { class: 'section-lbl', style: { display: 'flex', alignItems: 'center' } }, h('span', { class: 'grow' }, d),
        h('button', { class: 'btn sm danger', onclick: () => { cookies = cookies.filter(c => c.domain !== d); save(); } }, 'Clear domain')));
      body.append(h('table', { class: 'tbl' }, h('tr', null, h('th', null, 'Name'), h('th', null, 'Value'), h('th', null, 'Path'), h('th', null, 'Expires'), h('th', null, '')),
        byDomain[d].map(c => h('tr', null, h('td', null, c.name),
          h('td', null, h('input', { value: c.value, style: { width: '100%' }, onchange: e => { c.value = e.target.value; save(); } })),
          h('td', null, c.path), h('td', null, c.expires && !c.expires.startsWith('0001') ? new Date(c.expires).toLocaleString() : 'Session'),
          h('td', null, h('button', { class: 'icon-btn', onclick: () => { cookies = cookies.filter(x => x !== c); save(); } }, '×'))))));
    }
  };
  draw();
  const addRow = async () => {
    const domain = await promptModal('Add cookie', '', 'Domain (e.g. api.example.com)');
    if (!domain) return;
    const pair = await promptModal('Add cookie', 'name=value', 'Cookie as name=value');
    if (!pair || !pair.includes('=')) return;
    const i = pair.indexOf('=');
    cookies.push({ name: pair.slice(0, i).trim(), value: pair.slice(i + 1).trim(), domain, path: '/', hostOnly: false, secure: false, httpOnly: false });
    save();
  };
  const close = openModal({ title: 'Cookies', wide: true, body, foot: [h('button', { class: 'btn', onclick: addRow }, 'Add cookie'), h('button', { class: 'btn danger', onclick: async () => { cookies = []; save(); } }, 'Clear all'), h('button', { class: 'btn primary', onclick: () => close() }, 'Done')] });
}

// ---------- boot --------------------------------------------------------------------------
function bindGlobal() {
  $$('#rail [data-panel]').forEach(b => b.addEventListener('click', () => {
    if (S.panel === b.dataset.panel && innerWidth < 760) $('#sidebar').classList.toggle('collapsed');
    setPanel(b.dataset.panel);
  }));
  $('#btn-settings').addEventListener('click', openSettings);
  $('#btn-cookies').addEventListener('click', openCookies);
  $('#new-tab').addEventListener('click', () => openTab({ kind: 'request', name: 'Untitled Request', ref: null }, true));
  $('#side-filter').addEventListener('input', debounce(e => { S.filter = e.target.value.trim(); renderSidebar(); }, 120));

  // sidebar resize
  const side = $('#sidebar');
  const w = store.get('sideWidth', 290);
  side.style.width = w + 'px';
  $('#side-resize').addEventListener('mousedown', e => {
    e.preventDefault();
    const startX = e.clientX, startW = side.offsetWidth;
    const mv = ev => { side.style.width = Math.min(600, Math.max(200, startW + ev.clientX - startX)) + 'px'; };
    const up = () => { document.removeEventListener('mousemove', mv); document.removeEventListener('mouseup', up); store.set('sideWidth', side.offsetWidth); };
    document.addEventListener('mousemove', mv);
    document.addEventListener('mouseup', up);
  });

  document.addEventListener('keydown', e => {
    const mod = e.metaKey || e.ctrlKey;
    if (e.key === 'Escape') { if (openMenuEl) closeMenu(); else if (modalStack.length) modalStack[modalStack.length - 1](); return; }
    if (!mod) return;
    const t = S.tabs.find(x => x.id === S.active);
    if (e.key === 'Enter' && t && t.kind === 'request') { e.preventDefault(); sendRequest(t); }
    else if (e.key === 's' && t && (t.kind === 'request' || t.kind === 'realtime')) { e.preventDefault(); saveRequestTab(t); }
    else if (e.key === 't') { e.preventDefault(); openTab({ kind: 'request', name: 'Untitled Request', ref: null }, true); }
    else if (e.key === 'w' && t) { e.preventDefault(); closeTab(t.id); }
    else if (e.key === 'k' || (e.key === 'f' && e.shiftKey)) { e.preventDefault(); $('#side-filter').focus(); }
    else if (e.key === 'o') { e.preventDefault(); openImport(); }
    else if (e.key === ',') { e.preventDefault(); openSettings(); }
    else if (/^[1-9]$/.test(e.key) && S.tabs[+e.key - 1]) { e.preventDefault(); activate(S.tabs[+e.key - 1].id); }
  });
}

async function boot() {
  await store.load();
  S.panel = store.get('panel', 'collections');
  S.expanded = new Set(store.get('expanded', []));
  S.ws = await api('/api/workspace');
  for (const k of ['collections', 'environments', 'globals', 'mocks', 'monitors']) S.ws[k] = S.ws[k] || [];
  applyTheme();
  S.tabs = store.get('tabs', []).filter(t => {
    if (t.ref && t.ref.itemId && !findItem(t.ref.itemId)) return false;
    if (t.ref && t.ref.colId && !getCol(t.ref.colId)) return false;
    if (t.kind === 'env' && !getEnv(t.ref.envId)) return false;
    return true;
  });
  S.active = store.get('active', null);
  if (!S.tabs.find(t => t.id === S.active)) S.active = S.tabs.length ? S.tabs[0].id : null;
  bindGlobal();
  renderEnvSelect();
  setPanel(S.panel in PANELS ? S.panel : 'collections');
  renderTabs();
  renderView();
}
window.addEventListener('pagehide', () => {
  store.set('tabs', S.tabs.map(persistableTab));
  store.set('active', S.active);
  try { navigator.sendBeacon('/api/uistate', new Blob([JSON.stringify(store.data)], { type: 'application/json' })); } catch { /* best effort */ }
});
window.addEventListener('DOMContentLoaded', () => boot().catch(e => { document.body.innerHTML = '<div class="err-box" style="margin:40px"><b>Lightpost failed to start</b><br>' + esc(e.message) + '</div>'; }));
