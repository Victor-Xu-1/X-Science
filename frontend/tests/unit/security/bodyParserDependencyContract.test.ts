import { spawn } from 'node:child_process';
import { readFileSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const packagePath = path.join(frontend, 'package.json');
const projectRequire = createRequire(packagePath);
const approved = { 'body-parser': '2.4.0', qs: '6.16.1' } as const;
const readJSON = (filename: string) => JSON.parse(readFileSync(filename, 'utf8'));

const isolatedExpress = String.raw`
const assert = require('node:assert/strict');
const http = require('node:http');
const { createRequire } = require('node:module');
const { gzipSync } = require('node:zlib');
const projectRequire = createRequire(process.argv[1]);
const express = projectRequire('express');
const bodyParser = projectRequire('body-parser');
const app = express();
app.use(bodyParser.json({ limit: '16kb' }));
app.use(bodyParser.urlencoded({ extended: true, limit: '16kb' }));
app.post('/parse', (req, res) => res.json(req.body));
app.get('/health', (_req, res) => res.json({ alive: true }));
app.use((error, _req, res, _next) => res.status(error.status || 500).json({ type: error.type }));
const server = http.createServer(app);
const results = [];

function request(port, payload, headers = {}, method = 'POST', route = '/parse') {
  return new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port, path: route, method,
      headers: { connection: 'close', 'content-length': payload.length, ...headers } }, res => {
      let text = '';
      res.setEncoding('utf8');
      res.on('data', part => {
        text += part;
        if (text.length > 32768) res.destroy(new Error('response exceeds fixture bound'));
      });
      res.on('error', reject);
      res.on('end', () => resolve({ status: res.statusCode, body: JSON.parse(text) }));
    });
    req.on('error', reject);
    req.setTimeout(1200, () => req.destroy(new Error('request deadline')));
    req.end(payload);
  });
}

(async () => {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  try {
    const port = server.address().port;
    assert.ok(port !== 8765 && port !== 8766);
    const json = await request(port, Buffer.from('{"value":7}'), { 'content-type': 'application/json' });
    assert.equal(json.status, 200);
    assert.deepEqual(json.body, { value: 7 });
    results.push('json');
    const form = await request(port, Buffer.from('name=sample&nested[value]=7&values[]=a&values[]=b'),
      { 'content-type': 'application/x-www-form-urlencoded' });
    assert.equal(form.status, 200);
    assert.deepEqual(form.body, { name: 'sample', nested: { value: '7' }, values: ['a', 'b'] });
    results.push('urlencoded');
    const compressed = gzipSync(Buffer.from('{"compressed":true}'));
    const headers = { 'content-type': 'application/json', 'content-encoding': 'gzip' };
    const gzip = await request(port, compressed, headers);
    assert.equal(gzip.status, 200);
    assert.deepEqual(gzip.body, { compressed: true });
    results.push('gzip');
    const malformed = await request(port, Buffer.from('invalid-gzip'), headers);
    assert.equal(malformed.status, 400);
    results.push('malformed-rejected');
    // This tests bounded malformed-input survival, not deployed CVE reachability.
    const trailing = await request(port, Buffer.concat([compressed, Buffer.from('trailing-bytes')]), headers);
    assert.ok(trailing.status === 200 || trailing.status === 400,
      'trailing bytes must not cause an internal-server response');
    const health = await request(port, Buffer.alloc(0), {}, 'GET', '/health');
    assert.equal(health.status, 200);
    assert.deepEqual(health.body, { alive: true });
    results.push('survives-trailing-bytes');
    const next = await request(port, Buffer.from('{"next":true}'), { 'content-type': 'application/json' });
    assert.equal(next.status, 200);
    assert.deepEqual(next.body, { next: true });
    results.push('next-request');
    process.stdout.write(JSON.stringify(results));
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
`;

function runIsolatedExpress(): Promise<string[]> {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['--input-type=commonjs', '-e', isolatedExpress, packagePath], {
      cwd: frontend,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    let failure: Error | undefined;
    const stop = (error: Error) => {
      failure ??= error;
      child.kill('SIGKILL');
    };
    const timeout = setTimeout(() => stop(new Error('isolated Express deadline exceeded')), 6000);
    child.stdout.setEncoding('utf8').on('data', (part: string) => {
      stdout += part;
      if (stdout.length > 32768) stop(new Error('isolated stdout exceeds bound'));
    });
    child.stderr.setEncoding('utf8').on('data', (part: string) => {
      stderr += part;
      if (stderr.length > 32768) stop(new Error('isolated stderr exceeds bound'));
    });
    child.once('error', (error) => {
      failure ??= error;
    });
    child.once('close', (code, signal) => {
      clearTimeout(timeout);
      if (failure) return reject(failure);
      if (code !== 0) return reject(new Error(`isolated Express exited ${code}/${signal}: ${stderr}`));
      try {
        resolve(JSON.parse(stdout));
      } catch (error) {
        reject(error);
      }
    });
  });
}

describe('body-parser dependency contract', () => {
  it('keeps approved overrides and every corresponding lock entry consistent', () => {
    const manifest = readJSON(packagePath);
    const lock = readJSON(path.join(frontend, 'package-lock.json'));
    expect(lock.lockfileVersion).toBe(3);
    for (const [name, version] of Object.entries(approved)) {
      expect(manifest.overrides[name]).toBe(version);
      const entries = Object.entries(lock.packages).filter(([key]) => key.endsWith(`node_modules/${name}`));
      expect(entries.length).toBeGreaterThan(0);
      for (const [, entry] of entries) expect((entry as { version: string }).version).toBe(version);
    }
  });

  it('loads the patched packages from this candidate and through the actual Express resolver', () => {
    const localModules = realpathSync(path.join(frontend, 'node_modules'));
    expect(localModules).toBe(path.join(realpathSync(frontend), 'node_modules'));
    const expressRequire = createRequire(projectRequire.resolve('express/package.json'));
    const bodyPath = expressRequire.resolve('body-parser/package.json');
    const bodyRequire = createRequire(bodyPath);
    const qsPath = bodyRequire.resolve('qs/package.json');
    for (const filename of [bodyPath, qsPath]) {
      expect(realpathSync(filename).startsWith(localModules + path.sep)).toBe(true);
    }
    expect(readJSON(bodyPath).version).toBe(approved['body-parser']);
    expect(readJSON(qsPath).version).toBe(approved.qs);
    expect(realpathSync(projectRequire.resolve('body-parser/package.json'))).toBe(realpathSync(bodyPath));
  });

  it('parses JSON, forms and gzip, rejects malformed input and serves the next request', async () => {
    await expect(runIsolatedExpress()).resolves.toEqual([
      'json',
      'urlencoded',
      'gzip',
      'malformed-rejected',
      'survives-trailing-bytes',
      'next-request',
    ]);
  }, 8000);
});
