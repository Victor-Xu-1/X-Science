import React from 'react';
import { act, cleanup, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { bindRealtimeRuntime } from '@/common/adapter/httpBridge';
import { parseRealtimeMessage } from '@/common/adapter/realtimeProtocol';
import { createRealtimeRuntime, type RealtimeRuntimeClientObservers } from '@/common/adapter/realtimeRuntime';
import { clearSharedGatewayReads } from '@/renderer/services/xScienceGateway';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { emitter } from '@/renderer/utils/emitter';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/conversation/project-task' }),
}));
vi.mock('@/renderer/services/projectSidebarOrder', () => ({
  loadProjectSidebarOrder: async (): Promise<string[]> => [],
  saveProjectSidebarOrder: async (): Promise<void> => undefined,
}));

import ProjectHistorySection from '@/renderer/pages/conversation/GroupedHistory/ProjectHistorySection';

const response = (count: number) =>
  new Response(
    JSON.stringify({
      projects: [
        {
          project_id: 'project',
          name: 'Canonical project',
          conversation_count: 4,
          artifact_count: count,
        },
      ],
    }),
    { status: 200, headers: { 'Content-Type': 'application/json' } }
  );
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
let releaseRuntime: (() => void) | undefined;
afterEach(() => {
  cleanup();
  releaseRuntime?.();
  releaseRuntime = undefined;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  resetRendererAccountScopeForTest();
  clearSharedGatewayReads();
});

it('rereads canonical project totals on a durable artifact event and fences stale reads and cleanup', async () => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('project-owner');
  clearSharedGatewayReads();
  const older = deferred<Response>();
  const previousAccount = deferred<Response>();
  const reads: Array<RequestInit | undefined> = [];
  const fetchMock = vi.fn((input: RequestInfo | URL, options?: RequestInit) => {
    expect(new URL(String(input), 'http://localhost').pathname).toBe('/api/projects');
    reads.push(options);
    return reads.length === 1
      ? Promise.resolve(response(83))
      : reads.length === 2
        ? older.promise
        : reads.length === 3
          ? Promise.resolve(response(41))
          : previousAccount.promise;
  });
  vi.stubGlobal('fetch', fetchMock);
  let observers!: RealtimeRuntimeClientObservers;
  const runtime = createRealtimeRuntime({
    createClient: (next) => {
      observers = next;
      return { setIdentity: async () => undefined, dispose: () => undefined };
    },
  });
  await runtime.setIdentity('project-owner');
  const unbind = bindRealtimeRuntime(runtime);
  releaseRuntime = () => {
    unbind();
    runtime.dispose();
  };
  let sequence = 0;
  const artifactCommitted = () => {
    sequence += 1;
    const parsed = parseRealtimeMessage(
      JSON.stringify({
        type: 'artifact_created',
        project_id: 'project',
        artifact_id: 'artifact',
        version_id: 'version',
        _event_id: `artifact-${sequence}`,
        _sequence: sequence,
        _kind: 'composite',
      })
    );
    if (!parsed.ok) throw new Error('Controlled artifact event must satisfy the real protocol');
    observers.onMessage(parsed.message);
  };
  const view = await renderWithI18n(<ProjectHistorySection collapsed={false} activeProjectId='project' />);
  expect(await screen.findByRole('button', { name: 'Canonical project' })).toHaveTextContent('83 个文件');
  act(() => emitter.emit('xscience.projects.refresh'));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  act(artifactCommitted);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Canonical project' })).toHaveTextContent('41 个文件'));
  expect(reads[1]?.signal?.aborted).toBe(true);
  await act(async () => {
    older.resolve(response(999));
    await older.promise;
  });
  expect(screen.getByRole('button', { name: 'Canonical project' })).toHaveTextContent('41 个文件');
  act(() => emitter.emit('xscience.projects.refresh'));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4));
  act(() => setRendererAccountOwner('other-owner'));
  await act(async () => {
    previousAccount.resolve(response(501));
    await previousAccount.promise;
  });
  expect(screen.getByRole('button', { name: 'Canonical project' })).toHaveTextContent('41 个文件');
  view.unmount();
  expect(reads[3]?.signal?.aborted).toBe(true);
  act(artifactCommitted);
  expect(fetchMock).toHaveBeenCalledTimes(4);
});
