/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import PDFViewer from '@/renderer/pages/conversation/Preview/components/viewers/PDFViewer';
import { act, cleanup, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/pages/artifact/SynonBiomedPdfArtifactViewer', () => ({
  SynonBiomedPdfArtifactViewer: ({ filename, contentUrl }: { filename: string; contentUrl: string }) => (
    <section aria-label={filename} data-testid='shared-pdf-viewer' data-source={contentUrl} />
  ),
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('PDFViewer', () => {
  it('renders a localized missing-source error', async () => {
    const view = await renderWithI18n(<PDFViewer />, 'en-US');

    expect(await screen.findByText('PDF file path is missing')).toBeTruthy();
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByText('PDF 文件路径为空')).toBeTruthy();
  });

  it('uses the shared PDF.js renderer instead of a browser plugin', async () => {
    await renderWithI18n(<PDFViewer content='data:application/pdf;base64,JVBERi0xLjQ=' hideToolbar />, 'en-US');
    expect(screen.getByTestId('shared-pdf-viewer')).toHaveAttribute(
      'data-source',
      'data:application/pdf;base64,JVBERi0xLjQ='
    );
    expect(document.querySelector('iframe, webview')).toBeNull();
  });

  it('loads a remote project PDF from its authenticated content endpoint', async () => {
    await renderWithI18n(
      <PDFViewer
        file_path='synonbiomed://project/proj_123/project-files/report.pdf'
        content='/api/projects/proj_123/artifacts/artifact_123/content'
        hideToolbar
      />,
      'en-US'
    );

    expect(screen.getByTestId('shared-pdf-viewer')).toHaveAttribute(
      'data-source',
      '/api/projects/proj_123/artifacts/artifact_123/content'
    );
  });

  it('uses transported content even when local path metadata exists', async () => {
    await renderWithI18n(
      <PDFViewer file_path='/workspace/report.pdf' content='data:application/pdf;base64,JVBERi0xLjQ=' />,
      'en-US'
    );
    expect(screen.getByTestId('shared-pdf-viewer')).toHaveAttribute(
      'data-source',
      'data:application/pdf;base64,JVBERi0xLjQ='
    );
  });

  it('reports an unavailable disk source without requesting a file URL', async () => {
    await renderWithI18n(<PDFViewer file_path='/workspace/report.pdf' />, 'en-US');
    expect(screen.getByText('Failed to load PDF document')).toBeInTheDocument();
    expect(document.querySelector('iframe, webview')).toBeNull();
    expect(screen.queryByTestId('shared-pdf-viewer')).toBeNull();
  });
});
