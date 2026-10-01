import assert from 'node:assert/strict';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'vite';
import { chromium, expect } from '@playwright/test';

const api = process.env.SYNON_FRAME_ATTENTION_API;
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(api)) throw new Error('Controlled local API required');
const artifacts = await mkdtemp(join(tmpdir(), 'synon-task-attention-'));
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
        name: 'task-attention-regression',
        configureServer(vite) {
          vite.middlewares.use('/__task_attention__', async (_request, response, next) => {
            try {
              const html = await vite.transformIndexHtml(
                '/__task_attention__',
                `<html lang="zh-CN" data-theme="light"><body arco-theme="light"><div id="root"></div><script type="module" src="/@fs/${resolve('tests/web-e2e/taskAttention.fixture.tsx')}"></script></body></html>`
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
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const url = `http://127.0.0.1:${server.httpServer.address().port}/__task_attention__`;
  await page.goto(`${url}?frame=plan-review`);
  const entry = page.getByRole('button', { name: '查看计划并审批', exact: true });
  await expect(entry).toBeVisible({ timeout: 20_000 });
  await page.screenshot({ path: join(artifacts, 'plan-entry.png'), fullPage: true });
  await entry.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText('Controlled task plan', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '批准并执行', exact: true })).toBeVisible();
  // Observe the real five-second heartbeat; status refresh must not dismiss review.
  await expect
    .poll(async () => page.getByRole('button', { name: '批准并执行', exact: true }).isVisible(), { timeout: 7000 })
    .toBe(true);
  await page.waitForTimeout(5500);
  await expect(page.getByRole('button', { name: '批准并执行', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '批准并执行', exact: true }).click();
  await expect(page.getByTestId('synon-biomed-plan-attention')).toHaveCount(0);
  await page.goto(`${url}?frame=provider-paused`);
  await expect(page.getByTestId('synon-biomed-runtime-status')).toHaveAttribute('data-state', 'paused');
  await expect(page.getByTestId('synon-biomed-paused-attention')).toContainText('本次模型输出达到上限');
  await expect(page.getByRole('button', { name: '继续任务', exact: true })).toBeVisible();
  await expect(page.getByTestId('synon-biomed-plan-attention')).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId('synon-biomed-paused-attention')).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await expect(page.getByRole('button', { name: '继续任务', exact: true })).toBeVisible();
  assert.ok(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    'narrow viewport overflow'
  );
  await page.screenshot({ path: join(artifacts, 'paused-mobile.png'), fullPage: true });
  const resumed = page.waitForResponse(
    (response) => response.request().method() === 'POST' && response.url().endsWith('/provider-paused/resume')
  );
  await page.getByRole('button', { name: '继续任务', exact: true }).click();
  assert.equal((await resumed).status(), 200, 'pause recovery was not accepted by the real API');
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: 'passed',
      browser: await browser.version(),
      artifacts,
      checks: [
        'real Go HTTP/SQLite',
        'direct plan entry and keyboard',
        'review survives refresh',
        'one durable approval',
        'provider pause not false approval',
        'existing resume API accepts the paused task',
        'reload/navigation',
        'narrow viewport',
        'no page errors',
      ],
    })
  );
} finally {
  await browser?.close();
  await server?.close();
}
