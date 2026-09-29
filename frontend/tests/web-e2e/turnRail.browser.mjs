import assert from 'node:assert/strict';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { chromium, expect } from '@playwright/test';
import { createServer } from 'vite';

const artifacts = await mkdtemp(join(tmpdir(), 'synon-turn-rail-'));
let count = 1;
let windowed = false;
let vite, browser, page;
const luminance = (color) => {
  const channels = color
    .match(/[\d.]+/g)
    .slice(0, 3)
    .map((value) => Number(value) / 255);
  const linear = channels.map((value) => (value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4));
  return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
};
const checkPreviewContrast = async (preview) => {
  const colors = await preview.evaluate((element) => ({
    background: getComputedStyle(element).backgroundColor,
    text: [...element.querySelectorAll('p')].map((paragraph) => getComputedStyle(paragraph).color),
  }));
  const background = luminance(colors.background);
  for (const color of colors.text) {
    const text = luminance(color);
    const contrast = (Math.max(background, text) + 0.05) / (Math.min(background, text) + 0.05);
    assert.ok(contrast >= 4.5, JSON.stringify({ ...colors, contrast }));
  }
};
try {
  vite = await createServer({
    configFile: resolve('vite.config.ts'),
    server: { host: '127.0.0.1', port: 0, hmr: false, fs: { allow: [process.cwd()] } },
    plugins: [
      {
        name: 'turn-rail-browser-fixture',
        configureServer(server) {
          server.middlewares.use('/__turn_rail_data__', (_request, response) => {
            response.setHeader('content-type', 'application/json');
            response.end(JSON.stringify({ count, windowed }));
          });
          server.middlewares.use('/__turn_rail_test__', async (_request, response, next) => {
            try {
              const html = await server.transformIndexHtml(
                '/__turn_rail_test__',
                `<html lang="zh-CN" data-theme="light" data-color-scheme="default"><head><title>Turn rail regression</title></head><body arco-theme="light"><div id="root"></div><script type="module" src="/@fs/${resolve('tests/web-e2e/turnRail.fixture.tsx')}"></script></body></html>`
              );
              response.setHeader('content-type', 'text/html');
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
  browser = await chromium.launch({ headless: true });
  page = await browser.newPage({ viewport: { width: 1000, height: 800 } });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(`http://127.0.0.1:${vite.httpServer.address().port}/__turn_rail_test__`);
  const rail = page.getByTestId('conversation-turn-rail');
  await expect(rail).toBeVisible();
  await expect(rail.getByRole('button')).toHaveCount(1);
  count = 100;
  await page.reload();
  await expect(rail).toBeVisible();
  await expect(page.locator('[data-source-message-id="q99"]')).toBeInViewport();
  assert.ok((await rail.getByRole('button').count()) < 70);
  await expect(rail.locator('[aria-setsize="100"]')).toHaveCount(await rail.getByRole('button').count());
  const first = rail.locator('[data-turn-index="0"]');
  await first.focus();
  await first.hover();
  const preview = page.getByTestId('conversation-turn-preview');
  await expect(preview).toHaveAttribute('data-open', 'true');
  await expect(preview).toContainText('Question 1');
  await expect(preview).toContainText('Answer 1');
  await checkPreviewContrast(preview);
  const rect = await preview.boundingBox();
  assert.ok(rect && Math.round(rect.width) === 256 && Math.round(rect.height) === 88, JSON.stringify(rect));
  await page.screenshot({ path: join(artifacts, 'hover.png'), animations: 'disabled' });
  await page.mouse.move(900, 700);
  await expect(preview).toHaveAttribute('data-open', 'true');
  await page.getByTestId('before-navigation').focus();
  await expect(preview).toHaveAttribute('data-open', 'false');
  await first.focus();
  for (let index = 1; index < 100; index++) {
    await page.keyboard.press('Tab');
    await expect(rail.locator(`[data-turn-index="${index}"]`)).toBeFocused();
  }
  await expect(preview).toContainText('Question 100');
  assert.ok((await rail.getByRole('button').count()) < 70);
  await page.keyboard.press('Shift+Tab');
  await expect(rail.locator('[data-turn-index="98"]')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-source-message-id="q98"]')).toBeInViewport();
  await expect
    .poll(async () => {
      const target = await page.locator('[data-source-message-id="q98"]').boundingBox();
      const viewport = await page.locator('[data-virtuoso-scroller]').boundingBox();
      return Math.abs(target.y - viewport.y);
    })
    .toBeLessThanOrEqual(32);
  await expect(preview).toHaveAttribute('data-open', 'false');
  await rail.locator('[data-turn-index="99"]').focus();
  await page.keyboard.press('Escape');
  await expect(preview).toHaveAttribute('data-open', 'false');
  await page.locator('html').evaluate((html) => {
    html.setAttribute('data-theme', 'dark');
    html.querySelector('body').setAttribute('arco-theme', 'dark');
  });
  await rail.locator('[data-turn-index="99"]').hover();
  await checkPreviewContrast(preview);
  await page.screenshot({ path: join(artifacts, 'dark.png'), animations: 'disabled' });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(preview).toHaveCSS('transition-duration', '0s');
  await page.setViewportSize({ width: 375, height: 760 });
  await expect(rail).toBeHidden();
  await page.setViewportSize({ width: 1000, height: 800 });
  count = 1000;
  await page.reload();
  await expect(rail).toBeVisible();
  assert.ok((await rail.getByRole('button').count()) < 70);
  await rail.locator('[data-turn-index="999"]').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-source-message-id="q999"]')).toBeInViewport();
  windowed = true;
  await page.reload();
  await expect(rail).toBeVisible();
  await rail.locator('[data-turn-index="999"]').focus();
  for (let step = 0; step < 5; step++) await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Enter');
  await page.waitForTimeout(1800);
  await expect(page.locator('[data-source-message-id="q994"]')).toBeInViewport();
  await expect
    .poll(async () => {
      const target = await page.locator('[data-source-message-id="q994"]').boundingBox();
      const viewport = await page.locator('[data-virtuoso-scroller]').boundingBox();
      return Math.abs(target.y - viewport.y);
    })
    .toBeLessThanOrEqual(32);
  await expect(page.getByTestId('automatic-after-loads')).toHaveText('0');
  await rail.locator('[data-turn-index="0"]').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-source-message-id="q0"]')).toBeInViewport();
  await expect(page.getByTestId('automatic-after-loads')).toHaveText('0');
  const viewport = page.locator('[data-virtuoso-scroller]');
  await viewport.hover();
  await page.mouse.wheel(0, 4000);
  await expect(page.getByTestId('automatic-after-loads')).not.toHaveText('0');
  // Let the real controller's resize/animation-frame callbacks settle; one
  // reading gesture must not attach to an intermediate page and consume all pages.
  await page.waitForTimeout(500);
  await expect(page.getByTestId('automatic-after-loads')).toHaveText('1');
  const requestedLoads = await page.getByTestId('automatic-after-loads').innerText();
  await rail.locator('[data-turn-index="999"]').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-source-message-id="q999"]')).toBeInViewport();
  await expect(page.getByTestId('automatic-after-loads')).toHaveText(requestedLoads);
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      passed: true,
      browser: await browser.version(),
      artifacts,
      checks: [
        'one turn',
        '100 turns keyboard forward/backward',
        '1000 turns bounded DOM',
        'real Virtuoso jump',
        'disjoint cursor-window jump without pagination cascade',
        'late report hydration retains the requested anchor',
        'message margins participate in precise row measurement',
        'manual scrolling still loads the next page',
        'hover/focus preview',
        'delayed close',
        'Escape',
        'dark',
        'production light/dark palette text contrast',
        'narrow',
        'reduced motion',
        'console',
      ],
    })
  );
} catch (error) {
  if (page) {
    console.log(
      await page.evaluate(() => ({
        active: document.activeElement?.outerHTML.slice(0, 500),
        rows: [...document.querySelectorAll('[data-source-message-id]')].map((el) => ({
          id: el.getAttribute('data-source-message-id'),
          top: el.getBoundingClientRect().top,
        })),
        scrollers: [...document.querySelectorAll('[data-virtuoso-scroller]')].map((el) => ({
          top: el.scrollTop,
          height: el.scrollHeight,
        })),
      }))
    );
    await page.screenshot({ path: join(artifacts, 'failure.png') });
    console.log(artifacts);
  }
  throw error;
} finally {
  if (browser) await browser.close();
  if (vite) await vite.close();
}
