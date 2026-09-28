import { expect, test } from './officialChromeTest';
import {
  createScientificWorkspace,
  loginToScientificWorkbench,
  removeScientificWorkspace,
  uploadScientificArtifact,
} from './synonBiomedScientificFixture';
import {
  beginSynonGoStreamingParity,
  beginSynonGoTranscriptStream,
  completeSynonGoStreamingParity,
} from './synonGoFrameFixture';

const copy = {
  thinking: '核对本轮生成文件。',
  search: '核对输入资料',
  compute: '生成本轮结果',
  final: '本轮完成，文件统一列在回答之后。',
};
const csv = 'name,value\nround-result,42\n';

for (const viewport of [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'narrow', width: 390, height: 844 },
]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test('delivers one final file group with native preview and stable reloads', async ({ page }, testInfo) => {
      test.setTimeout(90_000);
      await loginToScientificWorkbench(page);
      const workspace = await createScientificWorkspace(page, 'round-delivery');
      const downloads: string[] = [];
      page.on('download', (download) => downloads.push(download.suggestedFilename()));
      try {
        const report = await uploadScientificArtifact(page, workspace, {
          filename: 'round-report.md',
          contentType: 'text/markdown',
          source: '# Round result\n\nVerified fixture.',
        });
        const table = await uploadScientificArtifact(page, workspace, {
          filename: 'round-table.csv',
          contentType: 'text/csv',
          source: csv,
        });
        const run = beginSynonGoTranscriptStream(workspace.conversationId);
        await page.goto(`/#/conversation/${workspace.conversationId}`, { waitUntil: 'domcontentloaded' });
        await expect(page.getByTestId('message-list-scroller')).toBeVisible();
        beginSynonGoStreamingParity(run, copy);
        await expect(page.getByRole('button', { name: /开展分析/ })).toBeVisible();
        const tray = page.getByTestId('message-artifact-references');
        await expect(tray).toHaveCount(0);

        completeSynonGoStreamingParity(
          run,
          copy,
          [report, table].map((artifact) => ({
            artifactId: artifact.artifactId,
            versionId: artifact.versionId,
          }))
        );
        const final = page.getByText(copy.final, { exact: true });
        await expect(final).toHaveCount(1);
        await expect(final).toBeVisible();
        await expect(tray).toHaveCount(1);
        await expect(tray.getByRole('button', { name: /^预览/ })).toHaveCount(2);
        const trayHandle = await tray.elementHandle();
        expect(
          await final.evaluate(
            (element, files) =>
              Boolean(files && element.compareDocumentPosition(files) & Node.DOCUMENT_POSITION_FOLLOWING),
            trayHandle
          )
        ).toBe(true);

        const pages = page.context().pages().length;
        await tray.getByRole('button', { name: '预览 round-table.csv', exact: true }).click();
        const preview = page.getByRole('region', { name: '数据表格预览' });
        await expect(preview.getByRole('cell', { name: 'round-result', exact: true })).toBeVisible();
        await expect(page).toHaveURL(new RegExp(`#/conversation/${workspace.conversationId}$`));
        expect(page.context().pages()).toHaveLength(pages);
        expect(downloads).toEqual([]);
        await page.getByRole('button', { name: '关闭预览', exact: true }).click();

        for (let reload = 0; reload < 2; reload += 1) {
          await page.reload({ waitUntil: 'domcontentloaded' });
          await expect(page.getByTestId('message-list-scroller')).toBeVisible();
          await expect(final).toHaveCount(1);
          await expect(tray).toHaveCount(1);
          await expect(tray.getByRole('button', { name: /^预览/ })).toHaveCount(2);
          await expect(tray.getByRole('button', { name: '预览 round-report.md', exact: true })).toBeVisible();
        }
        const content = await page.request.get(`/api/artifacts/${table.artifactId}/versions/${table.versionId}`);
        expect(content.status()).toBe(200);
        expect(await content.text()).toBe(csv);
        expect(downloads).toEqual([]);
        await page.screenshot({ path: testInfo.outputPath(`round-delivery-${viewport.name}.png`) });
      } finally {
        await removeScientificWorkspace(page, workspace);
      }
    });
  });
}
