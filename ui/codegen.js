'use strict';
// Code snippet generation. Input: a request whose variables are already
// resolved (or left as {{vars}}), with auth headers applied by prepareForCode.

function prepareForCode(req) {
  const r = clone(req);
  r.headers = (r.headers || []).filter(x => x.enabled && x.key);
  const a = r.auth || {};
  const p = a.params || {};
  const add = (k, v) => r.headers.push({ key: k, value: v, enabled: true });
  switch (a.type) {
    case 'basic': add('Authorization', 'Basic ' + btoa(unescape(encodeURIComponent((p.username || '') + ':' + (p.password || ''))))); break;
    case 'bearer': if (p.token) add('Authorization', 'Bearer ' + p.token); break;
    case 'oauth2': if (p.accessToken) add('Authorization', (p.headerPrefix || 'Bearer') + ' ' + p.accessToken); break;
    case 'apikey':
      if (p.key) {
        if (p.in === 'query') r.url += (r.url.includes('?') ? '&' : '?') + encodeURIComponent(p.key) + '=' + encodeURIComponent(p.value || '');
        else add(p.key, p.value || '');
      }
      break;
  }
  const b = r.body || { mode: 'none' };
  const hasCT = r.headers.some(x => x.key.toLowerCase() === 'content-type');
  const ctFor = { json: 'application/json', xml: 'application/xml', html: 'text/html', javascript: 'application/javascript', text: 'text/plain' };
  r.payload = null;
  if (b.mode === 'raw' && b.raw) { r.payload = b.raw; if (!hasCT) add('Content-Type', ctFor[b.lang] || 'text/plain'); }
  if (b.mode === 'graphql' && b.graphql) {
    let vars = {};
    try { vars = JSON.parse(b.graphql.variables || '{}'); } catch { /* keep {} */ }
    r.payload = JSON.stringify({ query: b.graphql.query, variables: vars });
    if (!hasCT) add('Content-Type', 'application/json');
  }
  if (b.mode === 'urlencoded') {
    r.payload = (b.form || []).filter(x => x.enabled && x.key).map(x => encodeURIComponent(x.key) + '=' + encodeURIComponent(x.value)).join('&');
    if (!hasCT) add('Content-Type', 'application/x-www-form-urlencoded');
  }
  r.form = b.mode === 'formdata' ? (b.form || []).filter(x => x.enabled && x.key) : null;
  r.file = b.mode === 'binary' ? b.file : null;
  r.method = (r.method || 'GET').toUpperCase();
  return r;
}

const sq = s => "'" + String(s).replace(/'/g, "'\\''") + "'";
const dq = s => JSON.stringify(String(s));

const CODEGEN = {
  'cURL': r => {
    let s = `curl --location --request ${r.method} ${sq(r.url)}`;
    r.headers.forEach(x => s += ` \\\n--header ${sq(x.key + ': ' + x.value)}`);
    if (r.payload != null) s += ` \\\n--data-raw ${sq(r.payload)}`;
    if (r.form) r.form.forEach(x => s += ` \\\n--form ${sq(x.key + '=' + (x.type === 'file' ? '@"' + x.value + '"' : x.value))}`);
    if (r.file) s += ` \\\n--data-binary ${sq('@' + r.file)}`;
    return s;
  },
  'HTTPie': r => {
    let s = `http --follow ${r.method} ${sq(r.url)}`;
    r.headers.forEach(x => s += ` \\\n  ${sq(x.key + ':' + x.value)}`);
    if (r.form) { s = s.replace('http ', 'http --multipart '); r.form.forEach(x => s += ` \\\n  ${sq(x.key + (x.type === 'file' ? '@' : '=') + x.value)}`); }
    if (r.payload != null) s = `printf %s ${sq(r.payload)} | ` + s;
    return s;
  },
  'wget': r => {
    let s = `wget --no-check-certificate --quiet \\\n  --method ${r.method} \\\n  --timeout=0`;
    r.headers.forEach(x => s += ` \\\n  --header ${sq(x.key + ': ' + x.value)}`);
    if (r.payload != null) s += ` \\\n  --body-data ${sq(r.payload)}`;
    return s + ` \\\n  ${sq(r.url)} -O -`;
  },
  'JavaScript – Fetch': r => {
    const lines = [];
    lines.push('const headers = new Headers();');
    r.headers.forEach(x => lines.push(`headers.append(${dq(x.key)}, ${dq(x.value)});`));
    let body = '';
    if (r.form) {
      lines.push('', 'const body = new FormData();');
      r.form.forEach(x => lines.push(x.type === 'file' ? `body.append(${dq(x.key)}, fileInput.files[0], ${dq(x.value.split('/').pop())});` : `body.append(${dq(x.key)}, ${dq(x.value)});`));
      body = '\n  body,';
    } else if (r.payload != null) {
      lines.push('', `const body = ${dq(r.payload)};`);
      body = '\n  body,';
    }
    lines.push('', `const response = await fetch(${dq(r.url)}, {\n  method: ${dq(r.method)},\n  headers,${body}\n  redirect: "follow"\n});`, 'console.log(await response.text());');
    return lines.join('\n');
  },
  'NodeJS – Axios': r => {
    const hs = r.headers.length ? '{\n' + r.headers.map(x => `    ${dq(x.key)}: ${dq(x.value)}`).join(',\n') + '\n  }' : '{}';
    let pre = "const axios = require('axios');\n";
    let data = '';
    if (r.form) {
      pre += "const FormData = require('form-data');\nconst fs = require('fs');\n\nconst data = new FormData();\n" + r.form.map(x => x.type === 'file' ? `data.append(${dq(x.key)}, fs.createReadStream(${dq(x.value)}));` : `data.append(${dq(x.key)}, ${dq(x.value)});`).join('\n') + '\n';
      data = ',\n  data';
    } else if (r.payload != null) { pre += `\nconst data = ${dq(r.payload)};\n`; data = ',\n  data'; }
    return `${pre}\nconst config = {\n  method: ${dq(r.method.toLowerCase())},\n  maxBodyLength: Infinity,\n  url: ${dq(r.url)},\n  headers: ${hs}${data}\n};\n\naxios.request(config)\n  .then(res => console.log(JSON.stringify(res.data)))\n  .catch(err => console.log(err));`;
  },
  'Python – Requests': r => {
    const hs = '{\n' + r.headers.map(x => `  ${dq(x.key)}: ${dq(x.value)}`).join(',\n') + '\n}';
    let s = `import requests\n\nurl = ${dq(r.url)}\n\n`;
    let extra = '';
    if (r.form) {
      const fields = r.form.filter(x => x.type !== 'file'), files = r.form.filter(x => x.type === 'file');
      s += `payload = {${fields.map(x => `${dq(x.key)}: ${dq(x.value)}`).join(', ')}}\n`;
      s += `files = [\n${files.map(x => `  (${dq(x.key)}, (${dq(x.value.split('/').pop())}, open(${dq(x.value)}, 'rb')))`).join(',\n')}\n]\n`;
      extra = ', data=payload, files=files';
    } else if (r.payload != null) { s += `payload = ${dq(r.payload)}\n`; extra = ', data=payload'; }
    s += `headers = ${r.headers.length ? hs : '{}'}\n\nresponse = requests.request(${dq(r.method)}, url, headers=headers${extra})\n\nprint(response.text)`;
    return s;
  },
  'Go – net/http': r => {
    let s = 'package main\n\nimport (\n  "fmt"\n  "io"\n  "net/http"\n' + (r.payload != null ? '  "strings"\n' : '') + ')\n\nfunc main() {\n';
    s += `  url := ${dq(r.url)}\n`;
    if (r.payload != null) s += `  payload := strings.NewReader(${'`' + r.payload.replace(/`/g, '` + "`" + `') + '`'})\n`;
    s += `  req, err := http.NewRequest(${dq(r.method)}, url, ${r.payload != null ? 'payload' : 'nil'})\n  if err != nil {\n    panic(err)\n  }\n`;
    r.headers.forEach(x => s += `  req.Header.Add(${dq(x.key)}, ${dq(x.value)})\n`);
    s += '\n  res, err := http.DefaultClient.Do(req)\n  if err != nil {\n    panic(err)\n  }\n  defer res.Body.Close()\n\n  body, _ := io.ReadAll(res.Body)\n  fmt.Println(string(body))\n}';
    return s;
  },
  'Java – OkHttp': r => {
    let s = 'OkHttpClient client = new OkHttpClient().newBuilder().build();\n';
    const ct = (r.headers.find(x => x.key.toLowerCase() === 'content-type') || {}).value || 'text/plain';
    if (r.payload != null) s += `MediaType mediaType = MediaType.parse(${dq(ct)});\nRequestBody body = RequestBody.create(mediaType, ${dq(r.payload)});\n`;
    s += `Request request = new Request.Builder()\n  .url(${dq(r.url)})\n  .method(${dq(r.method)}, ${r.payload != null ? 'body' : 'null'})\n`;
    r.headers.forEach(x => s += `  .addHeader(${dq(x.key)}, ${dq(x.value)})\n`);
    return s + '  .build();\nResponse response = client.newCall(request).execute();';
  },
  'C# – HttpClient': r => {
    let s = `var client = new HttpClient();\nvar request = new HttpRequestMessage(HttpMethod.${r.method[0] + r.method.slice(1).toLowerCase()}, ${dq(r.url)});\n`;
    r.headers.filter(x => x.key.toLowerCase() !== 'content-type').forEach(x => s += `request.Headers.Add(${dq(x.key)}, ${dq(x.value)});\n`);
    if (r.payload != null) {
      const ct = (r.headers.find(x => x.key.toLowerCase() === 'content-type') || {}).value || 'text/plain';
      s += `var content = new StringContent(${dq(r.payload)}, null, ${dq(ct.split(';')[0])});\nrequest.Content = content;\n`;
    }
    return s + 'var response = await client.SendAsync(request);\nresponse.EnsureSuccessStatusCode();\nConsole.WriteLine(await response.Content.ReadAsStringAsync());';
  },
  'PHP – cURL': r => {
    let s = `<?php\n$curl = curl_init();\ncurl_setopt_array($curl, array(\n  CURLOPT_URL => ${dq(r.url)},\n  CURLOPT_RETURNTRANSFER => true,\n  CURLOPT_FOLLOWLOCATION => true,\n  CURLOPT_CUSTOMREQUEST => ${dq(r.method)},\n`;
    if (r.payload != null) s += `  CURLOPT_POSTFIELDS => ${dq(r.payload)},\n`;
    if (r.headers.length) s += `  CURLOPT_HTTPHEADER => array(\n${r.headers.map(x => `    ${dq(x.key + ': ' + x.value)}`).join(',\n')}\n  ),\n`;
    return s + '));\n$response = curl_exec($curl);\ncurl_close($curl);\necho $response;';
  },
  'Ruby – Net::HTTP': r => {
    const cls = r.method[0] + r.method.slice(1).toLowerCase();
    let s = `require "uri"\nrequire "net/http"\n\nurl = URI(${dq(r.url)})\nhttp = Net::HTTP.new(url.host, url.port)\nhttp.use_ssl = url.scheme == "https"\nrequest = Net::HTTP::${cls}.new(url)\n`;
    r.headers.forEach(x => s += `request[${dq(x.key)}] = ${dq(x.value)}\n`);
    if (r.payload != null) s += `request.body = ${dq(r.payload)}\n`;
    return s + 'response = http.request(request)\nputs response.read_body';
  }
};
