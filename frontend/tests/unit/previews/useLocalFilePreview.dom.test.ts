/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useLocalFilePreview } from '@/renderer/pages/conversation/Preview/hooks/useLocalFilePreview';

const mocks = vi.hoisted(() => ({
  metadata: vi.fn(),
  readBuffer: vi.fn(),
  readText: vi.fn(),
  openPreview: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    fs: {
      getFileMetadata: { invoke: mocks.metadata },
      readFileBuffer: { invoke: mocks.readBuffer },
      readFile: { invoke: mocks.readText },
    },
  },
}));

vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => ({ openPreview: mocks.openPreview }),
}));

describe('local PDF preview transport', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.metadata.mockResolvedValue({ size: 12 });
    mocks.readBuffer.mockResolvedValue('JVBERi0xLjcK');
  });

  it('reads PDF bytes through the workspace-scoped bridge before opening the board', async () => {
    const { result } = renderHook(() => useLocalFilePreview('/workspace'));
    await act(async () => result.current('/workspace/report.pdf'));
    expect(mocks.readBuffer).toHaveBeenCalledWith({ path: '/workspace/report.pdf', workspace: '/workspace' });
    expect(mocks.readText).not.toHaveBeenCalled();
    expect(mocks.openPreview).toHaveBeenCalledWith(
      '',
      'pdf',
      expect.objectContaining({
        file_path: '/workspace/report.pdf',
        contentUrl: 'data:application/pdf;base64,JVBERi0xLjcK',
        workspace: '/workspace',
      }),
      { presentation: 'board' }
    );
  });

  it('does not open a filesystem URL when the scoped read is denied or oversized', async () => {
    mocks.readBuffer.mockRejectedValue(new Error('FS_ACCESS_DENIED'));
    const { result } = renderHook(() => useLocalFilePreview('/workspace'));
    await act(async () => result.current('/workspace/report.pdf'));
    expect(mocks.openPreview).toHaveBeenCalledWith(
      '',
      'pdf',
      expect.objectContaining({ missingFile: true, editable: false }),
      { presentation: 'board' }
    );
    expect(mocks.openPreview.mock.calls[0][2]).not.toHaveProperty('contentUrl');
  });
});
