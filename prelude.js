// Lightpost script runtime: a pm.* API compatible with common Postman scripts.
// Go provides the __-prefixed bindings.
(function (g) {
  'use strict';
  function fmt(v) {
    if (typeof v === 'string') return v;
    try { return JSON.stringify(v); } catch (e) { return String(v); }
  }
  function deepEqual(a, b) {
    if (a === b) return true;
    if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') return a !== a && b !== b;
    if (Array.isArray(a) !== Array.isArray(b)) return false;
    var ka = Object.keys(a), kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    for (var i = 0; i < ka.length; i++) if (!deepEqual(a[ka[i]], b[ka[i]])) return false;
    return true;
  }
  function typeOf(v) {
    if (v === null) return 'null';
    if (Array.isArray(v)) return 'array';
    if (v instanceof RegExp) return 'regexp';
    return typeof v;
  }
  function AssertionError(msg) { this.name = 'AssertionError'; this.message = msg; }
  AssertionError.prototype = Object.create(Error.prototype);

  // --- chai-style expect ---------------------------------------------------
  function Assertion(obj, flags) { this._obj = obj; this._f = flags || {}; }
  var A = Assertion.prototype;
  A._assert = function (ok, msg, negMsg) {
    if (this._f.negate) ok = !ok;
    if (!ok) throw new AssertionError(this._f.negate ? negMsg : msg);
    return this;
  };
  ['to', 'be', 'been', 'is', 'that', 'which', 'and', 'has', 'have', 'with', 'at', 'of', 'same', 'but', 'does', 'still', 'also']
    .forEach(function (w) { Object.defineProperty(A, w, { get: function () { return this; } }); });
  Object.defineProperty(A, 'not', { get: function () { this._f.negate = !this._f.negate; return this; } });
  Object.defineProperty(A, 'deep', { get: function () { this._f.deep = true; return this; } });
  Object.defineProperty(A, 'nested', { get: function () { this._f.nested = true; return this; } });
  Object.defineProperty(A, 'any', { get: function () { this._f.any = true; return this; } });
  Object.defineProperty(A, 'all', { get: function () { this._f.all = true; return this; } });
  function prop(name, fn) { Object.defineProperty(A, name, { get: function () { fn.call(this); return this; } }); }
  prop('ok', function () { this._assert(!!this._obj, 'expected ' + fmt(this._obj) + ' to be truthy', 'expected ' + fmt(this._obj) + ' to be falsy'); });
  prop('true', function () { this._assert(this._obj === true, 'expected ' + fmt(this._obj) + ' to be true', 'expected value not to be true'); });
  prop('false', function () { this._assert(this._obj === false, 'expected ' + fmt(this._obj) + ' to be false', 'expected value not to be false'); });
  prop('null', function () { this._assert(this._obj === null, 'expected ' + fmt(this._obj) + ' to be null', 'expected value not to be null'); });
  prop('undefined', function () { this._assert(this._obj === undefined, 'expected ' + fmt(this._obj) + ' to be undefined', 'expected value not to be undefined'); });
  prop('NaN', function () { this._assert(this._obj !== this._obj, 'expected value to be NaN', 'expected value not to be NaN'); });
  prop('exist', function () { this._assert(this._obj !== null && this._obj !== undefined, 'expected value to exist', 'expected ' + fmt(this._obj) + ' not to exist'); });
  prop('empty', function () {
    var o = this._obj, n = o == null ? 0 : (typeof o === 'string' || Array.isArray(o)) ? o.length : Object.keys(o).length;
    this._assert(n === 0, 'expected ' + fmt(o) + ' to be empty', 'expected ' + fmt(o) + ' not to be empty');
  });
  A.a = A.an = function (t) {
    t = String(t).toLowerCase();
    return this._assert(typeOf(this._obj) === t, 'expected ' + fmt(this._obj) + ' to be a ' + t, 'expected ' + fmt(this._obj) + ' not to be a ' + t);
  };
  A.equal = A.equals = A.eq = function (v) {
    var ok = this._f.deep ? deepEqual(this._obj, v) : this._obj === v;
    return this._assert(ok, 'expected ' + fmt(this._obj) + ' to equal ' + fmt(v), 'expected ' + fmt(this._obj) + ' to not equal ' + fmt(v));
  };
  A.eql = A.eqls = function (v) { return this._assert(deepEqual(this._obj, v), 'expected ' + fmt(this._obj) + ' to deeply equal ' + fmt(v), 'expected ' + fmt(this._obj) + ' to not deeply equal ' + fmt(v)); };
  A.above = A.gt = A.greaterThan = function (n) { return this._assert(this._obj > n, 'expected ' + fmt(this._obj) + ' to be above ' + n, 'expected ' + fmt(this._obj) + ' to be at most ' + n); };
  A.below = A.lt = A.lessThan = function (n) { return this._assert(this._obj < n, 'expected ' + fmt(this._obj) + ' to be below ' + n, 'expected ' + fmt(this._obj) + ' to be at least ' + n); };
  A.least = A.gte = function (n) { return this._assert(this._obj >= n, 'expected ' + fmt(this._obj) + ' to be at least ' + n, 'expected ' + fmt(this._obj) + ' to be below ' + n); };
  A.most = A.lte = function (n) { return this._assert(this._obj <= n, 'expected ' + fmt(this._obj) + ' to be at most ' + n, 'expected ' + fmt(this._obj) + ' to be above ' + n); };
  A.within = function (a, b) { return this._assert(this._obj >= a && this._obj <= b, 'expected ' + fmt(this._obj) + ' to be within ' + a + '..' + b, 'expected ' + fmt(this._obj) + ' not to be within ' + a + '..' + b); };
  A.include = A.includes = A.contain = A.contains = function (v) {
    var o = this._obj, ok;
    if (typeof o === 'string') ok = o.indexOf(v) !== -1;
    else if (Array.isArray(o)) ok = o.some(function (x) { return deepEqual(x, v); });
    else if (o && typeof v === 'object') ok = Object.keys(v).every(function (k) { return deepEqual(o[k], v[k]); });
    else ok = false;
    return this._assert(ok, 'expected ' + fmt(o) + ' to include ' + fmt(v), 'expected ' + fmt(o) + ' to not include ' + fmt(v));
  };
  A.match = A.matches = function (re) { return this._assert(re.test(this._obj), 'expected ' + fmt(this._obj) + ' to match ' + re, 'expected ' + fmt(this._obj) + ' not to match ' + re); };
  A.string = function (s) { return this._assert(String(this._obj).indexOf(s) !== -1, 'expected ' + fmt(this._obj) + ' to contain ' + fmt(s), 'expected ' + fmt(this._obj) + ' not to contain ' + fmt(s)); };
  A.oneOf = function (list) { var o = this._obj; return this._assert(list.some(function (x) { return deepEqual(x, o); }), 'expected ' + fmt(o) + ' to be one of ' + fmt(list), 'expected ' + fmt(o) + ' not to be one of ' + fmt(list)); };
  A.property = function (name, val) {
    var o = this._obj, has, v;
    if (this._f.nested) {
      v = o; has = true;
      String(name).split('.').forEach(function (p) { if (v != null && p in Object(v)) v = v[p]; else { has = false; v = undefined; } });
    } else { has = o != null && name in Object(o); v = has ? o[name] : undefined; }
    if (arguments.length > 1) {
      this._assert(has && deepEqual(v, val), 'expected ' + fmt(o) + ' to have property ' + name + ' of ' + fmt(val) + ', but got ' + fmt(v), 'expected ' + fmt(o) + ' to not have property ' + name + ' of ' + fmt(val));
    } else this._assert(has, 'expected ' + fmt(o) + ' to have property ' + name, 'expected ' + fmt(o) + ' to not have property ' + name);
    return has && !this._f.negate ? new Assertion(v) : this;
  };
  A.ownProperty = function (n) { return this._assert(Object.prototype.hasOwnProperty.call(this._obj, n), 'expected own property ' + n, 'expected no own property ' + n); };
  A.lengthOf = function (n) { var l = this._obj == null ? undefined : this._obj.length; return this._assert(l === n, 'expected ' + fmt(this._obj) + ' to have length ' + n + ' but got ' + l, 'expected length not to be ' + n); };
  Object.defineProperty(A, 'length', { get: function () { var self = this; var f = function (n) { return self.lengthOf(n); }; f.above = function (n) { return new Assertion(self._obj.length, self._f).above(n); }; f.below = function (n) { return new Assertion(self._obj.length, self._f).below(n); }; f.least = function (n) { return new Assertion(self._obj.length, self._f).least(n); }; f.most = function (n) { return new Assertion(self._obj.length, self._f).most(n); }; return f; } });
  A.keys = A.key = function () {
    var want = Array.isArray(arguments[0]) ? arguments[0] : Array.prototype.slice.call(arguments);
    var have = Object.keys(this._obj || {});
    var ok = this._f.any ? want.some(function (k) { return have.indexOf(k) !== -1; })
      : want.every(function (k) { return have.indexOf(k) !== -1; }) && (!this._f.all || have.length === want.length);
    return this._assert(ok, 'expected ' + fmt(have) + ' to have keys ' + fmt(want), 'expected ' + fmt(have) + ' to not have keys ' + fmt(want));
  };
  A.members = function (list) {
    var o = this._obj || [];
    var ok = list.every(function (x) { return o.some(function (y) { return deepEqual(x, y); }); }) && list.length === o.length;
    return this._assert(ok, 'expected ' + fmt(o) + ' to have members ' + fmt(list), 'expected ' + fmt(o) + ' to not have members ' + fmt(list));
  };
  A.instanceof = A.instanceOf = function (C) { return this._assert(this._obj instanceof C, 'expected instance', 'expected not instance'); };
  A.throw = A.throws = function () { var t = false; try { this._obj(); } catch (e) { t = true; } return this._assert(t, 'expected function to throw', 'expected function not to throw'); };
  A.satisfy = function (fn) { return this._assert(!!fn(this._obj), 'expected ' + fmt(this._obj) + ' to satisfy predicate', 'expected ' + fmt(this._obj) + ' not to satisfy predicate'); };
  A.jsonSchema = function (schema) { var errs = validateSchema(this._obj, schema, '$'); return this._assert(errs.length === 0, 'schema: ' + errs.join('; '), 'expected value not to match schema'); };
  A.fail = function (m) { throw new AssertionError(m || 'assertion failed'); };

  function expect(v, msg) { return new Assertion(v); }
  expect.fail = function (m) { throw new AssertionError(m || 'expect.fail()'); };

  // --- tiny JSON Schema (draft-07 subset) -----------------------------------
  function validateSchema(v, s, path) {
    var errs = [];
    if (!s || typeof s !== 'object') return errs;
    if (s.type) {
      var types = Array.isArray(s.type) ? s.type : [s.type], t = typeOf(v);
      var ok = types.some(function (x) { return x === t || (x === 'integer' && t === 'number' && v % 1 === 0); });
      if (!ok) { errs.push(path + ' should be ' + types.join('|') + ' but is ' + t); return errs; }
    }
    if (s.enum && !s.enum.some(function (x) { return deepEqual(x, v); })) errs.push(path + ' should be one of ' + fmt(s.enum));
    if ('const' in s && !deepEqual(s.const, v)) errs.push(path + ' should equal ' + fmt(s.const));
    if (typeof v === 'string') {
      if (s.minLength != null && v.length < s.minLength) errs.push(path + ' shorter than ' + s.minLength);
      if (s.maxLength != null && v.length > s.maxLength) errs.push(path + ' longer than ' + s.maxLength);
      if (s.pattern && !new RegExp(s.pattern).test(v)) errs.push(path + ' does not match ' + s.pattern);
    }
    if (typeof v === 'number') {
      if (s.minimum != null && v < s.minimum) errs.push(path + ' < ' + s.minimum);
      if (s.maximum != null && v > s.maximum) errs.push(path + ' > ' + s.maximum);
    }
    if (Array.isArray(v)) {
      if (s.minItems != null && v.length < s.minItems) errs.push(path + ' has fewer than ' + s.minItems + ' items');
      if (s.maxItems != null && v.length > s.maxItems) errs.push(path + ' has more than ' + s.maxItems + ' items');
      if (s.items) v.forEach(function (x, i) { errs = errs.concat(validateSchema(x, s.items, path + '[' + i + ']')); });
    }
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      (s.required || []).forEach(function (k) { if (!(k in v)) errs.push(path + '.' + k + ' is required'); });
      var props = s.properties || {};
      Object.keys(props).forEach(function (k) { if (k in v) errs = errs.concat(validateSchema(v[k], props[k], path + '.' + k)); });
      if (s.additionalProperties === false) Object.keys(v).forEach(function (k) { if (!(k in props)) errs.push(path + '.' + k + ' is not allowed'); });
    }
    ['allOf'].forEach(function (k) { (s[k] || []).forEach(function (sub) { errs = errs.concat(validateSchema(v, sub, path)); }); });
    if (s.anyOf && !s.anyOf.some(function (sub) { return validateSchema(v, sub, path).length === 0; })) errs.push(path + ' matches none of anyOf');
    if (s.oneOf && s.oneOf.filter(function (sub) { return validateSchema(v, sub, path).length === 0; }).length !== 1) errs.push(path + ' must match exactly one of oneOf');
    return errs;
  }

  // --- variable scopes -------------------------------------------------------
  function scope(name) {
    return {
      get: function (k) { return __vget(name, k); },
      set: function (k, v) { __vset(name, k, typeof v === 'string' ? v : fmt(v)); },
      unset: function (k) { __vunset(name, k); },
      has: function (k) { return __vhas(name, k); },
      clear: function () { __vclear(name); },
      toObject: function () { return JSON.parse(__vobj(name)); },
      replaceIn: function (s) { return __resolve(s); }
    };
  }

  // --- headers list ---------------------------------------------------------
  function HeaderList(arr) { this._h = arr || []; }
  HeaderList.prototype = {
    _find: function (k) { k = String(k).toLowerCase(); for (var i = 0; i < this._h.length; i++) if (this._h[i].enabled !== false && String(this._h[i].key).toLowerCase() === k) return i; return -1; },
    get: function (k) { var i = this._find(k); return i < 0 ? undefined : this._h[i].value; },
    has: function (k, v) { var i = this._find(k); return i >= 0 && (v === undefined || this._h[i].value === v); },
    add: function (h) { if (typeof h === 'string') { var p = h.indexOf(':'); h = { key: h.slice(0, p).trim(), value: h.slice(p + 1).trim() }; } this._h.push({ key: h.key, value: String(h.value), enabled: true }); },
    upsert: function (h) { var i = this._find(h.key); if (i < 0) this.add(h); else this._h[i].value = String(h.value); },
    remove: function (k) { k = String(k).toLowerCase(); this._h = this._h.filter(function (h) { return String(h.key).toLowerCase() !== k; }); },
    toObject: function () { var o = {}; this._h.forEach(function (h) { if (h.enabled !== false) o[h.key] = h.value; }); return o; },
    each: function (fn) { this._h.forEach(function (h) { if (h.enabled !== false) fn(h); }); },
    all: function () { return this._h.slice(); },
    count: function () { return this._h.length; },
    toJSON: function () { return this._h; }
  };

  var ctx = JSON.parse(__ctx);
  var tests = [];
  var reqData = ctx.request || { method: 'GET', url: '', headers: [], body: { mode: 'none' } };
  var urlObj = {
    _s: reqData.url,
    toString: function () { return this._s; },
    getHost: function () { var m = /^[a-z]+:\/\/([^\/?#:]+)/i.exec(this._s); return m ? m[1] : ''; },
    getPath: function () { var m = /^[a-z]+:\/\/[^\/?#]+([^?#]*)/i.exec(this._s); return (m && m[1]) || '/'; },
    getQueryString: function () { var i = this._s.indexOf('?'); return i < 0 ? '' : this._s.slice(i + 1); },
    update: function (s) { this._s = String(s); },
    addQueryParams: function (ps) { var self = this; (Array.isArray(ps) ? ps : [ps]).forEach(function (p) { if (typeof p === 'string') { self._s += (self._s.indexOf('?') < 0 ? '?' : '&') + p; } else { self._s += (self._s.indexOf('?') < 0 ? '?' : '&') + encodeURIComponent(p.key) + '=' + encodeURIComponent(p.value); } }); },
    removeQueryParams: function (ks) { ks = Array.isArray(ks) ? ks : [ks]; var i = this._s.indexOf('?'); if (i < 0) return; var base = this._s.slice(0, i); var q = this._s.slice(i + 1).split('&').filter(function (kv) { return ks.indexOf(decodeURIComponent(kv.split('=')[0])) === -1; }); this._s = base + (q.length ? '?' + q.join('&') : ''); }
  };
  Object.defineProperty(urlObj, 'query', { get: function () { var q = urlObj.getQueryString(); var list = q ? q.split('&').map(function (kv) { var p = kv.split('='); return { key: decodeURIComponent(p[0]), value: decodeURIComponent(p.slice(1).join('=')) }; }) : []; return { all: function () { return list; }, get: function (k) { var f = list.filter(function (x) { return x.key === k; })[0]; return f && f.value; }, has: function (k) { return list.some(function (x) { return x.key === k; }); }, toObject: function () { var o = {}; list.forEach(function (x) { o[x.key] = x.value; }); return o; }, add: function (p) { urlObj.addQueryParams(p); }, remove: function (k) { urlObj.removeQueryParams(k); }, upsert: function (p) { urlObj.removeQueryParams(p.key); urlObj.addQueryParams(p); } }; } });
  var request = {
    id: ctx.info.requestId, name: ctx.info.requestName,
    method: reqData.method,
    headers: new HeaderList(reqData.headers || []),
    body: reqData.body || { mode: 'none' },
    auth: reqData.auth || null,
    addHeader: function (h) { this.headers.add(h); },
    removeHeader: function (k) { this.headers.remove(k); },
    upsertHeader: function (h) { this.headers.upsert(h); }
  };
  Object.defineProperty(request, 'url', { get: function () { return urlObj; }, set: function (v) { urlObj._s = String(v); }, enumerable: true });

  var response = null;
  if (ctx.response) {
    var r = ctx.response, parsed;
    response = {
      code: r.code, status: r.status, responseTime: r.time, responseSize: r.size,
      headers: new HeaderList(r.headers),
      text: function () { return r.body; },
      json: function () { if (parsed === undefined) parsed = JSON.parse(r.body); return parsed; },
      reason: function () { return r.status; },
      cookies: { get: function (n) { var c = (r.cookies || []).filter(function (c) { return c.name === n; })[0]; return c && c.value; }, has: function (n) { return (r.cookies || []).some(function (c) { return c.name === n; }); }, toObject: function () { var o = {}; (r.cookies || []).forEach(function (c) { o[c.name] = c.value; }); return o; } }
    };
    var to = {};
    var neg = false;
    function chk(ok, m, nm) { var n = neg; neg = false; if (n ? ok : !ok) throw new AssertionError(n ? nm : m); }
    var have = {
      status: function (c) { if (typeof c === 'number') chk(r.code === c, 'expected response to have status code ' + c + ' but got ' + r.code, 'expected response not to have status ' + c); else chk(r.status === c, 'expected response to have status reason ' + c + ' but got ' + r.status, 'expected status reason not to be ' + c); },
      header: function (k, v) { var h = response.headers.get(k); chk(h !== undefined && (v === undefined || h === v), 'expected response to have header ' + k + (v !== undefined ? ' with value ' + v + ' but got ' + h : ''), 'expected response not to have header ' + k); },
      body: function (b) { if (b === undefined) chk(r.body.length > 0, 'expected response to have a body', 'expected no body'); else if (b instanceof RegExp) chk(b.test(r.body), 'expected body to match ' + b, 'expected body not to match ' + b); else chk(r.body === b, 'expected response body to equal ' + fmt(b), 'expected body not to equal ' + fmt(b)); },
      jsonBody: function (path, val) { var j; try { j = response.json(); } catch (e) { chk(false, 'expected response body to be JSON', ''); return; } if (path === undefined) return; if (typeof path === 'object') { chk(deepEqual(j, path), 'expected JSON body to equal ' + fmt(path), 'expected JSON body not to equal'); return; } var v = j, ok = true; String(path).split('.').forEach(function (p) { if (v != null && p in Object(v)) v = v[p]; else { ok = false; } }); chk(ok && (val === undefined || deepEqual(v, val)), 'expected JSON body to have ' + path + (val !== undefined ? ' = ' + fmt(val) : ''), 'expected JSON body not to have ' + path); },
      jsonSchema: function (s) { var errs = validateSchema(response.json(), s, '$'); chk(errs.length === 0, 'response does not match schema: ' + errs.join('; '), 'expected response not to match schema'); },
      responseTime: { below: function (n) { chk(r.time < n, 'expected response time ' + r.time + 'ms to be below ' + n, ''); }, above: function (n) { chk(r.time > n, 'expected response time to be above ' + n, ''); } }
    };
    var be = {};
    Object.defineProperty(be, 'ok', { get: function () { chk(r.code >= 200 && r.code < 300, 'expected response to be ok (2xx) but got ' + r.code, 'expected response not to be ok'); } });
    Object.defineProperty(be, 'success', { get: function () { chk(r.code >= 200 && r.code < 300, 'expected 2xx but got ' + r.code, ''); } });
    Object.defineProperty(be, 'error', { get: function () { chk(r.code >= 400, 'expected error status but got ' + r.code, ''); } });
    Object.defineProperty(be, 'clientError', { get: function () { chk(r.code >= 400 && r.code < 500, 'expected 4xx but got ' + r.code, ''); } });
    Object.defineProperty(be, 'serverError', { get: function () { chk(r.code >= 500, 'expected 5xx but got ' + r.code, ''); } });
    Object.defineProperty(be, 'notFound', { get: function () { chk(r.code === 404, 'expected 404 but got ' + r.code, ''); } });
    Object.defineProperty(be, 'json', { get: function () { var ok = true; try { JSON.parse(r.body); } catch (e) { ok = false; } chk(ok, 'expected response body to be valid JSON', 'expected body not to be JSON'); } });
    to.have = have; to.be = be;
    Object.defineProperty(to, 'not', { get: function () { neg = true; return to; } });
    response.to = to;
  }

  var visualizer = null, nextReq, skip = false;
  var pm = {
    info: ctx.info,
    environment: scope('environment'),
    globals: scope('globals'),
    collectionVariables: scope('collection'),
    iterationData: scope('data'),
    variables: {
      get: function (k) { return __vlookup(k); },
      set: function (k, v) { __vset('local', k, typeof v === 'string' ? v : fmt(v)); },
      unset: function (k) { __vunset('local', k); },
      has: function (k) { return __vlookup(k) !== undefined; },
      replaceIn: function (s) { return __resolve(s); },
      toObject: function () { return JSON.parse(__vobj('resolved')); }
    },
    request: request,
    response: response,
    expect: expect,
    test: function (name, fn) {
      if (typeof fn !== 'function') { tests.push({ name: name, skipped: true, passed: true }); return; }
      try { fn(); tests.push({ name: name, passed: true }); }
      catch (e) { tests.push({ name: name, passed: false, error: (e && e.message) || String(e) }); }
    },
    sendRequest: function (req, cb) {
      if (typeof req === 'string') req = { url: req, method: 'GET' };
      var out = JSON.parse(__send(JSON.stringify(req)));
      var res = null;
      if (!out.error) {
        var rr = out.response;
        res = { code: rr.code, status: rr.status, responseTime: rr.time, headers: new HeaderList(rr.headers), text: function () { return rr.body; }, json: function () { return JSON.parse(rr.body); } };
      }
      if (cb) cb(out.error ? new Error(out.error) : null, res);
      return res;
    },
    cookies: {
      get: function (n) { return __cookie(n) || undefined; },
      has: function (n) { return !!__cookie(n); },
      toObject: function () { return JSON.parse(__cookies()); }
    },
    visualizer: { set: function (tpl, data) { visualizer = { template: tpl, data: data === undefined ? null : data }; }, clear: function () { visualizer = null; } },
    execution: {
      setNextRequest: function (n) { nextReq = n === null ? { stop: true } : { name: String(n) }; },
      skipRequest: function () { skip = true; },
      location: [ctx.info.requestName]
    }
  };
  pm.test.skip = function (name) { tests.push({ name: name, skipped: true, passed: true }); };
  pm.expect.fail = expect.fail;

  var postman = {
    setNextRequest: pm.execution.setNextRequest,
    setEnvironmentVariable: function (k, v) { pm.environment.set(k, v); },
    getEnvironmentVariable: function (k) { return pm.environment.get(k); },
    clearEnvironmentVariable: function (k) { pm.environment.unset(k); },
    setGlobalVariable: function (k, v) { pm.globals.set(k, v); },
    getGlobalVariable: function (k) { return pm.globals.get(k); },
    clearGlobalVariable: function (k) { pm.globals.unset(k); },
    getResponseHeader: function (k) { return response && response.headers.get(k); }
  };

  var consoleObj = {};
  ['log', 'info', 'warn', 'error', 'debug'].forEach(function (lvl) {
    consoleObj[lvl] = function () { __log(lvl, Array.prototype.map.call(arguments, fmt).join(' ')); };
  });

  // CryptoJS-compatible subset backed by Go.
  function WordArray(hex) { this._hex = hex; }
  WordArray.prototype.toString = function (enc) { return enc ? enc.stringify(this) : this._hex; };
  var Enc = {
    Hex: { stringify: function (w) { return w._hex; }, parse: function (s) { return new WordArray(s); } },
    Base64: { stringify: function (w) { return __hexToB64(w._hex); }, parse: function (s) { return new WordArray(__b64ToHex(s)); } },
    Utf8: { stringify: function (w) { return __hexToUtf8(w._hex); }, parse: function (s) { return new WordArray(__utf8ToHex(s)); } }
  };
  Enc.Latin1 = Enc.Utf8;
  function raw(v) { return v instanceof WordArray ? __hexToUtf8(v._hex) : String(v); }
  var CryptoJS = { enc: Enc };
  ['MD5', 'SHA1', 'SHA256', 'SHA512'].forEach(function (a) {
    CryptoJS[a] = function (m) { return new WordArray(__hash(a, raw(m), '')); };
    CryptoJS['Hmac' + a] = function (m, k) { return new WordArray(__hash(a, raw(m), raw(k))); };
  });

  var atob = function (s) { return __hexToUtf8(__b64ToHex(s)); };
  var btoa = function (s) { return __hexToB64(__utf8ToHex(s)); };

  var modules = { 'crypto-js': CryptoJS, chai: { expect: expect }, uuid: { v4: function () { return __uuid(); } }, atob: atob, btoa: btoa };

  g.pm = pm; g.postman = postman; g.console = consoleObj; g.CryptoJS = CryptoJS;
  g.atob = atob; g.btoa = btoa;
  g.require = function (n) { if (n in modules) return modules[n]; throw new Error('Cannot find module \'' + n + '\''); };
  g.tests = {};
  g.environment = pm.environment.toObject(); g.globals = pm.globals.toObject(); g.data = pm.iterationData.toObject();
  if (response) { g.responseBody = ctx.response.body; g.responseCode = { code: ctx.response.code, name: ctx.response.status }; g.responseTime = ctx.response.time; g.responseHeaders = response.headers.toObject(); }
  g.request = { url: reqData.url, method: reqData.method, headers: request.headers.toObject(), data: reqData.body && reqData.body.raw };

  g.__collect = function () {
    Object.keys(g.tests).forEach(function (k) { tests.push({ name: k, passed: !!g.tests[k], error: g.tests[k] ? undefined : 'tests["' + k + '"] is false' }); });
    return JSON.stringify({
      tests: tests,
      request: { method: request.method, url: String(request.url), headers: request.headers._h, body: request.body },
      visualizer: visualizer,
      next: nextReq || null,
      skip: skip
    });
  };
})(this);
