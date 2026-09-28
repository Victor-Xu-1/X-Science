import { expect, test, type Page } from '@playwright/test';
import { createPreviewPdf } from './officePreviewFixtures';
import {
  createScientificWorkspace,
  loginToScientificWorkbench,
  removeScientificWorkspace,
  uploadScientificArtifact,
} from './synonBiomedScientificFixture';
import {
  appendSynonGoTranscriptDeltas,
  beginSynonGoTranscriptStream,
  cancelSynonGoProjectionTool,
  completeSynonGoProjectionTool,
} from './synonGoFrameFixture';

type ProjectedMessage = {
  id?: string;
  type?: string;
  content?: { content?: string; call_id?: string; status?: string };
};

async function messages(page: Page, conversationId: string): Promise<ProjectedMessage[]> {
  const response = await page.request.get(
    `/api/conversations/${encodeURIComponent(conversationId)}/messages?limit=50&content_mode=compact`
  );
  expect(response.status()).toBe(200);
  const body = await response.json();
  expect(Array.isArray(body.items)).toBe(true);
  return body.items;
}

test.use({ viewport: { width: 1440, height: 960 } });

test('preserves the final answer and file preview after a cancelled durable tool resumes', async ({ page }) => {
  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'projection-recovery');
  const pageErrors: string[] = [];
  const downloads: string[] = [];
  const popups: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  page.on('download', (download) => downloads.push(download.suggestedFilename()));
  page.on('popup', (popup) => popups.push(popup.url()));
  const prefix = '正在准备恢复回归报告，最终结果尚未生成。';
  const finalMarker = '恢复后的最终答复已完成，文件链接应持久可见。';
  try {
    const artifact = await uploadScientificArtifact(page, workspace, {
      filename: 'recovered-report.pdf',
      contentType: 'application/pdf',
      source: createPreviewPdf('Recovered final report'),
    });
    const artifactPath = `/api/artifacts/${encodeURIComponent(artifact.artifactId)}/versions/${encodeURIComponent(artifact.versionId)}`;
    const finalAnswer = `${finalMarker}\n\n[**recovered-report.pdf**](${artifactPath})`;
    const run = beginSynonGoTranscriptStream(workspace.conversationId);
    const conversationPath = `/#/conversation/${workspace.conversationId}`;
    await page.goto(conversationPath, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('message-list-scroller')).toBeVisible();
    appendSynonGoTranscriptDeltas(run, [prefix]);
    await expect(page.getByText(prefix, { exact: true })).toBeVisible();
    await expect
      .poll(
        async () =>
          (await messages(page, workspace.conversationId)).filter((item) => item.content?.content === prefix).length
      )
      .toBe(1);
    cancelSynonGoProjectionTool(run);
    // Observe settled cancellation before resuming: this exercises the persisted
    // incremental checkpoint, not merely a one-shot replay of the final stream.
    await expect
      .poll(
        async () =>
          (await messages(page, workspace.conversationId)).find(
            (item) => item.content?.call_id === `e2e-projection-tool:${run.runId}`
          )?.content?.status
      )
      .toBe('canceled');
    expect(completeSynonGoProjectionTool(run, finalAnswer, [artifact])).toBeGreaterThan(run.attempt);
    await expect(page.getByText(finalMarker, { exact: true })).toBeVisible();
    let finalID: string | undefined;
    await expect
      .poll(async () => {
        const final = (await messages(page, workspace.conversationId)).filter(
          (item) => item.content?.content === finalAnswer
        );
        finalID = final[0]?.id;
        return final.length;
      })
      .toBe(1);
    expect(finalID).toBeTruthy();
    for (const navigation of ['initial', 'refresh', 'navigate'] as const) {
      if (navigation === 'refresh') await page.reload({ waitUntil: 'domcontentloaded' });
      if (navigation === 'navigate') {
        await page.goto('/#/guid', { waitUntil: 'domcontentloaded' });
        await page.goto(conversationPath, { waitUntil: 'domcontentloaded' });
      }
      await expect(page.getByText(finalMarker, { exact: true })).toBeVisible();
      await expect(page.getByText(prefix, { exact: true })).toBeVisible();
      await expect(page.getByTestId('terminal-failure-history')).toContainText('此前未完成的回复');
      await expect(page.getByTestId('terminal-failure-alert')).toHaveCount(0);
      const finals = (await messages(page, workspace.conversationId)).filter(
        (item) => item.content?.content === finalAnswer
      );
      expect(finals).toHaveLength(1);
      expect(finals[0].id).toBe(finalID);
      const link = page.getByRole('link', { name: artifact.filename, exact: true });
      await expect(link).toHaveClass(/markdown-artifact-file-link/);
      for (const styled of [link, link.locator('strong')]) {
        await expect(styled).toHaveCSS('color', 'rgb(29, 78, 216)');
        await expect(styled).toHaveCSS('font-weight', '700');
      }
    }
    const contentResponse = page.waitForResponse(
      (response) => new URL(response.url()).pathname === artifactPath && response.status() === 200
    );
    const link = page.getByRole('link', { name: artifact.filename, exact: true });
    await link.focus();
    await link.press('Enter');
    await contentResponse;
    const panel = page.getByTestId('chat-preview-panel');
    await expect(panel.locator('[data-pdf-page="1"] canvas')).toBeVisible();
    await expect(panel.locator('[data-pdf-page="1"] .react-pdf__Page__textContent')).toContainText(
      'Recovered final report'
    );
    expect(page.url()).toContain(conversationPath);
    expect(popups).toEqual([]);
    expect(downloads).toEqual([]);
    expect(pageErrors).toEqual([]);
    await page.screenshot({ path: test.info().outputPath('recovered-final-answer-and-pdf.png'), fullPage: true });
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});
