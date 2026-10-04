import assert from 'node:assert/strict';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { build, preview as previewBuild } from 'vite';
import { chromium, expect } from '@playwright/test';

const artifacts = await mkdtemp(join(tmpdir(), 'synon-ligand-topology-'));
let vite, browser, page;
const errors = [];
const diagnostics = [];
try {
  const fixtureBuild = join(artifacts, 'compiled-fixture');
  await build({
    configFile: resolve('vite.config.ts'),
    root: process.cwd(),
    build: {
      outDir: fixtureBuild,
      emptyOutDir: true,
      rollupOptions: {
        input: resolve('tests/web-e2e/ligandTopology.fixture.html'),
      },
    },
  });
  vite = await previewBuild({
    configFile: resolve('vite.config.ts'),
    build: { outDir: fixtureBuild },
    preview: { host: '127.0.0.1', port: 0, strictPort: false },
  });
  const address = vite.httpServer.address();
  browser = await chromium.launch({
    headless: true,
    executablePath: chromium.executablePath(),
  });
  page = await browser.newPage({
    viewport: { width: 1000, height: 950 },
  });
  page.on('console', (message) => {
    if (diagnostics.length < 20 && ['error', 'warning'].includes(message.type()))
      diagnostics.push({ type: message.type(), text: message.text() });
  });
  page.on('response', (response) => {
    if (new URL(response.url()).pathname.startsWith('/rdkit/') && diagnostics.length < 20)
      diagnostics.push({ asset: new URL(response.url()).pathname, status: response.status() });
  });
  page.on('worker', (worker) => diagnostics.push({ worker: worker.url() }));
  let rejectPageError;
  const pageFailure = new Promise((_, reject) => {
    rejectPageError = reject;
  });
  page.on('pageerror', (error) => {
    errors.push(error.message);
    rejectPageError(error);
  });
  await Promise.race([
    pageFailure,
    page.goto(`http://127.0.0.1:${address.port}/tests/web-e2e/ligandTopology.fixture.html`, {
      waitUntil: 'domcontentloaded',
    }),
  ]);
  const depiction = page.locator('.synon-biomed-molstar__ligand-depiction');
  const svg = depiction.locator('.synon-biomed-molstar__ligand-depiction-svg svg');
  // Mol* rebuilds asynchronously after a format/model change. Use one bounded
  // visual-readiness budget; keep all chemistry and model assertions intact.
  const visualReadiness = { timeout: 45000 };
  const readinessMeasurements = [];
  const bondPathCounts = (element) => {
    const counts = new Map();
    for (const path of element.querySelectorAll('path[class]')) {
      const bond = path.getAttribute('class')?.match(/\bbond-(\d+)\b/)?.[1];
      if (bond) counts.set(bond, (counts.get(bond) ?? 0) + 1);
    }
    return [...counts.values()].sort((a, b) => a - b);
  };
  await Promise.race([pageFailure, page.getByRole('button', { name: 'sdf', exact: true }).waitFor({ timeout: 60000 })]);
  const expected = await page.getByTestId('reference-svg').evaluate(bondPathCounts);
  assert.equal(expected.length, 8);
  await expect(page.getByTestId('chemistry-validation')).toHaveText('[true,true,true,true]');
  assert.ok(expected.filter((value) => value > 1).length >= 4, JSON.stringify(expected));
  for (const format of ['sdf', 'mol', 'mol2', 'cif', 'pdbqt']) {
    const started = Date.now();
    await page.getByRole('button', { name: format, exact: true }).click();
    await expect(depiction).toBeVisible(visualReadiness);
    const expand = depiction.getByRole('button', {
      name: '向上展开二维结构',
      exact: true,
    });
    if (await expand.isVisible()) await expand.click();
    await expect(svg).toBeVisible(visualReadiness);
    await expect.poll(() => svg.evaluate(bondPathCounts), { timeout: 15000 }).toEqual(expected);
    readinessMeasurements.push({ format, readyAfterMs: Date.now() - started });
    await page.screenshot({
      path: join(artifacts, format + '-bond-orders.png'),
    });
  }
  await expect(page.getByTestId('synon-biomed-molstar-model-navigator')).toContainText('模型 1 / 9');
  const nextModel = page.getByRole('button', { name: '下一个模型', exact: true });
  if (!(await nextModel.isVisible())) await page.getByTestId('synon-biomed-molstar-toolbar-collapse').click();
  await nextModel.click();
  await expect(page.getByTestId('synon-biomed-molstar-model-navigator')).toContainText('模型 2 / 9');
  await expect(svg).toBeVisible(visualReadiness);
  assert.deepEqual(await svg.evaluate(bondPathCounts), expected);
  const unknownStarted = Date.now();
  await page.getByRole('button', { name: 'pdb', exact: true }).click();
  await expect(depiction).toBeVisible(visualReadiness);
  const expandUnknown = depiction.getByRole('button', { name: '向上展开二维结构', exact: true });
  if (await expandUnknown.isVisible()) await expandUnknown.click();
  await expect(depiction).toContainText('文件缺少可核实的键级信息', {
    timeout: 30000,
  });
  await expect(svg).toHaveCount(0);
  await expect(page.getByTestId('synon-biomed-structure-canvas').locator('canvas').first()).toBeVisible(
    visualReadiness
  );
  readinessMeasurements.push({ format: 'pdb', readyAfterMs: Date.now() - unknownStarted });
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
      readinessMeasurements,
      pageErrors: errors,
      artifacts,
    })
  );
} catch (error) {
  if (page) {
    await page.screenshot({ path: join(artifacts, 'failed.png') });
    console.error(
      JSON.stringify({
        artifacts,
        pageErrors: errors,
        environment: await page.evaluate(() => ({
          Worker: Worker.toString(),
          baseURI: document.baseURI,
          location: location.href,
        })),
        diagnostics,
        visibleText: await page.locator('body').innerText(),
      })
    );
  }
  throw error;
} finally {
  await browser?.close();
  if (vite) await new Promise((resolve) => vite.httpServer.close(resolve));
}
