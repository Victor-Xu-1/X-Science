import assert from 'node:assert/strict';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'vite';
import { chromium, expect } from '@playwright/test';

const artifacts = await mkdtemp(join(tmpdir(), 'synon-ligand-topology-'));
let vite, browser, page;
const errors = [];
try {
  vite = await createServer({
    configFile: resolve('vite.config.ts'),
    optimizeDeps: { noDiscovery: true },
    server: {
      host: '127.0.0.1',
      port: 0,
      hmr: false,
      fs: { allow: [process.cwd()] },
    },
    plugins: [
      {
        name: 'ligand-topology-browser-page',
        configureServer(server) {
          server.middlewares.use('/__ligand_topology__', async (_request, response, next) => {
            try {
              const html = await server.transformIndexHtml(
                '/__ligand_topology__',
                `<html lang="zh-CN"><body><div id="root"></div><script type="module" src="/@fs/${resolve(
                  'tests/web-e2e/ligandTopology.fixture.tsx'
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
  browser = await chromium.launch({
    headless: true,
    executablePath: chromium.executablePath(),
  });
  page = await browser.newPage({
    viewport: { width: 1000, height: 950 },
  });
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(`http://127.0.0.1:${address.port}/__ligand_topology__`, {
    waitUntil: 'domcontentloaded',
  });
  const depiction = page.locator('.synon-biomed-molstar__ligand-depiction');
  const svg = depiction.locator('.synon-biomed-molstar__ligand-depiction-svg svg');
  const bondPathCounts = (element) => {
    const counts = new Map();
    for (const path of element.querySelectorAll('path[class]')) {
      const bond = path.getAttribute('class')?.match(/\bbond-(\d+)\b/)?.[1];
      if (bond) counts.set(bond, (counts.get(bond) ?? 0) + 1);
    }
    return [...counts.values()].sort((a, b) => a - b);
  };
  await page.getByRole('button', { name: 'sdf', exact: true }).waitFor({ timeout: 60000 });
  const expected = await page.getByTestId('reference-svg').evaluate(bondPathCounts);
  assert.equal(expected.length, 8);
  await expect(page.getByTestId('chemistry-validation')).toHaveText('[true,true,true,true]');
  assert.ok(expected.filter((value) => value > 1).length >= 4, JSON.stringify(expected));
  for (const format of ['sdf', 'mol', 'mol2', 'cif', 'pdbqt']) {
    await page.getByRole('button', { name: format, exact: true }).click();
    await expect(depiction).toBeVisible({ timeout: 45000 });
    const expand = depiction.getByRole('button', {
      name: '向上展开二维结构',
      exact: true,
    });
    if (await expand.isVisible()) await expand.click();
    await expect(svg).toBeVisible({ timeout: 45000 });
    await expect.poll(() => svg.evaluate(bondPathCounts), { timeout: 15000 }).toEqual(expected);
    await page.screenshot({
      path: join(artifacts, format + '-bond-orders.png'),
    });
  }
  await expect(page.getByTestId('synon-biomed-molstar-model-navigator')).toContainText('模型 1 / 9');
  const nextModel = page.getByRole('button', { name: '下一个模型', exact: true });
  if (!(await nextModel.isVisible())) await page.getByTestId('synon-biomed-molstar-toolbar-collapse').click();
  await nextModel.click();
  await expect(page.getByTestId('synon-biomed-molstar-model-navigator')).toContainText('模型 2 / 9');
  await expect(svg).toBeVisible();
  assert.deepEqual(await svg.evaluate(bondPathCounts), expected);
  await page.getByRole('button', { name: 'pdb', exact: true }).click();
  await expect(depiction).toBeVisible();
  const expandUnknown = depiction.getByRole('button', { name: '向上展开二维结构', exact: true });
  if (await expandUnknown.isVisible()) await expandUnknown.click();
  await expect(depiction).toContainText('文件缺少可核实的键级信息', {
    timeout: 30000,
  });
  await expect(svg).toHaveCount(0);
  await expect(page.getByTestId('synon-biomed-structure-canvas').locator('canvas').first()).toBeVisible();
  await page.screenshot({
    path: join(artifacts, 'unknown-coordinate-topology.png'),
  });
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      passed: true,
      formats: ['sdf', 'mol', 'mol2', 'cif', 'pdbqt'],
      bondPathCounts: expected,
      modelCount: 9,
      pageErrors: errors,
      artifacts,
    })
  );
} catch (error) {
  if (page) {
    await page.screenshot({ path: join(artifacts, 'failed.png') });
    console.error(
      JSON.stringify({ artifacts, pageErrors: errors, visibleText: await page.locator('body').innerText() })
    );
  }
  throw error;
} finally {
  await browser?.close();
  await vite?.close();
}
