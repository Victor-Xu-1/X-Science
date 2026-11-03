import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  usePreviewArtifactEdits,
  type PreviewArtifactSaveOutcome,
} from '@/renderer/pages/conversation/Preview/context/usePreviewArtifactEdits';
import type { PreviewTab } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { XScienceHttpError } from '@/renderer/services/xScienceHttp';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const calls = vi.hoisted(() => ({ head: vi.fn(), save: vi.fn(), content: vi.fn() }));
vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceArtifact: (...args: unknown[]) => calls.head(...args),
}));
vi.mock('@/renderer/services/xScienceArtifacts', () => ({
  createXScienceTextArtifactVersion: (...args: unknown[]) => calls.save(...args),
  loadXScienceArtifactVersionText: (...args: unknown[]) => calls.content(...args),
  getXScienceArtifactVersionContentUrl: (id: string) => `/api/artifacts/versions/${id}`,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
function tab(versionId = 'v1', content = 'draft'): PreviewTab {
  return {
    id: 'tab',
    title: 'report.md',
    content_type: 'markdown',
    content,
    metadata: { artifactId: 'artifact', versionId, file_name: 'report.md' },
  };
}
function fixture(initial = tab()) {
  const commit = vi.fn(),
    open = vi.fn();
  calls.head.mockResolvedValue({ artifactId: 'artifact', versionId: 'v1' });
  calls.save.mockResolvedValue({ artifactId: 'artifact', versionId: 'v2' });
  calls.content.mockResolvedValue('newer content');
  return {
    commit,
    open,
    ...renderHook(
      ({ tabs, activeTabId }) => usePreviewArtifactEdits({ tabs, activeTabId, commit, open, presentation: 'board' }),
      {
        initialProps: { tabs: [initial], activeTabId: 'tab' as string | null },
      }
    ),
  };
}

describe('PreviewContext remote artifact edit ownership', () => {
  afterEach(() => {
    vi.resetAllMocks();
    resetRendererAccountScopeForTest();
  });

  it('shows an unavailable edit when the displayed immutable source version is unknown', async () => {
    const initial = tab();
    delete initial.metadata!.versionId;
    const f = fixture(initial);
    await act(async () => {
      expect(await f.result.current.begin('tab')).toBe(false);
    });
    expect(f.result.current.states.tab?.status).toBe('error');
    expect(calls.head).not.toHaveBeenCalled();
    expect(calls.save).not.toHaveBeenCalled();
  });

  it('captures the original head and reuses the exact operation after an unknown response', async () => {
    const f = fixture();
    await act(async () => {
      expect(await f.result.current.begin('tab')).toBe(true);
    });
    calls.save.mockRejectedValueOnce(new TypeError('response lost'));
    await act(async () => {
      expect(await f.result.current.save('tab', 'draft')).toEqual({ kind: 'unconfirmed' });
    });
    expect(f.result.current.states.tab?.status).toBe('error');
    await act(async () => {
      expect(await f.result.current.save('tab', 'draft')).toMatchObject({
        kind: 'published',
        originApplied: true,
        draftClean: true,
      });
    });
    const first = calls.save.mock.calls[0]![0];
    expect(calls.save.mock.calls[1]![0]).toEqual(first);
    expect(first).toMatchObject({
      parentVersionId: 'v1',
      expectedHeadVersionId: 'v1',
      operationId: expect.any(String),
    });
    expect(calls.head).toHaveBeenCalledTimes(1);
    expect(f.commit).toHaveBeenCalledTimes(1);
  });

  it('keeps the draft on conflict and opens the exact newer version separately', async () => {
    const f = fixture();
    await act(async () => {
      await f.result.current.begin('tab');
    });
    calls.save.mockRejectedValueOnce(
      new XScienceHttpError({
        path: '/api/artifacts/artifact/versions',
        status: 409,
        body: { code: 'ARTIFACT_HEAD_CONFLICT', details: { artifact_id: 'artifact', current_head_version_id: 'v2' } },
      })
    );
    await act(async () => {
      expect(await f.result.current.save('tab', 'draft')).toEqual({ kind: 'conflict' });
    });
    expect(f.commit).not.toHaveBeenCalled();
    expect(f.result.current.states.tab).toMatchObject({ status: 'conflict', currentHeadVersionId: 'v2' });
    await act(async () => {
      await f.result.current.openLatest('tab');
    });
    expect(calls.content).toHaveBeenCalledWith('v2', expect.objectContaining({ signal: expect.any(AbortSignal) }));
    expect(f.open).toHaveBeenCalledWith(
      'newer content',
      'markdown',
      expect.objectContaining({ artifactId: 'artifact', versionId: 'v2' }),
      { presentation: 'board' }
    );
    expect(f.commit).not.toHaveBeenCalled();
  });

  it('restores unchanged historical content with its parent and a separately captured current head', async () => {
    const f = fixture(tab('v1', 'history'));
    calls.head.mockResolvedValue({ artifactId: 'artifact', versionId: 'v2' });
    calls.save.mockResolvedValue({ artifactId: 'artifact', versionId: 'v3' });
    await act(async () => {
      await f.result.current.begin('tab');
    });
    expect(f.result.current.states.tab?.restoring).toBe(true);
    await act(async () => {
      expect(await f.result.current.save('tab', 'history')).toMatchObject({
        kind: 'published',
        originApplied: true,
        draftClean: true,
      });
    });
    expect(calls.save).toHaveBeenCalledWith(
      expect.objectContaining({ parentVersionId: 'v1', expectedHeadVersionId: 'v2', content: 'history' })
    );
  });

  it.each(['closed', 'source-switched', 'account-switched', 'cancelled'] as const)(
    'ignores a late accepted save after %s',
    async (change) => {
      const f = fixture();
      const pending = deferred<{ artifactId: string; versionId: string }>();
      calls.save.mockReturnValue(pending.promise);
      await act(async () => {
        await f.result.current.begin('tab');
      });
      let saving!: Promise<PreviewArtifactSaveOutcome>;
      act(() => {
        saving = f.result.current.save('tab', 'draft');
      });
      if (change === 'closed') f.rerender({ tabs: [], activeTabId: null });
      if (change === 'source-switched')
        f.rerender({
          tabs: [{ ...tab(), metadata: { artifactId: 'another', versionId: 'other' } }],
          activeTabId: 'tab',
        });
      if (change === 'account-switched') {
        setRendererAccountOwner('another-owner');
        f.rerender({ tabs: [tab()], activeTabId: 'tab' });
      }
      if (change === 'cancelled') act(() => f.result.current.cancel('tab'));
      await act(async () => {
        pending.resolve({ artifactId: 'artifact', versionId: 'v2' });
        expect(await saving).toMatchObject({ kind: 'published', originApplied: false, draftClean: false });
      });
      expect(f.commit).not.toHaveBeenCalled();
      expect(f.result.current.states.tab).toBeUndefined();
    }
  );

  it('never overwrites draft bytes typed during a pending save', async () => {
    const f = fixture();
    const pending = deferred<{ artifactId: string; versionId: string }>();
    calls.save.mockReturnValue(pending.promise);
    await act(async () => {
      await f.result.current.begin('tab');
    });
    let saving!: Promise<PreviewArtifactSaveOutcome>;
    act(() => {
      saving = f.result.current.save('tab', 'draft');
    });
    f.rerender({ tabs: [tab('v1', 'newer draft')], activeTabId: 'tab' });
    await act(async () => {
      pending.resolve({ artifactId: 'artifact', versionId: 'v2' });
      expect(await saving).toMatchObject({ kind: 'published', originApplied: true, draftClean: false });
    });
    expect(f.commit).toHaveBeenCalledWith(
      'tab',
      'v1',
      'draft',
      expect.objectContaining({ versionId: 'v2' }),
      expect.any(String)
    );
  });

  it('does not start an editor from a late head read for an inactive tab', async () => {
    const f = fixture();
    const pending = deferred<{ artifactId: string; versionId: string }>();
    calls.head.mockReturnValue(pending.promise);
    let starting!: Promise<boolean>;
    act(() => {
      starting = f.result.current.begin('tab');
    });
    f.rerender({ tabs: [tab()], activeTabId: 'another-tab' });
    await act(async () => {
      pending.resolve({ artifactId: 'artifact', versionId: 'v1' });
      expect(await starting).toBe(false);
    });
    expect(f.result.current.states.tab).toBeUndefined();
    expect(calls.save).not.toHaveBeenCalled();
  });

  it.each(['another-tab', 'away-and-back'])(
    'does not activate a late newer-version read after navigation %s',
    async (navigation) => {
      const f = fixture();
      await act(async () => {
        await f.result.current.begin('tab');
      });
      calls.save.mockRejectedValueOnce(
        new XScienceHttpError({
          path: '/api/artifacts/artifact/versions',
          status: 409,
          body: { code: 'ARTIFACT_HEAD_CONFLICT', details: { artifact_id: 'artifact', current_head_version_id: 'v2' } },
        })
      );
      await act(async () => {
        await f.result.current.save('tab', 'draft');
      });
      const reading = deferred<string>();
      calls.content.mockReturnValue(reading.promise);
      let pending!: Promise<void>;
      act(() => {
        pending = f.result.current.openLatest('tab');
      });
      f.rerender({ tabs: [tab()], activeTabId: 'another-tab' });
      if (navigation === 'away-and-back') f.rerender({ tabs: [tab()], activeTabId: 'tab' });
      await act(async () => {
        reading.resolve('late newer content');
        await pending;
      });
      expect(f.open).not.toHaveBeenCalled();
    }
  );

  it('publishes a clean original tab while another tab is active', async () => {
    const f = fixture();
    await act(async () => {
      await f.result.current.begin('tab');
    });
    f.rerender({ tabs: [tab()], activeTabId: 'another-tab' });
    await act(async () => {
      expect(await f.result.current.save('tab', 'draft')).toMatchObject({
        kind: 'published',
        originApplied: true,
        draftClean: true,
      });
    });
    expect(f.commit).toHaveBeenCalledTimes(1);
  });
});
