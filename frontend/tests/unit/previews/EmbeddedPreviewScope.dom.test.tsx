import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { emitter } from '@/renderer/utils/emitter';
import { act, cleanup, renderHook } from '@testing-library/react';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createTestI18n } from '../i18nTestUtils';

vi.mock('@/common', () => ({ ipcBridge: { fs: {} } }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { on: vi.fn(), off: vi.fn() } }));

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function embedded() {
  const i18n = await createTestI18n('en-US');
  const wrapper = ({ children }: React.PropsWithChildren) => (
    <I18nextProvider i18n={i18n}>
      <PreviewProvider scope='embedded'>{children}</PreviewProvider>
    </I18nextProvider>
  );
  return renderHook(() => usePreviewContext(), { wrapper });
}

describe('embedded preview ownership', () => {
  it('does not read or inherit the workspace preview history', async () => {
    localStorage.setItem(
      'synon-ai_preview_tabs',
      JSON.stringify([
        { id: 'workspace-draft', title: 'workspace.md', content: '# Keep this', content_type: 'markdown' },
      ])
    );
    const get = vi.spyOn(Storage.prototype, 'getItem');
    const { result } = await embedded();
    expect(result.current.tabs).toEqual([]);
    expect(get).not.toHaveBeenCalled();
  });

  it('does not overwrite or remove workspace history when its local entries change', async () => {
    const view = await embedded();
    const set = vi.spyOn(Storage.prototype, 'setItem');
    const remove = vi.spyOn(Storage.prototype, 'removeItem');
    vi.useFakeTimers();
    act(() =>
      view.result.current.openPreview(
        '# Local',
        'markdown',
        { file_name: 'local.md', editable: false },
        { presentation: 'board' }
      )
    );
    act(() => vi.advanceTimersByTime(200));
    expect(view.result.current.tabs).toHaveLength(1);
    expect(set).not.toHaveBeenCalled();
    expect(remove).not.toHaveBeenCalled();
    act(() => view.result.current.closePreview());
    expect(remove).not.toHaveBeenCalled();
  });

  it('does not subscribe to the workspace global open channel', async () => {
    await embedded();
    expect(emitter.on).not.toHaveBeenCalled();
  });
});
