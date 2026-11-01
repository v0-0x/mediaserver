package extensions

// nuvioPrelude adapts the sandbox to what Nuvio providers expect (they
// are written for React Native): CommonJS globals, require() of the
// modules the Nuvio app bundles, a browser-like fetch (Headers,
// text()/json() returning promises, AbortController), a small axios, and
// TextEncoder/TextDecoder. It's called with (globalThis, cheerio module,
// the original require).
const nuvioPrelude = `(function (g, cheerio, origRequire) {
  'use strict';
  if (typeof g.global === 'undefined') g.global = g;
  if (typeof g.process === 'undefined') {
    g.process = { env: {}, argv: [], version: 'v18.0.0', versions: {}, platform: 'kumobox', browser: false,
      nextTick: function (fn) {
        var args = Array.prototype.slice.call(arguments, 1);
        Promise.resolve().then(function () { fn.apply(null, args); });
      } };
  }
  g.module = { exports: {} };
  g.exports = g.module.exports;

  if (typeof g.TextEncoder === 'undefined') {
    g.TextEncoder = function TextEncoder() { this.encoding = 'utf-8'; };
    g.TextEncoder.prototype.encode = function (s) { return $toBytes(String(s === undefined ? '' : s)); };
  }
  if (typeof g.TextDecoder === 'undefined') {
    g.TextDecoder = function TextDecoder() { this.encoding = 'utf-8'; };
    g.TextDecoder.prototype.decode = function (b) { return b === undefined ? '' : $toString(b); };
  }

  function hasHeader(h, name) {
    name = name.toLowerCase();
    for (var k in h) if (Object.prototype.hasOwnProperty.call(h, k) && k.toLowerCase() === name) return true;
    return false;
  }

  // Headers
  function Headers(init) {
    this._map = {};
    if (!init) return;
    var self = this;
    if (Array.isArray(init)) {
      init.forEach(function (p) { self.append(p[0], p[1]); });
    } else if (init instanceof Headers || typeof init.forEach === 'function') {
      init.forEach(function (v, k) { self.append(k, v); });
    } else {
      Object.keys(init).forEach(function (k) {
        var v = init[k];
        if (Array.isArray(v)) v.forEach(function (x) { self.append(k, x); });
        else if (v !== undefined && v !== null) self.append(k, v);
      });
    }
  }
  Headers.prototype.append = function (k, v) {
    k = String(k).toLowerCase(); v = String(v);
    this._map[k] = this._map[k] !== undefined ? this._map[k] + ', ' + v : v;
  };
  Headers.prototype.set = function (k, v) { this._map[String(k).toLowerCase()] = String(v); };
  Headers.prototype.get = function (k) { var v = this._map[String(k).toLowerCase()]; return v === undefined ? null : v; };
  Headers.prototype.has = function (k) { return this._map[String(k).toLowerCase()] !== undefined; };
  Headers.prototype['delete'] = function (k) { delete this._map[String(k).toLowerCase()]; };
  Headers.prototype.forEach = function (fn, thisArg) {
    var m = this._map, self = this;
    Object.keys(m).forEach(function (k) { fn.call(thisArg, m[k], k, self); });
  };
  Headers.prototype.entries = function () { var m = this._map; return Object.keys(m).map(function (k) { return [k, m[k]]; })[Symbol.iterator](); };
  Headers.prototype.keys = function () { return Object.keys(this._map)[Symbol.iterator](); };
  Headers.prototype.values = function () { var m = this._map; return Object.keys(m).map(function (k) { return m[k]; })[Symbol.iterator](); };
  Headers.prototype[Symbol.iterator] = Headers.prototype.entries;
  g.Headers = Headers;

  // plainHeaders flattens any headers value into {name: value}.
  function plainHeaders(h, method) {
    var out = {};
    if (!h) return out;
    if (h instanceof Headers) { h.forEach(function (v, k) { out[k] = v; }); return out; }
    if (Array.isArray(h)) { h.forEach(function (p) { out[String(p[0])] = String(p[1]); }); return out; }
    // axios-style defaults: {common: {...}, get: {...}, 'X-A': 'b'}
    var nested = ['common', 'get', 'post', 'put', 'patch', 'delete', 'head', 'options'];
    if (h.common && typeof h.common === 'object') Object.assign(out, plainHeaders(h.common));
    if (method && h[method] && typeof h[method] === 'object') Object.assign(out, plainHeaders(h[method]));
    Object.keys(h).forEach(function (k) {
      var v = h[k];
      if (nested.indexOf(k) >= 0 && v && typeof v === 'object') return;
      if (v !== undefined && v !== null && typeof v !== 'function') out[k] = String(v);
    });
    return out;
  }

  // AbortController
  function abortError(msg) { var e = new Error(msg || 'This operation was aborted'); e.name = 'AbortError'; return e; }
  function AbortSignal() { this.aborted = false; this.reason = undefined; this.onabort = null; this._listeners = []; }
  AbortSignal.prototype.addEventListener = function (type, fn) { if (type === 'abort' && typeof fn === 'function') this._listeners.push(fn); };
  AbortSignal.prototype.removeEventListener = function (type, fn) { this._listeners = this._listeners.filter(function (x) { return x !== fn; }); };
  AbortSignal.prototype.throwIfAborted = function () { if (this.aborted) throw this.reason; };
  AbortSignal.prototype._abort = function (reason) {
    if (this.aborted) return;
    this.aborted = true;
    this.reason = reason !== undefined ? reason : abortError();
    var ev = { type: 'abort', target: this };
    if (typeof this.onabort === 'function') { try { this.onabort(ev); } catch (e) {} }
    this._listeners.slice().forEach(function (fn) { try { fn(ev); } catch (e) {} });
  };
  AbortSignal.abort = function (reason) { var s = new AbortSignal(); s._abort(reason); return s; };
  AbortSignal.timeout = function (ms) {
    var s = new AbortSignal();
    setTimeout(function () { var e = new Error('The operation timed out'); e.name = 'TimeoutError'; s._abort(e); }, ms);
    return s;
  };
  function AbortController() { this.signal = new AbortSignal(); }
  AbortController.prototype.abort = function (reason) { this.signal._abort(reason); };
  if (typeof g.AbortController === 'undefined') { g.AbortController = AbortController; g.AbortSignal = AbortSignal; }

  // withSignal rejects p when signal aborts first.
  function withSignal(p, signal) {
    if (!signal || typeof signal.addEventListener !== 'function') return p;
    if (signal.aborted) return Promise.reject(signal.reason || abortError());
    return new Promise(function (resolve, reject) {
      signal.addEventListener('abort', function () { reject(signal.reason || abortError()); });
      p.then(resolve, reject);
    });
  }

  // fetch: the sandbox's, with browser semantics on top.
  var rawFetch = g.fetch;
  function makeResponse(r) {
    var headers = new Headers();
    var raw = r.rawHeaders || {};
    Object.keys(raw).forEach(function (k) { (raw[k] || []).forEach(function (v) { headers.append(k, v); }); });
    return {
      ok: r.ok, status: r.status, statusText: r.statusText, url: r.url, redirected: r.redirected, type: 'basic',
      headers: headers, bodyUsed: false,
      text: function () { return Promise.resolve(r.text()); },
      json: function () { return new Promise(function (resolve) { resolve(JSON.parse(r.text())); }); },
      arrayBuffer: function () { return Promise.resolve(r.arrayBuffer()); },
      blob: function () { return Promise.reject(new Error('Response.blob() is not available in KumoBox')); },
      clone: function () { return makeResponse(r); }
    };
  }
  function fetchBody(body, headers) {
    if (body === undefined || body === null) return undefined;
    if (typeof URLSearchParams !== 'undefined' && body instanceof URLSearchParams) {
      if (!hasHeader(headers, 'content-type')) headers['content-type'] = 'application/x-www-form-urlencoded;charset=UTF-8';
      return body.toString();
    }
    return body;
  }
  g.fetch = function (input, init) {
    init = init || {};
    var url = input;
    if (input && typeof input === 'object') {
      url = input.url || input.href || String(input);
      if (input.url) init = Object.assign({ method: input.method, headers: input.headers, body: input.body }, init);
    }
    var method = String(init.method || 'GET').toUpperCase();
    var headers = plainHeaders(init.headers);
    var opts = { method: method, headers: headers };
    var body = fetchBody(init.body, headers);
    if (body !== undefined) opts.body = body;
    if (init.redirect) opts.redirect = init.redirect;
    // node-fetch style timeout, in milliseconds (the sandbox's is in seconds)
    if (typeof init.timeout === 'number' && init.timeout > 0) opts.timeout = Math.max(1, Math.ceil(init.timeout / 1000));
    return withSignal(Promise.resolve().then(function () { return rawFetch(String(url), opts); }).then(makeResponse), init.signal);
  };

  // axios
  function Interceptors() { this.handlers = []; }
  Interceptors.prototype.use = function (ok, fail) { this.handlers.push({ ok: ok, fail: fail }); return this.handlers.length - 1; };
  Interceptors.prototype.eject = function (i) { this.handlers[i] = null; };
  Interceptors.prototype.clear = function () { this.handlers = []; };

  function buildURL(url, params, baseURL) {
    url = String(url || '');
    if (baseURL && !/^[a-z][a-z0-9+.-]*:/i.test(url)) url = String(baseURL).replace(/\/+$/, '') + '/' + url.replace(/^\/+/, '');
    if (!params) return url;
    var parts = [];
    if (typeof URLSearchParams !== 'undefined' && params instanceof URLSearchParams) {
      var s = params.toString();
      if (s) parts.push(s);
    } else {
      Object.keys(params).forEach(function (k) {
        var v = params[k];
        var add = function (key, x) {
          if (x === undefined || x === null) return;
          if (x instanceof Date) x = x.toISOString();
          else if (typeof x === 'object') x = JSON.stringify(x);
          parts.push(encodeURIComponent(key) + '=' + encodeURIComponent(x));
        };
        if (Array.isArray(v)) v.forEach(function (x) { add(k + '[]', x); });
        else add(k, v);
      });
    }
    if (!parts.length) return url;
    return url + (url.indexOf('?') >= 0 ? '&' : '?') + parts.join('&');
  }
  function mergeConfig(a, b) {
    var out = Object.assign({}, a || {}, b || {});
    out.headers = Object.assign({}, (a && a.headers) || {}, (b && b.headers) || {});
    return out;
  }
  function axiosError(message, config, response, code) {
    var e = new Error(message);
    e.name = 'AxiosError';
    e.isAxiosError = true;
    e.config = config;
    e.code = code;
    if (response) { e.response = response; e.status = response.status; }
    e.toJSON = function () { return { message: message, code: code, status: e.status }; };
    return e;
  }
  function responseHeaders(r) {
    var out = {};
    var raw = r.rawHeaders || {};
    Object.keys(raw).forEach(function (k) {
      var v = raw[k] || [];
      out[k.toLowerCase()] = k.toLowerCase() === 'set-cookie' ? v.slice() : v.join(', ');
    });
    Object.defineProperty(out, 'get', { enumerable: false, value: function (k) { var v = out[String(k).toLowerCase()]; return v === undefined ? undefined : v; } });
    return out;
  }
  function createAxios(defaults) {
    var interceptors = { request: new Interceptors(), response: new Interceptors() };
    function send(config) {
      var method = String(config.method || 'get').toLowerCase();
      var headers = plainHeaders(config.headers, method);
      var data = config.data;
      if (data !== undefined && data !== null) {
        if (typeof URLSearchParams !== 'undefined' && data instanceof URLSearchParams) {
          if (!hasHeader(headers, 'content-type')) headers['content-type'] = 'application/x-www-form-urlencoded;charset=utf-8';
          data = data.toString();
        } else if (typeof data === 'object' && !(data instanceof ArrayBuffer) && !ArrayBuffer.isView(data) && !data.__kumoboxFormData) {
          if (!hasHeader(headers, 'content-type')) headers['content-type'] = 'application/json';
          data = JSON.stringify(data);
        }
      }
      if (!hasHeader(headers, 'accept')) headers['accept'] = 'application/json, text/plain, */*';
      var opts = { method: method.toUpperCase(), headers: headers };
      if (data !== undefined && data !== null && method !== 'get' && method !== 'head') opts.body = data;
      if (config.timeout > 0) opts.timeout = Math.max(1, Math.ceil(config.timeout / 1000));
      if (config.maxRedirects === 0) opts.redirect = 'manual';
      var url = buildURL(config.url, config.params, config.baseURL);
      var p = Promise.resolve().then(function () { return rawFetch(url, opts); }).then(function (r) {
        var text = r.text();
        var rt = config.responseType;
        var body = text;
        if (rt === 'arraybuffer') {
          body = r.arrayBuffer();
        } else if (rt !== 'text' && rt !== 'document' && rt !== 'stream') {
          var t = text.replace(/^\s+/, '');
          if (rt === 'json' || t.charAt(0) === '{' || t.charAt(0) === '[' || /json/i.test(r.contentType || '')) {
            try { body = JSON.parse(text); } catch (e) { body = text; }
          }
        }
        var response = { data: body, status: r.status, statusText: r.statusText, headers: responseHeaders(r), config: config, request: { responseURL: r.url } };
        var validate = config.validateStatus === undefined ? function (s) { return s >= 200 && s < 300; } : config.validateStatus;
        if (!validate || validate(r.status)) return response;
        throw axiosError('Request failed with status code ' + r.status, config, response, r.status >= 500 ? 'ERR_BAD_RESPONSE' : 'ERR_BAD_REQUEST');
      }, function (err) {
        throw axiosError(err && err.message ? err.message : String(err), config, undefined, 'ERR_NETWORK');
      });
      return withSignal(p, config.signal);
    }
    function request(configOrUrl, maybeConfig) {
      var config = typeof configOrUrl === 'string' ? mergeConfig(maybeConfig, { url: configOrUrl }) : (configOrUrl || {});
      config = mergeConfig(instance.defaults, config);
      var chain = Promise.resolve(config);
      interceptors.request.handlers.forEach(function (h) { if (h) chain = chain.then(h.ok, h.fail); });
      chain = chain.then(send);
      interceptors.response.handlers.forEach(function (h) { if (h) chain = chain.then(h.ok, h.fail); });
      return chain;
    }
    var instance = function (a, b) { return request(a, b); };
    instance.request = request;
    ['get', 'delete', 'head', 'options'].forEach(function (m) {
      instance[m] = function (url, config) { return request(mergeConfig(config, { url: url, method: m })); };
    });
    ['post', 'put', 'patch'].forEach(function (m) {
      instance[m] = function (url, data, config) { return request(mergeConfig(config, { url: url, method: m, data: data })); };
    });
    instance.defaults = defaults;
    instance.interceptors = interceptors;
    instance.create = function (cfg) { return createAxios(mergeConfig(instance.defaults, cfg)); };
    instance.isAxiosError = function (e) { return !!(e && e.isAxiosError); };
    instance.isCancel = function () { return false; };
    instance.all = function (ps) { return Promise.all(ps); };
    instance.spread = function (fn) { return function (arr) { return fn.apply(null, arr); }; };
    instance.default = instance;
    return instance;
  }
  var axios = createAxios({ headers: { common: {} }, timeout: 0 });

  // require
  var C = g.CryptoJS;
  var modules = {
    'cheerio-without-node-native': cheerio, 'cheerio': cheerio, 'react-native-cheerio': cheerio,
    'crypto-js': C, 'axios': axios
  };
  var cryptoParts = {
    'md5': 'MD5', 'sha1': 'SHA1', 'sha256': 'SHA256', 'sha512': 'SHA512', 'aes': 'AES',
    'hmac-md5': 'HmacMD5', 'hmac-sha1': 'HmacSHA1', 'hmac-sha256': 'HmacSHA256', 'hmac-sha512': 'HmacSHA512'
  };
  var cryptoEnc = { 'enc-utf8': 'Utf8', 'enc-hex': 'Hex', 'enc-base64': 'Base64', 'enc-latin1': 'Latin1', 'enc-utf16': 'Utf16' };
  g.require = function (name) {
    name = String(name);
    if (Object.prototype.hasOwnProperty.call(modules, name)) return modules[name];
    var m = /^crypto-js\/(.+)$/.exec(name);
    if (m) {
      if (cryptoParts[m[1]] && C[cryptoParts[m[1]]]) return C[cryptoParts[m[1]]];
      if (cryptoEnc[m[1]] && C.enc[cryptoEnc[m[1]]]) return C.enc[cryptoEnc[m[1]]];
      if (m[1] === 'core' || m[1] === 'index') return C;
    }
    var bare = name.replace(/^node:/, '');
    if (bare === 'buffer' || bare === 'url') return origRequire(bare);
    throw new Error("Cannot find module '" + name + "' (Nuvio providers in KumoBox can use cheerio-without-node-native, crypto-js and axios)");
  };
})`
