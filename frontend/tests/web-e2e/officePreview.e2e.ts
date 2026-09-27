import { expect, test, type Locator } from '@playwright/test';
import pdfPackage from 'pdfjs-dist/package.json';
import { createOfficePreviewFixtures, type OfficePreviewFixture } from './officePreviewFixtures';
import { appendSynonGoAssistantMessage, beginSynonGoTranscriptStream } from './synonGoFrameFixture';
import {
  createScientificWorkspace,
  csrfHeaders,
  loginToScientificWorkbench,
  openScientificArtifact,
  removeScientificWorkspace,
  showArtifactReferenceAssistantMessage,
  uploadScientificArtifact,
} from './synonBiomedScientificFixture';

test.use({ viewport: { width: 1440, height: 960 } });
const pdfjsVersion = pdfPackage.version;

for (const kind of ['pdf', 'docx', 'xlsx', 'pptx'] as const) {
  test(`renders real ${kind} artifact contents after navigation and refresh`, async ({ page }) => {
    await loginToScientificWorkbench(page);
    const workspace = await createScientificWorkspace(page, `office-${kind}`);
    const pageErrors: string[] = [];
    const pdfResources: Array<{ path: string; status: number }> = [];
    page.on('pageerror', (error) => pageErrors.push(error.message));
    page.on('response', (response) => {
      const path = new URL(response.url()).pathname;
      if (path.includes('/pdfjs/') || path.includes('/pdf.worker.'))
        pdfResources.push({ path, status: response.status() });
    });
    try {
      const fixture = (await createOfficePreviewFixtures()).find((item) => item.kind === kind)!;
      const artifact = await uploadScientificArtifact(page, workspace, fixture);
      if (kind === 'xlsx') {
        const converted = await page.request.post('/api/document/convert', {
          headers: await csrfHeaders(page),
          data: { artifact_id: artifact.artifactId, version_id: artifact.versionId, to: 'excel-json' },
        });
        expect(converted.status()).toBe(200);
        const payload = await converted.json();
        expect(payload.result.success).toBe(true);
        expect(payload.result.data.sheets[0].data[1]).toEqual([]);
        expect(payload.result.data.sheets[0].data[2][0]).toBe('THIRD ROW / 第三行');
        expect(String(payload.result.data.sheets[1].data[3][0])).toBe('30');
      }
      await openScientificArtifact(page, artifact.artifactId);
      await assertOfficeContent(page.locator('.artifact-preview-pane'), kind);
      if (kind === 'pdf') {
        expect(pdfResources.some((resource) => resource.path.includes('/pdf.worker.') && resource.status === 200)).toBe(
          true
        );
        expect(pdfResources.some((resource) => resource.path.includes('/cmaps/') && resource.status === 200)).toBe(
          true
        );
        expect(pdfResources.every((resource) => resource.status === 200)).toBe(true);
        // Some standard fonts are provided by the host; independently check the
        // configured packaged fallback is a real font, not an SPA HTML fallback.
        const font = await page.request.get(`/pdfjs/${pdfjsVersion}/standard_fonts/FoxitSerif.pfb`);
        expect(font.status()).toBe(200);
        expect(font.headers()['content-type']).not.toContain('text/html');
        expect((await font.body()).byteLength).toBeGreaterThan(1_000);
        await test.info().attach('pdf-resource-responses', {
          body: JSON.stringify(pdfResources, null, 2),
          contentType: 'application/json',
        });
      }
      await page.reload({ waitUntil: 'domcontentloaded' });
      await assertOfficeContent(page.locator('.artifact-preview-pane'), kind);
      expect(pageErrors).toEqual([]);
      await page.screenshot({ path: test.info().outputPath(`${kind}-artifact.png`), fullPage: true });
    } finally {
      await removeScientificWorkspace(page, workspace);
    }
  });
}

test('renders the same four files from assistant links in single preview and the file board', async ({ page }) => {
  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'office-links');
  const pageErrors: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  try {
    const artifacts = [];
    for (const fixture of await createOfficePreviewFixtures()) {
      artifacts.push({ ...(await uploadScientificArtifact(page, workspace, fixture)), kind: fixture.kind });
    }
    await showArtifactReferenceAssistantMessage(
      page,
      workspace,
      artifacts.map((artifact) => ({
        versionId: artifact.versionId,
        label: `Open ${artifact.filename}`,
      }))
    );
    const initialPageCount = page.context().pages().length;
    await page.getByRole('link', { name: 'Open preview-test.pdf', exact: true }).click();
    await assertOfficeContent(page.getByTestId('chat-preview-panel'), 'pdf');
    await expect(page.getByTestId('preview-board')).toHaveCount(0);
    for (const artifact of artifacts.slice(1)) {
      await page.getByRole('link', { name: `Open ${artifact.filename}`, exact: true }).click();
    }
    const board = page.getByTestId('preview-board');
    await expect(board).toBeVisible();
    await expect(board.getByTestId('preview-board-tile')).toHaveCount(4);
    for (const artifact of artifacts) {
      const tile = board
        .getByTestId('preview-board-tile')
        .filter({ has: page.locator('.preview-board__file-name', { hasText: artifact.filename }) });
      await assertOfficeContent(tile, artifact.kind);
    }
    expect(page.context().pages()).toHaveLength(initialPageCount);
    await expect(page).toHaveURL(new RegExp(`#/conversation/${workspace.conversationId}$`));
    await page.screenshot({ path: test.info().outputPath('office-board.png'), fullPage: true });
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.getByRole('link', { name: 'Open preview-test.pdf', exact: true }).click();
    await assertOfficeContent(page.getByTestId('chat-preview-panel'), 'pdf');
    expect(pageErrors).toEqual([]);
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});

test('shows a real PDF parse error and retry without a false loaded state', async ({ page }) => {
  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'invalid-pdf');
  try {
    const invalid = await uploadScientificArtifact(page, workspace, {
      filename: 'invalid-preview.pdf',
      contentType: 'application/pdf',
      source: '%PDF-1.4\ninvalid document',
    });
    let downloads = 0;
    page.on('request', (request) => {
      if (new URL(request.url()).pathname === `/api/artifacts/${invalid.artifactId}`) downloads += 1;
    });
    await openScientificArtifact(page, invalid.artifactId);
    await expect(page.getByText('PDF 预览加载失败', { exact: true })).toBeVisible();
    await expect(page.locator('.react-pdf__Page canvas')).toHaveCount(0);
    const beforeRetry = downloads;
    await page.getByRole('button', { name: '重试', exact: true }).click();
    await expect.poll(() => downloads).toBeGreaterThan(beforeRetry);
    await expect(page.getByText('PDF 预览加载失败', { exact: true })).toBeVisible();
    await showArtifactReferenceAssistantMessage(page, workspace, [
      { versionId: invalid.versionId, label: 'Open invalid PDF' },
    ]);
    await page.getByRole('link', { name: 'Open invalid PDF', exact: true }).click();
    await expect(page.getByText('PDF 预览加载失败', { exact: true })).toBeVisible();
    await expect(page.locator('iframe[data-testid="pdf-browser-preview"]')).toHaveCount(0);
    const valid = (await createOfficePreviewFixtures())[0];
    const recovery = await uploadScientificArtifact(page, workspace, valid);
    await openScientificArtifact(page, recovery.artifactId);
    await assertOfficeContent(page.locator('.artifact-preview-pane'), 'pdf');
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});

test('opens blue bold generated-file links in app without remote navigation or downloads', async ({ page }) => {
  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'office-markdown-links');
  const remoteRequests: string[] = [];
  const downloads: string[] = [];
  const popups: string[] = [];
  page.on('download', (download) => downloads.push(download.suggestedFilename()));
  page.on('popup', (popup) => popups.push(popup.url()));
  // An incorrect implementation must fail without contacting a public host.
  await page.route('https://synon.bio/**', (route) => {
    remoteRequests.push(route.request().url());
    return route.abort();
  });
  try {
    const fixtures = (await createOfficePreviewFixtures()).filter(
      (fixture) => fixture.kind === 'pdf' || fixture.kind === 'docx'
    );
    const links = [];
    for (const fixture of fixtures) {
      const artifact = await uploadScientificArtifact(page, workspace, fixture);
      const artifactPath = `/api/artifacts/${artifact.artifactId}/versions/${artifact.versionId}`;
      links.push({
        label: `${fixture.filename} (relative)`,
        markdownLabel: `**\`${fixture.filename}\`** (relative)`,
        url: artifactPath,
        kind: fixture.kind,
      });
      links.push({
        label: `${fixture.filename} (absolute)`,
        markdownLabel: `${fixture.filename} (absolute)`,
        url: `https://synon.bio${artifactPath}?download=1`,
        kind: fixture.kind,
      });
    }
    const stream = beginSynonGoTranscriptStream(workspace.conversationId);
    await page.goto(`/#/conversation/${workspace.conversationId}`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('message-list-scroller')).toBeVisible();
    appendSynonGoAssistantMessage(stream, links.map((link) => `[${link.markdownLabel}](${link.url})`).join('\n\n'));
    const initialPageCount = page.context().pages().length;
    for (const link of links) {
      const control = page.getByRole('link', { name: link.label, exact: true });
      await expect(control).toHaveClass(/markdown-artifact-file-link/);
      const style = await control.evaluate((element) => {
        const computed = getComputedStyle(element);
        return { color: computed.color, weight: Number(computed.fontWeight) };
      });
      const channels = style.color.match(/\d+/g)?.map(Number);
      expect(channels, style.color).toHaveLength(3);
      expect(channels![2]).toBeGreaterThan(channels![0] + 40);
      expect(channels![2]).toBeGreaterThan(channels![1]);
      expect(style.weight).toBeGreaterThanOrEqual(700);
      for (const nestedStyle of await control.locator('strong, code').evaluateAll((elements) =>
        elements.map((element) => {
          const computed = getComputedStyle(element);
          return { color: computed.color, weight: Number(computed.fontWeight) };
        })
      )) {
        expect(nestedStyle.color).toBe(style.color);
        expect(nestedStyle.weight).toBeGreaterThanOrEqual(700);
      }
      if (link === links[0]) {
        await control.focus();
        await control.press('Enter');
      } else {
        await control.click();
      }
      await assertOfficeContent(page.getByTestId('chat-preview-panel'), link.kind);
      await expect(page).toHaveURL(new RegExp(`#/conversation/${workspace.conversationId}$`));
      if (link !== links.at(-1)) await page.getByRole('button', { name: '关闭预览', exact: true }).click();
    }
    expect(page.context().pages()).toHaveLength(initialPageCount);
    expect(popups).toEqual([]);
    expect(downloads).toEqual([]);
    expect(remoteRequests).toEqual([]);
    await page.screenshot({ path: test.info().outputPath('generated-file-links.png'), fullPage: true });
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});

async function assertOfficeContent(root: Locator, kind: OfficePreviewFixture['kind']) {
  if (kind === 'pdf') {
    const scroller = root.locator('.react-pdf__Document [data-virtuoso-scroller]').first();
    await expect(scroller).toBeVisible();
    await scroller.hover();
    await root.page().mouse.wheel(0, -10_000);
    for (const page of [1, 2]) {
      if (page === 2) {
        await scroller.hover();
        await root.page().mouse.wheel(0, 10_000);
      }
      const pageSurface = root.locator(`[data-pdf-page="${page}"]`);
      await expect(pageSurface.locator('canvas')).toBeVisible();
      const title = pageSurface
        .locator('.react-pdf__Page__textContent')
        .getByText(`PREVIEW TEST page ${page}`, { exact: true });
      await title.scrollIntoViewIfNeeded();
      await expect(title).toBeInViewport();
      await expect(
        pageSurface.locator('.react-pdf__Page__textContent').getByText('预览测试', { exact: true })
      ).toBeVisible();
    }
    const canvas = root.locator('[data-pdf-page="2"] canvas');
    await expect
      .poll(() =>
        canvas.evaluate((element) => {
          const surface = element as HTMLCanvasElement;
          const context = surface.getContext('2d');
          if (!context || !surface.width || !surface.height) return 0;
          const pixels = context.getImageData(0, 0, surface.width, surface.height).data;
          let colored = 0;
          for (let index = 0; index < pixels.length; index += 4) {
            if (pixels[index + 1] - pixels[index] > 40 && pixels[index + 2] - pixels[index] > 40) colored += 1;
          }
          return colored;
        })
      )
      .toBeGreaterThan(100);
    await expect(root.locator('iframe')).toHaveCount(0);
  } else if (kind === 'docx') {
    await expect(root.getByTestId('lightweight-office-word')).toContainText(
      'Office preview preserves Chinese text: 中文内容。'
    );
    await expect(root.getByTestId('lightweight-office-word')).toContainText('Beta');
  } else if (kind === 'xlsx') {
    const workbook = root.locator('[data-testid="lightweight-office-excel"],.preview-table');
    await expect(workbook).toBeVisible();
    if ((await workbook.getAttribute('data-testid')) !== 'lightweight-office-excel') {
      await workbook.getByRole('button', { name: 'Sparse', exact: true }).click();
      await expect(workbook.getByRole('cell', { name: 'THIRD ROW / 第三行', exact: true })).toBeVisible();
      await workbook.getByRole('button', { name: 'Totals', exact: true }).click();
      await expect(workbook.getByRole('cell', { name: '30', exact: true })).toBeVisible();
      return;
    }
    await workbook.getByRole('tab', { name: 'Sparse', exact: true }).click();
    const rows = workbook.locator('tbody tr');
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(1).locator('td').first()).toHaveText('');
    await expect(rows.nth(2).locator('th')).toHaveText('3');
    await expect(rows.nth(2)).toContainText('THIRD ROW / 第三行');
    await workbook.getByRole('tab', { name: 'Totals', exact: true }).click();
    await expect(workbook.locator('tbody tr').nth(3).locator('td').first()).toHaveText('30');
  } else {
    const slides = root.getByTestId('lightweight-office-ppt');
    await expect(slides.getByRole('region')).toHaveCount(2);
    await expect(slides.getByRole('region').nth(0)).toContainText('PREVIEW TEST slide 1');
    await expect(slides.getByRole('region').nth(1)).toContainText('预览测试 / Chinese content 2');
  }
}
