import React from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { createTestI18n } from '../i18nTestUtils';
import type { PdfReadingPreviewOwner } from '@/renderer/services/runtime/pdfReadingPreview';
import { setRendererAccountOwner, resetRendererAccountScopeForTest } from '@/renderer/services/rendererAccountScope';
import {
  previewWorkspaceStorageKey,
  type PreviewWorkspace,
} from '@/renderer/pages/conversation/Preview/context/previewWorkspacePersistence';

async function canvas(initial: PreviewWorkspace) {
  let workspace = initial;
  const i18n = await createTestI18n('en-US');
  const wrapper = ({ children }: React.PropsWithChildren) => (
    <I18nextProvider i18n={i18n}>
      <PreviewProvider workspace={workspace}>{children}</PreviewProvider>
    </I18nextProvider>
  );
  const view = renderHook(() => usePreviewContext(), { wrapper });
  return {
    ...view,
    move(next: PreviewWorkspace) {
      workspace = next;
      act(() => view.rerender());
    },
  };
}

const project = (projectId: string, ownerId = 'owner'): PreviewWorkspace => ({
  ownerId,
  projectId,
  routeKey: `/projects/${projectId}`,
});
const session = (conversationId: string): PreviewWorkspace => ({
  ownerId: 'owner',
  conversationId,
  routeKey: `/conversation/${conversationId}`,
});

const localNote = (workspace = '/synthetic') => ({
  file_name: 'notes.md',
  file_path: 'notes.md',
  workspace,
});

const pendingLocalRead = () => {
  let resolve!: (content: string) => void;
  const promise = new Promise<string>((finish) => (resolve = finish));
  return { promise, resolve };
};

async function localCanvas(projectId: string) {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  vi.useFakeTimers();
  return canvas(project(projectId));
}

const advanceLocalClock = async (milliseconds: number) => {
  await act(async () => vi.advanceTimersByTimeAsync(milliseconds));
};

const calls = vi.hoisted(() => ({
  loadArtifact: vi.fn(),
  metadata: vi.fn(),
  readFile: vi.fn(),
  writeFile: vi.fn(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    fs: {
      getFileMetadata: { invoke: calls.metadata },
      readFile: { invoke: calls.readFile },
      writeFile: { invoke: calls.writeFile },
    },
  },
}));
vi.mock('@/renderer/services/xScienceGateway', () => ({ loadXScienceArtifact: calls.loadArtifact }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { on: vi.fn(), off: vi.fn() } }));

beforeEach(() => {
  localStorage.clear();
  resetRendererAccountScopeForTest();
  calls.loadArtifact.mockReset();
  calls.metadata.mockReset();
  calls.readFile.mockReset();
  calls.writeFile.mockReset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('project preview scope', () => {
  it('keeps a newer saved local draft when an older polling read arrives after the save marker expires', async () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    vi.useFakeTimers();
    let finishOldRead!: (content: string) => void;
    calls.metadata.mockResolvedValueOnce({ lastModified: 1 }).mockResolvedValue({ lastModified: 2 });
    calls.readFile.mockReturnValue(new Promise<string>((resolve) => (finishOldRead = resolve)));
    calls.writeFile.mockResolvedValue(true);
    const view = await canvas(project('poll-save'));
    await act(async () => {
      view.result.current.openPreview('original local text', 'markdown', {
        file_name: 'notes.md',
        file_path: '/synthetic/notes.md',
        workspace: '/synthetic',
      });
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(calls.readFile).toHaveBeenCalledExactlyOnceWith({
      path: '/synthetic/notes.md',
      workspace: '/synthetic',
    });
    act(() => view.result.current.updateContent('newer saved local text'));
    await act(async () => {
      expect(await view.result.current.saveContent()).toBe(true);
    });
    expect(view.result.current.activeTab).toMatchObject({
      content: 'newer saved local text',
      originalContent: 'newer saved local text',
      isDirty: false,
    });
    await act(async () => vi.advanceTimersByTimeAsync(501));
    await act(async () => finishOldRead('older filesystem response'));
    expect(view.result.current.activeTab).toMatchObject({
      content: 'newer saved local text',
      originalContent: 'newer saved local text',
      isDirty: false,
    });
  });
  it('keeps current local content after a failed read and retries the same changed mtime', async () => {
    const view = await localCanvas('read-failure');
    vi.spyOn(console, 'error').mockImplementation(() => {});
    calls.metadata.mockResolvedValueOnce({ lastModified: 1 }).mockResolvedValue({ lastModified: 2 });
    calls.readFile.mockRejectedValueOnce(new Error('synthetic read failed')).mockResolvedValueOnce('fresh disk text');
    await act(async () => view.result.current.openPreview('known local text', 'markdown', localNote()));
    await advanceLocalClock(1000);
    expect(view.result.current.activeTab).toMatchObject({
      content: 'known local text',
      originalContent: 'known local text',
      isDirty: false,
    });
    await advanceLocalClock(1000);
    expect(calls.readFile).toHaveBeenCalledTimes(2);
    expect(view.result.current.activeTab).toMatchObject({
      content: 'fresh disk text',
      originalContent: 'fresh disk text',
      isDirty: false,
    });
  });

  it.each(['refused', 'rejected'] as const)(
    'keeps an unsaved local draft when save is %s and an older read finishes',
    async (failure) => {
      const view = await localCanvas(`save-${failure}`);
      const oldRead = pendingLocalRead();
      calls.metadata.mockResolvedValueOnce({ lastModified: 1 }).mockResolvedValue({ lastModified: 2 });
      calls.readFile.mockReturnValue(oldRead.promise);
      if (failure === 'refused') calls.writeFile.mockResolvedValue(false);
      else calls.writeFile.mockRejectedValue(new Error('synthetic write failed'));
      await act(async () => view.result.current.openPreview('known local text', 'markdown', localNote()));
      await advanceLocalClock(1000);
      expect(calls.readFile).toHaveBeenCalledTimes(1);
      act(() => view.result.current.updateContent('unsaved local draft'));
      await act(async () => {
        if (failure === 'refused') expect(await view.result.current.saveContent()).toBe(false);
        else await expect(view.result.current.saveContent()).rejects.toThrow('synthetic write failed');
      });
      await advanceLocalClock(501);
      await act(async () => oldRead.resolve('older filesystem response'));
      expect(view.result.current.activeTab).toMatchObject({
        content: 'unsaved local draft',
        originalContent: 'known local text',
        isDirty: true,
      });
    }
  );

  it.each(['source', 'project'] as const)(
    'ignores an old local polling read after a %s switch and ABA return',
    async (kind) => {
      const view = await localCanvas('local-aba-a');
      const oldRead = pendingLocalRead();
      calls.metadata.mockResolvedValueOnce({ lastModified: 1 }).mockResolvedValue({ lastModified: 2 });
      calls.readFile.mockReturnValue(oldRead.promise);
      await act(async () => view.result.current.openPreview('original a', 'markdown', localNote('/synthetic-a')));
      await advanceLocalClock(1000);
      expect(calls.readFile).toHaveBeenCalledTimes(1);
      if (kind === 'project') view.move(project('local-aba-b'));
      await act(async () => view.result.current.openPreview('current b', 'markdown', localNote('/synthetic-b')));
      if (kind === 'project') view.move(project('local-aba-a'));
      await act(async () => view.result.current.openPreview('current a', 'markdown', localNote('/synthetic-a')));
      await act(async () => oldRead.resolve('older a response'));
      expect(view.result.current.activeTab).toMatchObject({
        content: 'current a',
        originalContent: 'current a',
        metadata: { workspace: '/synthetic-a' },
        isDirty: false,
      });
    }
  );
  it('keeps live project switching usable when browser persistence is unavailable', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded');
    });
    const view = await canvas(project('a'));
    act(() => view.result.current.openPreview('live a', 'code', { title: 'a.py' }));
    view.move(project('b'));
    expect(view.result.current.tabs).toEqual([]);
    view.move(project('a'));
    expect(view.result.current.activeTab?.content).toBe('live a');
  });
  it('rejects stale composer, DOM snippet and PDF owner callbacks after a project switch', async () => {
    const view = await canvas(project('a'));
    const stale = view.result.current;
    const pdfOwner = (frameId: string): PdfReadingPreviewOwner => ({
      ownerId: 'owner',
      frameId,
      context: null,
      pending: false,
      link: async () => {},
      unlink: async () => {},
      reportPosition: vi.fn(),
    });
    let unregister!: () => void;
    act(() => {
      unregister = stale.registerPdfReadingOwner(pdfOwner('a'));
    });
    view.move(project('b'));
    const currentComposer = vi.fn();
    act(() => view.result.current.setSendBoxHandler(currentComposer));
    const currentOwner = pdfOwner('b');
    act(() => view.result.current.registerPdfReadingOwner(currentOwner));
    act(() => {
      stale.setSendBoxHandler(null);
      stale.addToSendBox('late source');
      stale.addDomSnippet('p', '<p>old project</p>');
      stale.registerPdfReadingOwner(pdfOwner('late-a'));
      unregister();
    });
    act(() => view.result.current.addToSendBox('current source'));
    expect(currentComposer).toHaveBeenCalledExactlyOnceWith('current source');
    expect(view.result.current.domSnippets).toEqual([]);
    expect(view.result.current.pdfReadingOwner).toBe(currentOwner);
  });
  it('invalidates an old owner immediately on account reset before React receives the new owner', async () => {
    setRendererAccountOwner('one');
    const view = await canvas(project('a', 'one'));
    act(() => view.result.current.openPreview('one', 'code', { title: 'one.py' }));
    const stale = view.result.current;
    act(() => setRendererAccountOwner('two'));
    expect(view.result.current.tabs).toEqual([]);
    act(() => stale.openPreview('late one', 'code', { title: 'late.py' }));
    expect(view.result.current.tabs).toEqual([]);
    view.move(project('a', 'two'));
    act(() => view.result.current.openPreview('two', 'code', { title: 'two.py' }));
    expect(view.result.current.tabs.map((tab) => tab.content)).toEqual(['two']);
    expect(JSON.parse(localStorage.getItem(previewWorkspaceStorageKey(project('a', 'one')))!).tabs[0].content).toBe(
      'one'
    );
  });

  it('keeps a deliberately opened cross-project artifact source exact within the current canvas', async () => {
    const view = await canvas(project('a'));
    act(() =>
      view.result.current.openPreview('reference', 'markdown', {
        projectId: 'b',
        artifactId: 'b-artifact',
        versionId: 'b-version',
        file_name: 'reference.md',
      })
    );
    view.unmount();
    const reloaded = await canvas(project('a'));
    expect(reloaded.result.current.activeTab?.metadata).toMatchObject({
      projectId: 'b',
      artifactId: 'b-artifact',
      versionId: 'b-version',
    });
    reloaded.move(project('b'));
    expect(reloaded.result.current.tabs).toEqual([]);
  });

  it('does not adopt another project or unidentified legacy tabs on a fresh project route', async () => {
    const legacy = JSON.stringify([
      { id: 'old-a', title: 'a.py', content: '# Project A', content_type: 'code', metadata: { projectId: 'a' } },
      { id: 'unknown', title: 'unknown.py', content: '# Unknown project', content_type: 'code' },
    ]);
    localStorage.setItem('x-science_preview_tabs', legacy);
    const i18n = await createTestI18n('en-US');
    const wrapper = ({ children }: React.PropsWithChildren) => (
      <I18nextProvider i18n={i18n}>
        <PreviewProvider workspace={{ ownerId: 'owner', routeKey: '/projects/b', projectId: 'b' }}>
          {children}
        </PreviewProvider>
      </I18nextProvider>
    );
    const view = renderHook(() => usePreviewContext(), { wrapper });
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.openPreview('# B', 'markdown', { projectId: 'b', file_name: 'b.md' }));
    expect(view.result.current.tabs.map((tab) => tab.title)).toEqual(['b.md']);
    expect(localStorage.getItem('x-science_preview_tabs')).toBe(legacy);
  });

  it('restores each project tabs, active file, column count and focus without losing unsaved edits', async () => {
    const view = await canvas(project('a'));
    act(() => {
      view.result.current.openPreview('a1', 'code', { file_name: 'a1.py', projectId: 'a' }, { presentation: 'board' });
      view.result.current.openPreview('a2', 'code', { file_name: 'a2.py', projectId: 'a' }, { presentation: 'board' });
    });
    const aId = view.result.current.activeTabId!;
    act(() => {
      view.result.current.updateContent('unsaved a2');
      view.result.current.setBoardColumns(3);
      view.result.current.setFocusedTabId(aId);
    });
    view.move(project('b'));
    expect(view.result.current.tabs).toEqual([]);
    expect(view.result.current.boardColumns).toBe(2);
    expect(view.result.current.focusedTabId).toBeNull();
    act(() => {
      view.result.current.openPreview('b', 'code', { file_name: 'b.py', projectId: 'b' });
      view.result.current.setBoardColumns(1);
    });
    view.move(project('a'));
    expect(view.result.current.tabs.map((tab) => tab.title)).toEqual(['a1.py', 'a2.py']);
    expect(view.result.current.activeTab).toMatchObject({
      id: aId,
      content: 'unsaved a2',
      originalContent: 'a2',
      isDirty: true,
    });
    expect(view.result.current.presentationMode).toBe('board');
    expect(view.result.current.boardColumns).toBe(3);
    expect(view.result.current.focusedTabId).toBe(aId);
    view.move(project('b'));
    expect(view.result.current.tabs.map((tab) => tab.title)).toEqual(['b.py']);
    expect(view.result.current.boardColumns).toBe(1);
  });

  it('shares a project canvas across sessions and never paints the old project while a route is unresolved', async () => {
    const view = await canvas(session('a1'));
    act(() => view.result.current.bindWorkspaceProject('a1', 'a'));
    act(() => view.result.current.openPreview('a', 'code', { file_name: 'a.py', projectId: 'a' }));
    const id = view.result.current.activeTabId;
    view.move(session('a2'));
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.bindWorkspaceProject('a2', 'a'));
    expect(view.result.current.activeTabId).toBe(id);
    expect(view.result.current.tabs.map((tab) => tab.title)).toEqual(['a.py']);
    expect(view.result.current.isOpen).toBe(true);
    view.move(session('b1'));
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.bindWorkspaceProject('b1', 'b'));
    expect(view.result.current.tabs).toEqual([]);
  });

  it('flushes quick switches to their own keys and restores only the chosen project after reload', async () => {
    const first = await canvas(project('a'));
    act(() => {
      first.result.current.openPreview(
        'original',
        'code',
        { file_name: 'a.py', projectId: 'a' },
        { presentation: 'board' }
      );
      first.result.current.setBoardColumns(3);
    });
    const id = first.result.current.activeTabId;
    act(() => {
      first.result.current.updateContent('draft');
      first.result.current.setFocusedTabId(id);
    });
    first.move(project('b'));
    act(() => first.result.current.openPreview('b', 'markdown', { file_name: 'b.md', projectId: 'b' }));
    first.unmount();
    const reloaded = await canvas(project('a'));
    expect(reloaded.result.current.tabs).toHaveLength(1);
    expect(reloaded.result.current.activeTab).toMatchObject({
      content: 'draft',
      originalContent: 'original',
      isDirty: true,
    });
    expect(reloaded.result.current.boardColumns).toBe(3);
    expect(reloaded.result.current.focusedTabId).toBe(id);
    expect(reloaded.result.current.presentationMode).toBe('board');
    expect(reloaded.result.current.isOpen).toBe(true);
    expect(JSON.parse(localStorage.getItem(previewWorkspaceStorageKey(project('b')))!).tabs[0].title).toBe('b.md');
  });

  it('rejects late route bindings and old open/edit/close actions even after returning to that project', async () => {
    const view = await canvas(session('a'));
    const lateBind = view.result.current.bindWorkspaceProject;
    let finishOldRead!: (projectId: string) => void;
    const oldRead = new Promise<string>((resolve) => {
      finishOldRead = resolve;
    }).then((projectId) => lateBind('a', projectId));
    act(() => lateBind('a', 'a'));
    act(() => view.result.current.openPreview('a', 'code', { file_name: 'a.py' }));
    const stale = view.result.current;
    view.move(session('b'));
    await act(async () => {
      finishOldRead('a');
      await oldRead;
    });
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.bindWorkspaceProject('b', 'b'));
    act(() => view.result.current.openPreview('b', 'code', { file_name: 'b.py' }));
    act(() => {
      stale.openPreview('late a', 'code', { file_name: 'late.py' });
      stale.updateContent('late edit');
      stale.closePreview();
    });
    expect(view.result.current.tabs.map((tab) => tab.content)).toEqual(['b']);
    view.move(session('a-return'));
    act(() => view.result.current.bindWorkspaceProject('a-return', 'a'));
    act(() => stale.updateContent('late a after return'));
    expect(view.result.current.tabs.map((tab) => tab.content)).toEqual(['a']);
  });

  it('cancels a pending artifact edit baseline and ignores its late response after switching away and back', async () => {
    let finish!: (value: { artifactId: string; versionId: string }) => void;
    calls.loadArtifact.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        })
    );
    const view = await canvas(project('a'));
    act(() =>
      view.result.current.openPreview('a', 'markdown', {
        artifactId: 'artifact-a',
        versionId: 'version-a',
        title: 'a.md',
      })
    );
    const id = view.result.current.activeTabId!;
    let pending!: Promise<boolean>;
    act(() => {
      pending = view.result.current.beginContentEdit(id);
    });
    const signal = calls.loadArtifact.mock.calls[0][1].signal as AbortSignal;
    view.move(project('b'));
    expect(signal.aborted).toBe(true);
    view.move(project('a'));
    await act(async () => {
      finish({ artifactId: 'artifact-a', versionId: 'version-a' });
      expect(await pending).toBe(false);
    });
    expect(view.result.current.artifactEditStates[id]).toBeUndefined();
    expect(view.result.current.activeTab).toMatchObject({ content: 'a', metadata: { versionId: 'version-a' } });
  });

  it('isolates owners and local workspace roots, preserves the other owner storage, and separates Library', async () => {
    const view = await canvas(project('a', 'one'));
    act(() => view.result.current.openPreview('owner one', 'markdown', { title: 'one.md' }));
    view.move(project('a', 'two'));
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.openPreview('owner two', 'markdown', { title: 'two.md' }));
    view.move({ ownerId: 'two', routeKey: 'library', area: 'library' });
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.openPreview('library', 'markdown', { title: 'library.md' }));
    view.move({ ownerId: 'two', routeKey: 'workspace-one', workspaceId: '/one' });
    expect(view.result.current.tabs).toEqual([]);
    act(() => view.result.current.openPreview('local', 'code', { title: 'local.py' }));
    view.move({ ownerId: 'two', routeKey: 'workspace-two', workspaceId: '/two' });
    expect(view.result.current.tabs).toEqual([]);
    view.move({ ownerId: 'two', routeKey: 'library', area: 'library' });
    expect(view.result.current.tabs.map((tab) => tab.content)).toEqual(['library']);
    expect(JSON.parse(localStorage.getItem(previewWorkspaceStorageKey(project('a', 'one')))!).tabs[0].content).toBe(
      'owner one'
    );
    view.move(project('a', 'two'));
    expect(view.result.current.tabs.map((tab) => tab.content)).toEqual(['owner two']);
  });

  it('keeps close, active tab and focus changes inside the current project', async () => {
    const view = await canvas(project('a'));
    act(() => {
      view.result.current.openPreview('1', 'code', { title: '1.py' }, { presentation: 'board' });
      view.result.current.openPreview('2', 'code', { title: '2.py' }, { presentation: 'board' });
    });
    const id = view.result.current.activeTabId!;
    act(() => view.result.current.setFocusedTabId(id));
    act(() => view.result.current.closeTab(id));
    expect(view.result.current.focusedTabId).toBeNull();
    expect(view.result.current.activeTab?.title).toBe('1.py');
    expect(view.result.current.presentationMode).toBe('single');
    view.move(project('b'));
    act(() => view.result.current.openPreview('b', 'code', { title: 'b.py' }));
    view.move(project('a'));
    act(() => view.result.current.closePreview());
    expect(view.result.current.tabs).toEqual([]);
    view.move(project('b'));
    expect(view.result.current.tabs.map((tab) => tab.title)).toEqual(['b.py']);
  });

  it('does not restore invalid identities, unsupported or oversized content', async () => {
    const key = previewWorkspaceStorageKey(project('a'));
    const valid = { id: 'valid', title: 'a.py', content: 'a', content_type: 'code', metadata: { projectId: 'a' } };
    localStorage.setItem(
      key,
      JSON.stringify({
        schema: 1,
        scope: key,
        activeTabId: 'missing',
        focusedTabId: 'other',
        boardColumns: 9,
        tabs: [
          valid,
          valid,
          { ...valid, id: 'large', content: 'x'.repeat(80_001) },
          { ...valid, id: 'image', content_type: 'image' },
        ],
      })
    );
    const view = await canvas(project('a'));
    expect(view.result.current.tabs.map((tab) => tab.id)).toEqual(['valid']);
    expect(view.result.current.activeTabId).toBe('valid');
    expect(view.result.current.focusedTabId).toBeNull();
    expect(view.result.current.boardColumns).toBe(2);
  });

  it('keeps an explicitly closed canvas closed after reload without closing another project', async () => {
    const view = await canvas(project('a'));
    act(() => view.result.current.openPreview('a', 'code', { title: 'a.py' }));
    view.move(project('b'));
    act(() => view.result.current.openPreview('b', 'code', { title: 'b.py' }));
    act(() => view.result.current.closePreview());
    view.unmount();
    const closed = await canvas(project('b'));
    expect(closed.result.current.isOpen).toBe(false);
    expect(closed.result.current.tabs).toEqual([]);
    closed.move(project('a'));
    expect(closed.result.current.isOpen).toBe(true);
    expect(closed.result.current.tabs.map((tab) => tab.content)).toEqual(['a']);
  });
});
