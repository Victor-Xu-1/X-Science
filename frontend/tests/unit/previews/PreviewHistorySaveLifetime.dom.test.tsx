/**
 * @license
 * Copyright 2026 X-Science contributors
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import { usePreviewHistory } from '@/renderer/pages/conversation/Preview/hooks/usePreviewHistory';
import { PreviewProvider } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { ConfigProvider } from '@arco-design/web-react';
import { act, cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createTestI18n } from '../i18nTestUtils';

type History = ReturnType<typeof usePreviewHistory>;
type Tab = NonNullable<Parameters<typeof usePreviewHistory>[0]['activeTab']> & { id?: string };
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
}
const artifact = (id = 'tab-a', artifactId = 'artifact-a'): Tab => ({
  id,
  content_type: 'markdown',
  content: 'Synthetic saved content',
  title: 'Synthetic artifact',
  metadata: { artifactId, versionId: 'version-1', file_name: 'synthetic.md' },
});
async function host(
  initial: Tab,
  saveContent: () => Promise<boolean>,
  onMountSave?: (operation: Promise<boolean>) => void
) {
  const i18n = await createTestI18n('en-US');
  const markContentSaved = vi.fn();
  let tab = initial,
    canvas = 'a',
    current!: History;
  function Host() {
    current = usePreviewHistory({ activeTab: tab, saveContent, markContentSaved });
    React.useEffect(() => {
      if (onMountSave) onMountSave(current.handleSaveSnapshot());
    }, [onMountSave]);
    return <ConfigProvider>{current.messageContextHolder}</ConfigProvider>;
  }
  const content = () => (
    <I18nextProvider i18n={i18n}>
      <PreviewProvider
        workspace={{ ownerId: getRendererAccountOwnerId(), projectId: canvas, routeKey: `/projects/${canvas}` }}
      >
        <Host />
      </PreviewProvider>
    </I18nextProvider>
  );
  const element = () => (onMountSave ? <React.StrictMode>{content()}</React.StrictMode> : content());
  let view!: ReturnType<typeof render>;
  await act(async () => {
    view = render(element());
  });
  return {
    ...view,
    i18n,
    markContentSaved,
    current: () => current,
    move(next: Tab, nextCanvas = canvas) {
      tab = next;
      canvas = nextCanvas;
      view.rerender(element());
    },
    save() {
      let promise!: Promise<boolean>;
      act(() => {
        promise = current.handleSaveSnapshot();
      });
      return promise;
    },
  };
}
beforeEach(() => {
  window.__backendPort = 13400;
  localStorage.clear();
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('test-owner');
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async () => Response.json([]))
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  resetRendererAccountScopeForTest();
  delete window.__backendPort;
});

describe('preview history save lifetime', () => {
  it.each(['resolve', 'reject'] as const)(
    'discards %s completion after its real message host unmounts',
    async (outcome) => {
      const pending = deferred<boolean>(),
        save = vi.fn(() => pending.promise);
      const view = await host(artifact(), save),
        operation = view.save();
      expect(save).toHaveBeenCalledOnce();
      expect(view.current().snapshotSaving).toBe(true);
      const discarded = expect(operation).resolves.toBe(false);
      view.unmount();
      await act(async () => {
        if (outcome === 'resolve') pending.resolve(true);
        else pending.reject(new Error('Synthetic save failure'));
        await discarded;
      });
    }
  );
  it.each(['resolve', 'reject'] as const)(
    'keeps current-owner %s feedback and clears its busy state',
    async (outcome) => {
      const pending = deferred<boolean>(),
        view = await host(artifact(), () => pending.promise),
        operation = view.save();
      await act(async () => {
        if (outcome === 'resolve') pending.resolve(true);
        else pending.reject(new Error('Synthetic current failure'));
        expect(await operation).toBe(outcome === 'resolve');
      });
      expect(view.current().snapshotSaving).toBe(false);
      expect(
        screen.getByText(view.i18n.t(outcome === 'resolve' ? 'preview.snapshotSaved' : 'preview.snapshotSaveFailed'))
      ).toBeTruthy();
    }
  );
  it('accepts its own successful ACK when that updates the artifact version', async () => {
    const pending = deferred<boolean>(),
      tab = artifact(),
      view = await host(tab, () => pending.promise),
      operation = view.save();
    view.move({ ...tab, metadata: { ...tab.metadata, versionId: 'version-2' } });
    expect(view.current().snapshotSaving).toBe(true);
    await act(async () => {
      pending.resolve(true);
      expect(await operation).toBe(true);
    });
    expect(view.current().snapshotSaving).toBe(false);
    expect(screen.getByText(view.i18n.t('preview.snapshotSaved'))).toBeTruthy();
  });
  it.each(['tab', 'canvas'] as const)(
    'allows the next %s to save the same artifact while an old save finishes',
    async (receiver) => {
      const old = deferred<boolean>(),
        next = deferred<boolean>(),
        save = vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
      const view = await host(artifact(), save),
        first = view.save();
      view.move(artifact(receiver === 'tab' ? 'tab-b' : 'tab-a'), receiver === 'canvas' ? 'b' : 'a');
      expect(view.current().snapshotSaving).toBe(false);
      const second = view.save();
      expect(save).toHaveBeenCalledTimes(2);
      await act(async () => {
        old.resolve(true);
        expect(await first).toBe(false);
      });
      expect(view.current().snapshotSaving).toBe(true);
      expect(screen.queryByText(view.i18n.t('preview.snapshotSaved'))).toBeNull();
      await act(async () => {
        next.resolve(true);
        expect(await second).toBe(true);
      });
      expect(view.current().snapshotSaving).toBe(false);
    }
  );
  it('does not revive an original save after A to B to A returns to the same tab', async () => {
    const old = deferred<boolean>(),
      next = deferred<boolean>(),
      tab = artifact(),
      save = vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
    const view = await host(tab, save),
      first = view.save();
    view.move(artifact('tab-b', 'artifact-b'));
    view.move(tab);
    const second = view.save();
    expect(save).toHaveBeenCalledTimes(2);
    await act(async () => {
      old.reject(new Error('Synthetic stale A failure'));
      expect(await first).toBe(false);
    });
    expect(view.current().snapshotSaving).toBe(true);
    expect(screen.queryByText(view.i18n.t('preview.snapshotSaveFailed'))).toBeNull();
    await act(async () => {
      next.resolve(true);
      expect(await second).toBe(true);
    });
  });
  it('invalidates immediately at the account boundary and permits the new account save', async () => {
    const old = deferred<boolean>(),
      next = deferred<boolean>(),
      tab = artifact(),
      save = vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
    const view = await host(tab, save),
      first = view.save();
    act(() => {
      setRendererAccountOwner('next-owner');
    });
    await act(async () => {
      old.resolve(true);
      expect(await first).toBe(false);
    });
    expect(screen.queryByText(view.i18n.t('preview.snapshotSaved'))).toBeNull();
    view.move(tab);
    const second = view.save();
    await act(async () => {
      next.resolve(true);
      expect(await second).toBe(true);
    });
  });
  it('marks only the current local file and avoids carrying the old debounce into it', async () => {
    const old = deferred<boolean>(),
      next = deferred<boolean>();
    const save = vi
      .spyOn(ipcBridge.previewHistory.save, 'invoke')
      .mockImplementationOnce(async () => {
        await old.promise;
        return { id: 'old', label: 'Old', created_at: 1, size: 0, contentType: 'markdown' };
      })
      .mockImplementationOnce(async () => {
        await next.promise;
        return { id: 'next', label: 'Next', created_at: 1, size: 0, contentType: 'markdown' };
      });
    const local = (id: string): Tab => ({
      id,
      content_type: 'markdown',
      content: 'Local synthetic',
      title: 'Local',
      metadata: { workspace: '/synthetic', file_path: `/synthetic/${id}.md` },
    });
    const view = await host(local('a'), async () => true),
      first = view.save();
    view.move(local('b'));
    const second = view.save();
    expect(save).toHaveBeenCalledTimes(2);
    await act(async () => {
      old.resolve(true);
      expect(await first).toBe(false);
    });
    expect(view.markContentSaved).not.toHaveBeenCalled();
    expect(view.current().snapshotSaving).toBe(true);
    await act(async () => {
      next.resolve(true);
      expect(await second).toBe(true);
    });
    expect(view.markContentSaved).toHaveBeenCalledOnce();
    expect(view.current().snapshotSaving).toBe(false);
  });
  it('invalidates the old mount generation during StrictMode replay without clearing its successor save', async () => {
    const old = deferred<boolean>(),
      next = deferred<boolean>();
    const save = vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
    const operations: Promise<boolean>[] = [];
    const view = await host(artifact(), save, (operation) => operations.push(operation));
    expect(save).toHaveBeenCalledTimes(2);
    expect(operations).toHaveLength(2);
    await act(async () => {
      old.resolve(true);
      expect(await operations[0]).toBe(false);
    });
    expect(view.current().snapshotSaving).toBe(true);
    expect(screen.queryByText(view.i18n.t('preview.snapshotSaved'))).toBeNull();
    await act(async () => {
      next.resolve(true);
      expect(await operations[1]).toBe(true);
    });
    expect(view.current().snapshotSaving).toBe(false);
  });
});
