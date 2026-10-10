import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useArtifactContextReads } from '@/renderer/pages/artifact/useArtifactContextReads';

const api = vi.hoisted(() => ({ project: vi.fn(), benches: vi.fn() }));
vi.mock('@/renderer/services/synonBiomedGateway', () => ({
  loadSynonBiomedProject: api.project,
  loadSynonBiomedProjectBenches: api.benches,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
}

beforeEach(() => {
  api.project
    .mockReset()
    .mockImplementation(async (projectId: string) => ({ projectId, name: `Project ${projectId}` }));
  api.benches
    .mockReset()
    .mockImplementation(async (projectId: string) => [{ frameId: projectId, projectId, name: `Task ${projectId}` }]);
});
afterEach(() => vi.useRealTimers());

describe('artifact context name reads', () => {
  it('does not invent requests for unlinked projects or after queued unmount', async () => {
    const noProject = renderHook(() => useArtifactContextReads(null, 'known-task'));
    expect(noProject.result.current.task).toEqual({ status: 'ready', value: null });
    noProject.unmount();
    const queued = renderHook(() => useArtifactContextReads('project-a', 'project-a'));
    queued.unmount();
    await act(async () => {
      await Promise.resolve();
    });
    expect(api.project).not.toHaveBeenCalled();
    expect(api.benches).not.toHaveBeenCalled();
  });

  it('keeps independent failures and coalesces retry without rereading the healthy peer', async () => {
    api.project.mockRejectedValueOnce(new Error('metadata unavailable'));
    const { result } = renderHook(() => useArtifactContextReads('project-a', 'project-a'));
    await waitFor(() => expect(result.current.project.status).toBe('failed'));
    expect(result.current.task.value?.name).toBe('Task project-a');
    const pending = deferred<{ projectId: string; name: string }>();
    api.project.mockReturnValueOnce(pending.promise);
    act(() => {
      result.current.retry('project');
      result.current.retry('project');
    });
    await waitFor(() => expect(api.project).toHaveBeenCalledTimes(2));
    expect(api.benches).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.resolve({ projectId: 'project-a', name: 'Recovered' });
    });
    expect(result.current.project.value?.name).toBe('Recovered');
  });

  it('aborts obsolete ownership and rejects a late previous-context name', async () => {
    const old = deferred<{ projectId: string; name: string }>();
    api.project.mockReturnValueOnce(old.promise);
    const { result, rerender } = renderHook(({ id }) => useArtifactContextReads(id, id), { initialProps: { id: 'a' } });
    await waitFor(() => expect(api.project).toHaveBeenCalledTimes(1));
    const signal = api.project.mock.calls[0][1].signal as AbortSignal;
    rerender({ id: 'b' });
    await waitFor(() => expect(result.current.project.value?.name).toBe('Project b'));
    expect(signal.aborted).toBe(true);
    await act(async () => {
      old.resolve({ projectId: 'a', name: 'Obsolete name' });
    });
    expect(result.current.project.value?.name).toBe('Project b');
    expect(result.current.task.value?.name).toBe('Task b');
  });

  it('does not substitute another task with a shared root or mismatched project', async () => {
    api.benches.mockResolvedValueOnce([
      { frameId: 'other', rootFrameId: 'expected', projectId: 'p', name: 'Wrong task' },
    ]);
    const first = renderHook(() => useArtifactContextReads('p', 'expected'));
    await waitFor(() => expect(first.result.current.task.status).toBe('ready'));
    expect(first.result.current.task.value).toBeNull();
    first.unmount();
    api.benches.mockResolvedValueOnce([{ frameId: 'expected', projectId: 'another-project', name: 'Wrong owner' }]);
    const second = renderHook(() => useArtifactContextReads('p', 'expected'));
    await waitFor(() => expect(second.result.current.task.status).toBe('failed'));
    expect(second.result.current.task.value).toBeNull();
  });

  it('does not reread unchanged context on an unrelated rendering revision', async () => {
    const { result, rerender } = renderHook(
      ({ revision }) => {
        void revision;
        return useArtifactContextReads('a', 'a');
      },
      { initialProps: { revision: 0 } }
    );
    await waitFor(() => expect(result.current.task.status).toBe('ready'));
    rerender({ revision: 1 });
    expect(api.project).toHaveBeenCalledTimes(1);
    expect(api.benches).toHaveBeenCalledTimes(1);
  });

  it('ends an owned slow read at the deadline without automatic retries', async () => {
    vi.useFakeTimers();
    api.project.mockImplementationOnce(
      (_id: string, { signal }: { signal: AbortSignal }) =>
        new Promise((_resolve, reject) =>
          signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
        )
    );
    const view = renderHook(() => useArtifactContextReads('a', 'a'));
    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(view.result.current.project.status).toBe('failed');
    expect(view.result.current.task.status).toBe('ready');
    expect(api.project).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
