'use strict';
// Shared helpers. No framework: small DOM builder plus a few components.

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else if (k.startsWith('on')) el.addEventListener(k.slice(2).toLowerCase(), v);
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'value') el.value = v;
      else if (k === 'checked') el.checked = !!v;
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const uid = () => Math.random().toString(16).slice(2, 10) + Date.now().toString(16).slice(-6);
const clone = o => JSON.parse(JSON.stringify(o));

function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

function fmtSize(n) {
  if (n == null) return '';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(2) + ' MB';
  if (n >= 1024) return (n / 1024).toFixed(2) + ' KB';
  return n + ' B';
}
function fmtMs(n) { return n >= 1000 ? (n / 1000).toFixed(2) + ' s' : Math.round(n) + ' ms'; }
function fmtTime(t) {
  const d = new Date(t);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

// UI state is persisted by the server (see /api/uistate); load() runs at boot.
const store = {
  data: {},
  async load() { try { this.data = await (await fetch('/api/uistate')).json() || {}; } catch { this.data = {}; } },
  get(k, def) { return k in this.data ? this.data[k] : def; },
  set(k, v) { this.data[k] = v; this.flush(); },
  flush: null
};
store.flush = (() => { let t; return () => { clearTimeout(t); t = setTimeout(() => fetch('/api/uistate', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(store.data) }).catch(() => {}), 500); }; })();

async function api(path, opts = {}) {
  const init = { method: opts.method || 'GET', headers: {} };
  if (opts.body !== undefined) { init.body = JSON.stringify(opts.body); init.headers['Content-Type'] = 'application/json'; }
  if (opts.signal) init.signal = opts.signal;
  const r = await fetch(path, init);
  if (opts.raw) return r;
  const ct = r.headers.get('content-type') || '';
  const data = ct.includes('json') ? await r.json() : await r.text();
  if (!r.ok) throw new Error((data && data.error) || data || r.statusText);
  return data;
}

let toastTimer;
function toast(msg, isErr) {
  const t = $('#toast');
  t.textContent = msg;
  t.className = 'show' + (isErr ? ' err' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.className = ''), isErr ? 4000 : 2200);
}

// ---- modal ---------------------------------------------------------------
let modalStack = [];
function openModal({ title, body, foot, wide, onClose }) {
  const ov = $('#overlay');
  const m = h('div', { class: 'modal' + (wide ? ' wide' : '') },
    h('div', { class: 'modal-head' }, h('h3', null, title), h('button', { class: 'icon-btn', onclick: () => close() }, '×')),
    h('div', { class: 'modal-body' }, body),
    foot ? h('div', { class: 'modal-foot' }, foot) : null);
  const close = () => {
    m.remove();
    modalStack = modalStack.filter(x => x !== close);
    if (!ov.children.length) ov.classList.remove('show');
    onClose && onClose();
  };
  ov.append(m);
  ov.classList.add('show');
  modalStack.push(close);
  const first = m.querySelector('input:not([type=checkbox]),textarea,select');
  if (first) setTimeout(() => first.focus(), 30);
  return close;
}
$('#overlay').addEventListener('mousedown', e => { if (e.target.id === 'overlay' && modalStack.length) modalStack[modalStack.length - 1](); });

function promptModal(title, value = '', label = '') {
  return new Promise(res => {
    const inp = h('input', { value, style: { width: '100%' } });
    let done = false;
    const ok = () => { done = true; close(); res(inp.value.trim()); };
    inp.addEventListener('keydown', e => { if (e.key === 'Enter') ok(); });
    const close = openModal({
      title, body: [label ? h('div', { class: 'hint', style: { marginBottom: '6px' } }, label) : null, inp],
      foot: [h('button', { class: 'btn', onclick: () => close() }, 'Cancel'), h('button', { class: 'btn primary', onclick: ok }, 'OK')],
      onClose: () => { if (!done) res(null); }
    });
    setTimeout(() => inp.select(), 40);
  });
}

function confirmModal(title, text, okLabel = 'Delete') {
  return new Promise(res => {
    let done = false;
    const close = openModal({
      title, body: h('div', null, text),
      foot: [h('button', { class: 'btn', onclick: () => close() }, 'Cancel'),
        h('button', { class: 'btn primary', onclick: () => { done = true; close(); res(true); } }, okLabel)],
      onClose: () => { if (!done) res(false); }
    });
  });
}

// ---- context menu ---------------------------------------------------------
let openMenuEl = null;
function closeMenu() { if (openMenuEl) { openMenuEl.remove(); openMenuEl = null; } $$('.menu-open').forEach(e => e.classList.remove('menu-open')); }
function showMenu(x, y, items, anchorRow) {
  closeMenu();
  const m = h('div', { class: 'menu' });
  for (const it of items) {
    if (it === '-') { m.append(h('hr')); continue; }
    if (!it) continue;
    m.append(h('button', { class: it.danger ? 'danger' : '', onclick: () => { closeMenu(); it.action(); } }, it.label));
  }
  document.body.append(m);
  const r = m.getBoundingClientRect();
  m.style.left = Math.min(x, innerWidth - r.width - 8) + 'px';
  m.style.top = Math.min(y, innerHeight - r.height - 8) + 'px';
  openMenuEl = m;
  if (anchorRow) anchorRow.classList.add('menu-open');
}
document.addEventListener('mousedown', e => { if (openMenuEl && !openMenuEl.contains(e.target)) closeMenu(); });

// ---- JSON highlighting ------------------------------------------------------
function highlightJSON(text) {
  return esc(text).replace(/(&quot;(?:\\.|[^&\\]|&(?!quot;))*?&quot;)(\s*:)?|\b(true|false)\b|\bnull\b|-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/g,
    (m, s, colon, b) => {
      if (s) return colon ? `<span class="j-k">${s}</span>${colon}` : `<span class="j-s">${s}</span>`;
      if (b) return `<span class="j-b">${m}</span>`;
      if (m === 'null') return `<span class="j-z">${m}</span>`;
      return `<span class="j-n">${m}</span>`;
    });
}

function prettyBody(body, ct) {
  const t = (body || '').trim();
  if ((ct || '').includes('json') || /^[\[{]/.test(t)) {
    try { return { text: JSON.stringify(JSON.parse(t), null, 2), lang: 'json' }; } catch { /* not JSON */ }
  }
  if ((ct || '').match(/xml|html/) || t.startsWith('<')) return { text: formatXML(t), lang: 'xml' };
  return { text: body || '', lang: 'text' };
}

function formatXML(xml) {
  if (xml.length > 500000) return xml;
  let pad = 0;
  return xml.replace(/>\s*</g, '>\n<').split('\n').map(line => {
    if (/^<\/\w/.test(line)) pad = Math.max(pad - 1, 0);
    const out = '  '.repeat(pad) + line;
    if (/^<\w[^>]*[^\/]>.*$/.test(line) && !/<\/\w/.test(line) && !/^<(meta|link|br|img|input|hr)\b/i.test(line)) pad++;
    return out;
  }).join('\n');
}

// ---- query helpers -----------------------------------------------------------
function splitURL(url) {
  const i = url.indexOf('?');
  if (i < 0) return { base: url, query: '' };
  const hash = url.indexOf('#', i);
  return { base: url.slice(0, i), query: hash < 0 ? url.slice(i + 1) : url.slice(i + 1, hash) };
}
function safeDecode(s) { try { return decodeURIComponent(s.replace(/\+/g, ' ')); } catch { return s; } }
function parseQuery(q) {
  if (!q) return [];
  return q.split('&').filter(Boolean).map(p => {
    const i = p.indexOf('=');
    return { key: safeDecode(i < 0 ? p : p.slice(0, i)), value: i < 0 ? '' : safeDecode(p.slice(i + 1)), enabled: true };
  });
}
// Keep {{vars}} readable when encoding query params.
function encQ(s) { return String(s).split(/(\{\{[^}]*\}\})/).map((part, i) => i % 2 ? part : encodeURIComponent(part).replace(/%20/g, '+').replace(/%2C/g, ',').replace(/%3A/g, ':').replace(/%2F/g, '/')).join(''); }
function buildQuery(params) {
  return params.filter(p => p.enabled && (p.key || p.value)).map(p => encQ(p.key) + (p.value !== '' || p.key === '' ? '=' + encQ(p.value) : '')).join('&');
}
function pathVarNames(url) {
  const { base } = splitURL(url);
  const path = base.replace(/^[a-z]+:\/\/[^\/]*/i, '').replace(/^\{\{[^}]+\}\}/, '');
  return (path.match(/\/:([A-Za-z_][\w-]*)/g) || []).map(s => s.slice(2));
}

// ---- KV table ------------------------------------------------------------------
// opts: {rows, onChange, placeholderKey, placeholderVal, files, desc, readonly}
function kvTable(opts) {
  let rows = opts.rows;
  const wrap = h('div');
  let bulk = false;

  const ensureTrailing = () => {
    if (opts.fixed) return;
    const last = rows[rows.length - 1];
    if (!last || last.key || last.value) rows.push({ key: '', value: '', enabled: true });
  };
  const emit = () => opts.onChange(rows.filter(r => r.key || r.value || r.description));

  function render() {
    wrap.innerHTML = '';
    const tools = h('div', { class: 'kv-tools' }, h('span', { class: 'lbl' }, opts.title || ''),
      opts.fixed ? null : h('button', { class: 'btn sm', onclick: () => { bulk = !bulk; render(); } }, bulk ? 'Key-Value Edit' : 'Bulk Edit'));
    wrap.append(tools);
    if (bulk) {
      const ta = h('textarea', { class: 'code-area', style: { minHeight: '180px' }, spellcheck: 'false' });
      ta.value = rows.filter(r => r.key || r.value).map(r => (r.enabled ? '' : '//') + r.key + ':' + r.value).join('\n');
      ta.addEventListener('input', () => {
        rows = ta.value.split('\n').filter(l => l.trim()).map(l => {
          const off = l.startsWith('//'); if (off) l = l.slice(2);
          const i = l.indexOf(':');
          return { key: (i < 0 ? l : l.slice(0, i)).trim(), value: i < 0 ? '' : l.slice(i + 1).trim(), enabled: !off };
        });
        emit();
      });
      wrap.append(ta);
      return;
    }
    ensureTrailing();
    const tbl = h('table', { class: 'kv' },
      h('colgroup', null, h('col', { class: 'ck', style: { width: '30px' } }), h('col'), opts.files ? h('col', { style: { width: '72px' } }) : null, h('col'), opts.desc ? h('col') : null, h('col', { style: { width: opts.secrets ? '58px' : '30px' } })),
      h('tr', null, h('th', null, ''), h('th', null, 'Key'), opts.files ? h('th', null, 'Type') : null, h('th', null, 'Value'), opts.desc ? h('th', null, 'Description') : null, h('th', null, '')));
    rows.forEach((r, i) => {
      const isLast = i === rows.length - 1 && !opts.fixed;
      const tr = h('tr', { class: r.enabled === false ? 'off' : '' });
      const ck = h('input', { type: 'checkbox', checked: r.enabled !== false, onchange: e => { r.enabled = e.target.checked; tr.className = r.enabled ? '' : 'off'; emit(); } });
      const onEdit = (field) => e => {
        r[field] = e.target.value;
        if (isLast && (r.key || r.value)) { emit(); render(); const ins = $$('tr', wrap)[i + 1]; const el = ins && ins.querySelectorAll('input:not([type=checkbox])')[field === 'key' ? 0 : 1]; if (el) { el.focus(); el.setSelectionRange(el.value.length, el.value.length); } return; }
        emit();
      };
      const key = h('input', { value: r.key, placeholder: isLast ? (opts.placeholderKey || 'Key') : '', spellcheck: 'false', readonly: opts.fixed || null, oninput: onEdit('key') });
      let valCell;
      if (opts.files && r.type === 'file') {
        valCell = h('div', { class: 'file-cell' },
          h('input', { value: r.value, placeholder: 'File path', spellcheck: 'false', oninput: onEdit('value') }),
          h('button', { class: 'btn sm', onclick: async () => { try { const { path } = await api('/api/pickfile', { method: 'POST', body: {} }); r.value = path; emit(); render(); } catch { /* cancelled */ } } }, 'Choose'));
      } else {
        const secret = r.type === 'secret';
        valCell = h('input', { value: r.value, type: secret ? 'password' : 'text', placeholder: isLast ? (opts.placeholderVal || 'Value') : '', spellcheck: 'false', oninput: onEdit('value'), onfocus: secret ? e => (e.target.type = 'text') : null, onblur: secret ? e => (e.target.type = 'password') : null });
      }
      tr.append(h('td', { class: 'ck' }, isLast ? null : ck), h('td', null, key));
      if (opts.files) {
        tr.append(h('td', null, h('select', { onchange: e => { r.type = e.target.value; emit(); render(); } },
          h('option', { value: 'text', selected: r.type !== 'file' }, 'Text'), h('option', { value: 'file', selected: r.type === 'file' }, 'File'))));
      }
      tr.append(h('td', null, valCell));
      if (opts.desc) tr.append(h('td', null, h('input', { value: r.description || '', placeholder: isLast ? 'Description' : '', oninput: e => { r.description = e.target.value; emit(); } })));
      tr.append(h('td', { class: 'act', style: opts.secrets ? { whiteSpace: 'nowrap' } : null }, isLast || opts.fixed ? null : [
        opts.secrets ? h('button', { class: 'icon-btn', title: r.type === 'secret' ? 'Secret (click to reveal type)' : 'Mark as secret', style: { fontSize: '12px', opacity: r.type === 'secret' ? 1 : .45 }, onclick: () => { r.type = r.type === 'secret' ? '' : 'secret'; emit(); render(); } }, r.type === 'secret' ? '🔒' : '🔓') : null,
        h('button', { class: 'icon-btn', title: 'Remove', onclick: () => { rows.splice(i, 1); emit(); render(); } }, '×')]));
      tbl.append(tr);
    });
    wrap.append(tbl);
  }
  render();
  wrap.setRows = r => { rows = r; render(); };
  return wrap;
}

// Code textarea with tab indentation and auto-indent.
function codeArea(value, onInput, attrs = {}) {
  const ta = h('textarea', Object.assign({ class: 'code-area', spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off' }, attrs));
  ta.value = value || '';
  ta.addEventListener('input', () => onInput(ta.value));
  ta.addEventListener('keydown', e => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const s = ta.selectionStart, en = ta.selectionEnd;
      ta.setRangeText('  ', s, en, 'end');
      onInput(ta.value);
    } else if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey) {
      const s = ta.selectionStart;
      const line = ta.value.slice(ta.value.lastIndexOf('\n', s - 1) + 1, s);
      const ind = line.match(/^\s*/)[0] + (/[{\[(]\s*$/.test(line) ? '  ' : '');
      e.preventDefault();
      ta.setRangeText('\n' + ind, s, ta.selectionEnd, 'end');
      onInput(ta.value);
    }
  });
  return ta;
}

// Minimal Handlebars-compatible renderer for pm.visualizer templates.
function renderTemplate(tpl, data) {
  const get = (ctx, path, stack) => {
    if (path === 'this' || path === '.') return ctx;
    if (path.startsWith('@')) return ctx && ctx[path];
    if (path.startsWith('../')) return get(stack[stack.length - 2] || ctx, path.slice(3), stack.slice(0, -1));
    path = path.replace(/^this\./, '');
    return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), ctx);
  };
  const tokens = tpl.split(/(\{\{\{[^}]+\}\}\}|\{\{[^}]+\}\})/);
  let pos = 0;
  function parse(until) {
    const nodes = [];
    while (pos < tokens.length) {
      const t = tokens[pos++];
      if (!t.startsWith('{{')) { nodes.push({ text: t }); continue; }
      const raw = t.startsWith('{{{');
      const inner = t.slice(raw ? 3 : 2, raw ? -3 : -2).trim();
      if (inner.startsWith('/')) return { nodes, end: inner.slice(1).trim() };
      if (inner === 'else') { nodes.push({ else: true }); continue; }
      if (inner.startsWith('!')) continue;
      if (inner.startsWith('#')) {
        const [kw, ...rest] = inner.slice(1).split(/\s+/);
        const body = parse(kw);
        const idx = body.nodes.findIndex(n => n.else);
        nodes.push({ block: kw, arg: rest.join(' '), body: idx < 0 ? body.nodes : body.nodes.slice(0, idx), alt: idx < 0 ? [] : body.nodes.slice(idx + 1) });
        continue;
      }
      nodes.push({ v: inner, raw });
    }
    return { nodes };
  }
  const tree = parse().nodes;
  function run(nodes, ctx, stack) {
    let out = '';
    for (const n of nodes) {
      if (n.text != null) out += n.text;
      else if (n.v) { const v = get(ctx, n.v, stack); out += v == null ? '' : (n.raw ? String(typeof v === 'object' ? JSON.stringify(v) : v) : esc(typeof v === 'object' ? JSON.stringify(v) : v)); }
      else if (n.block === 'each') {
        const list = get(ctx, n.arg, stack);
        const items = Array.isArray(list) ? list : list && typeof list === 'object' ? Object.values(list) : [];
        if (!items.length) out += run(n.alt, ctx, stack);
        items.forEach((it, i) => {
          const c = it !== null && typeof it === 'object' ? Object.assign(Array.isArray(it) ? [...it] : { ...it }, { '@index': i, '@first': i === 0, '@last': i === items.length - 1 }) : it;
          out += run(n.body, c, [...stack, c]);
        });
      } else if (n.block === 'if' || n.block === 'unless') {
        let v = get(ctx, n.arg, stack);
        if (Array.isArray(v)) v = v.length;
        out += run((n.block === 'if' ? !!v : !v) ? n.body : n.alt, ctx, stack);
      } else if (n.block === 'with') {
        const c = get(ctx, n.arg, stack);
        out += run(n.body, c, [...stack, c]);
      }
    }
    return out;
  }
  return run(tree, data, [data]);
}
