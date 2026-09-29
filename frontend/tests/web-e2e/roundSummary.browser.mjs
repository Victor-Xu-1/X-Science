import assert from 'node:assert/strict';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'vite';
import { chromium, expect } from '@playwright/test';

const artifacts = await mkdtemp(join(tmpdir(), 'synon-round-summary-'));
const full = {
  attempt: 1,
  input_revision: 1,
  completed_at: 1790610800000,
  elapsed_ms: 10000,
  call_count: 2,
  reported_call_count: 2,
  usage_state: 'complete',
  tokens: {
    input: 1251,
    cache_read: 52024,
    cache_write: 0,
    output: 377,
    total: 53652,
  },
  models: ['browser-fixture-model'],
};
let summary = full;
let vite;
let browser;
try {
  vite = await createServer({
    configFile: resolve('vite.config.ts'),
    server: {
      host: '127.0.0.1',
      port: 0,
      hmr: false,
      fs: { allow: [process.cwd()] },
    },
    plugins: [
      {
        name: 'round-summary-browser-page',
        configureServer(server) {
          server.middlewares.use('/__round_summary_fixture_data__', (_request, response) => {
            response.setHeader('content-type', 'application/json');
            response.end(JSON.stringify(summary));
          });
          server.middlewares.use('/__round_summary_test__', async (_request, response, next) => {
            try {
              const html = await server.transformIndexHtml(
                '/__round_summary_test__',
                `<html lang="zh-CN" data-theme="light" data-color-scheme="default"><head><title>Round summary test</title></head><body arco-theme="light"><div id="root"></div><script type="module" src="/@fs/${resolve(
                  'tests/web-e2e/roundSummary.fixture.tsx'
                )}"></script></body></html>`
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
  await vite.listen();
  const address = vite.httpServer.address();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 900, height: 650 } });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(`http://127.0.0.1:${address.port}/__round_summary_test__`);
  const trigger = page.getByRole('button', { name: '调用', exact: true });
  const panel = page.getByTestId('round-usage-panel');
  const before = await page.getByTestId('message-round-footer').innerText();
  const assertAlignment = async () => {
    const layout = await page.getByTestId('footer-layout').evaluate((element) => {
      const controls = [...element.querySelectorAll('button')].map((button) => {
        const box = button.getBoundingClientRect();
        const icon = button.querySelector('svg').getBoundingClientRect();
        return {
          height: box.height,
          iconWidth: icon.width,
          iconHeight: icon.height,
          centerDelta: Math.abs(icon.y + icon.height / 2 - box.y - box.height / 2),
        };
      });
      const bounds = element.getBoundingClientRect();
      return {
        controls,
        overflows: element.scrollWidth > element.clientWidth,
        right: bounds.right,
        viewport: innerWidth,
      };
    });
    for (const control of layout.controls) {
      assert.equal(control.height, 28, JSON.stringify(layout));
      assert.equal(control.iconWidth, 16, JSON.stringify(layout));
      assert.equal(control.iconHeight, 16, JSON.stringify(layout));
      assert.ok(control.centerDelta <= 1, JSON.stringify(layout));
    }
    assert.equal(layout.overflows, false, JSON.stringify(layout));
    assert.ok(layout.right <= layout.viewport, JSON.stringify(layout));
  };
  await assertAlignment();
  await trigger.focus();
  await expect(trigger).toHaveCSS('border-bottom-width', '0px');
  await expect(trigger).toHaveCSS('text-decoration-line', 'none');
  await expect(trigger).toHaveCSS('outline-style', 'solid');
  await page.keyboard.press('Enter');
  await expect(panel).toBeVisible();
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getTiming().iterations !== Infinity)
        .map((animation) => animation.finished.catch(() => undefined))
    );
  });
  for (const text of [
    '2 次调用',
    '输入（含缓存）',
    '未缓存',
    '53,275',
    '1,251',
    '52,024',
    '377',
    '53,652',
    'browser-fixture-model',
  ])
    await expect(panel).toContainText(text);
  const composition = page.getByTestId('round-usage-bar');
  await expect(composition.locator(':scope > i')).toHaveCount(2);
  await expect(composition).toHaveAttribute('role', 'img');
  const proportions = await composition.evaluate((element) => {
    const width = element.getBoundingClientRect().width;
    return [...element.children].map((child) => ({
      key: child.getAttribute('data-category'),
      fraction: child.getBoundingClientRect().width / width,
    }));
  });
  assert.deepEqual(
    proportions.map((part) => part.key),
    ['input', 'output']
  );
  assert.ok(Math.abs(proportions[0].fraction - 53275 / 53652) < 0.001, JSON.stringify(proportions));
  await expect(page.getByTestId('round-input-breakdown')).toContainText('52,024');
  const surface = await panel.evaluate((element) => {
    const shell = element.closest('.arco-popover-content');
    if (!shell) throw new Error('Missing native popup shell');
    const inner = getComputedStyle(element);
    const outer = shell.getBoundingClientRect();
    const colors = [...element.querySelectorAll('dt i')].map((dot) => getComputedStyle(dot).backgroundColor);
    return {
      width: outer.width,
      height: outer.height,
      border: inner.borderTopWidth,
      shadow: inner.boxShadow,
      background: inner.backgroundColor,
      colors,
    };
  });
  assert.ok(surface.width <= 280 && surface.height <= 285, JSON.stringify(surface));
  assert.equal(surface.border, '0px', 'Only the native popup shell may have a border');
  assert.equal(surface.shadow, 'none', 'An inner card must not add another shadow');
  assert.equal(surface.background, 'rgba(0, 0, 0, 0)', 'The content must not paint a second card');
  for (const color of surface.colors) {
    const channels = color.match(/\d+/g).slice(0, 3).map(Number);
    assert.ok(Math.max(...channels) - Math.min(...channels) >= 140, `Muted category color: ${color}`);
  }
  await expect(panel).not.toContainText('智能体');
  const bounds = await panel.boundingBox();
  assert.ok(bounds && bounds.width <= 340 && bounds.x >= 0 && bounds.y >= 0, JSON.stringify(bounds));
  await page.screenshot({
    path: join(artifacts, 'desktop.png'),
    animations: 'disabled',
  });
  await page.keyboard.press('Escape');
  await expect(trigger).toHaveAttribute('aria-expanded', 'false');
  await page.reload();
  await expect(page.getByTestId('message-round-footer')).toHaveText(before, {
    useInnerText: true,
  });
  await page.setViewportSize({ width: 375, height: 760 });
  await assertAlignment();
  await trigger.click();
  await expect(panel).toBeVisible();
  const mobile = await panel.boundingBox();
  assert.ok(mobile && mobile.x >= 0 && mobile.x + mobile.width <= 375, JSON.stringify(mobile));
  await page.screenshot({
    path: join(artifacts, 'mobile.png'),
    animations: 'disabled',
  });
  await page.locator('html').evaluate((html) => {
    html.setAttribute('data-theme', 'dark');
    html.querySelector('body').setAttribute('arco-theme', 'dark');
  });
  await page.screenshot({
    path: join(artifacts, 'dark.png'),
    animations: 'disabled',
  });
  summary = { ...full, elapsed_ms: 90061000 };
  await page.setViewportSize({ width: 320, height: 760 });
  await page.reload();
  await expect(page.getByTestId('message-round-footer')).toContainText('25h 1m 1s');
  await assertAlignment();
  summary = {
    ...full,
    usage_state: 'unavailable',
    tokens: null,
    reported_call_count: 0,
    models: [],
  };
  await page.reload();
  await trigger.click();
  await expect(panel).toContainText('未记录');
  await expect(panel).not.toContainText('53,652');
  await expect(composition).toHaveCount(0);
  summary = { ...full, tokens: { ...full.tokens, total: 99999 } };
  await page.reload();
  await trigger.click();
  await expect(panel).toContainText('99,999');
  await expect(panel).toContainText('不一致');
  await expect(composition).toHaveCount(0);
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      passed: true,
      browser: await browser.version(),
      artifacts,
      checks: [
        'inclusive input parent',
        'nested cache details',
        'input/output proportions',
        'inconsistent totals',
        'keyboard',
        'Escape',
        'reload',
        'compact single surface',
        'desktop',
        'mobile',
        'dark',
        'unavailable',
        'console',
        'uniform action sizes and vertical alignment',
        'responsive action-row wrapping',
      ],
    })
  );
} finally {
  if (browser) await browser.close();
  if (vite) await vite.close();
}
