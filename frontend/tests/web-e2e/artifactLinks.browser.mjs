import assert from 'node:assert/strict';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'vite';
import { chromium, expect } from '@playwright/test';

const api = process.env.SYNON_ARTIFACT_LINK_API;
const fixture = JSON.parse(process.env.SYNON_ARTIFACT_LINK_FIXTURE);
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(api)) throw new Error('Controlled local API required');
const artifacts = await mkdtemp(join(tmpdir(), 'synon-artifact-links-'));
let server, browser;
try {
  server = await createServer({
    configFile: resolve('vite.config.ts'),
    server: {
      host: '127.0.0.1',
      port: 0,
      hmr: false,
      proxy: { '/api': { target: api } },
      fs: { allow: [process.cwd()] },
    },
    plugins: [
      {
        name: 'artifact-link-regression',
        configureServer(vite) {
          vite.middlewares.use('/__artifact_fixture__', (_request, response) => {
            response.setHeader('content-type', 'application/json');
            response.end(JSON.stringify(fixture));
          });
          vite.middlewares.use('/__artifact_links__', async (_request, response, next) => {
            try {
              const html = await vite.transformIndexHtml(
                '/__artifact_links__',
                `<html lang="zh-CN" data-theme="light"><body arco-theme="light"><div id="root"></div><script type="module" src="/@fs/${resolve('tests/web-e2e/artifactLinks.fixture.tsx')}"></script></body></html>`
              );
              response.setHeader('content-type', 'text/html; charset=utf-8');
              response.end(html);
            } catch (error) {
              next(error);
            }
          });
        },
      },
    ],
  });
  await server.listen();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1200, height: 850 } });
  const errors = [],
    requests = [],
    downloads = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('pageerror', (error) => console.error('Browser error:', error.stack));
  page.on('response', (response) => {
    if (response.status() >= 400) console.error('HTTP failure:', response.status(), response.url());
  });
  page.on('request', (request) => requests.push(request.url()));
  page.on('download', (download) => downloads.push(download.suggestedFilename()));
  const url = `http://127.0.0.1:${server.httpServer.address().port}/__artifact_links__#/conversation/${fixture.conversation}`;
  await page.goto(url);
  const latest = page.getByRole('link', { name: 'Latest report', exact: true });
  await expect(latest).toHaveAttribute('data-artifact-link-resolution', 'resolved', { timeout: 20_000 });
  const style = await latest.evaluate((element) => ({
    color: getComputedStyle(element).color,
    weight: getComputedStyle(element).fontWeight,
  }));
  const rgb = style.color.match(/\d+/g).map(Number);
  assert.ok(rgb[2] > rgb[0] && Number(style.weight) >= 600, JSON.stringify(style));
  const pageCount = page.context().pages().length;
  await latest.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('preview-identity')).toContainText(fixture.latest.version_id);
  await expect(page.getByText('controlled branch fixture new-version', { exact: true })).toBeVisible();
  await page.screenshot({ path: join(artifacts, 'native-preview.png'), fullPage: true });
  await page.getByRole('button', { name: 'Close test preview' }).click();
  await page.getByRole('link', { name: 'Exact old report', exact: true }).click();
  await expect(page.getByTestId('preview-identity')).toContainText(fixture.old.version_id);
  await expect(page.getByText('controlled branch fixture old-version', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close test preview' }).click();
  await page.getByRole('link', { name: 'Unknown artifact', exact: true }).click();
  await expect(page.getByTestId('preview-identity')).toHaveText('closed');
  await expect(page.getByText('文件预览失败', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Unknown artifact', exact: true })).toHaveAttribute(
    'data-artifact-link-resolution',
    'unavailable'
  );
  assert.equal(page.url(), url);
  assert.equal(page.context().pages().length, pageCount);
  assert.deepEqual(downloads, []);
  assert.deepEqual(errors, []);
  assert.ok(
    requests.every((value) => new URL(value).origin === new URL(url).origin),
    'model origin was fetched'
  );
  await page.reload();
  await expect(latest).toHaveAttribute('data-artifact-link-resolution', 'resolved');
  console.log(
    JSON.stringify({
      result: 'passed',
      browser: await browser.version(),
      artifacts,
      checks: [
        'production renderer/resolver/native preview',
        'real HTTP and SQLite',
        'blue bold links',
        'keyboard',
        'immutable old version',
        'unknown identity contained',
        'no new tab/download/origin fetch',
        'reload',
      ],
    })
  );
} finally {
  await browser?.close();
  await server?.close();
}
