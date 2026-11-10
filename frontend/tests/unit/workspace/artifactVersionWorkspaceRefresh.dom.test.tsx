/**
 * @license
 * Copyright 2026 X-Science contributors
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkspaceArtifactPage } from '@/common/adapter/workspaceMapper';
import { useWorkspaceTree } from '@/renderer/pages/conversation/Workspace/hooks/useWorkspaceTree';
import { useWorkspaceEvents } from '@/renderer/pages/conversation/Workspace/hooks/useWorkspaceEvents';
import { createXScienceTextArtifactVersion } from '@/renderer/services/xScienceArtifacts';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { emitter } from '@/renderer/utils/emitter';

const mocks = vi.hoisted(() => ({
  page: vi.fn(),
  auth: { id: 'owner' },
}));
const noop = () => {};
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      getWorkspacePage: { invoke: mocks.page },
      turnCompleted: { on: () => () => {} },
      responseSearchWorkSpace: { provider: () => () => {} },
    },
    acpConversation: { responseStream: { on: () => () => {} } },
    runtime: { statusChanged: { on: () => () => {} } },
  },
}));
vi.mock('@/renderer/hooks/context/AuthContext', () => ({ useAuth: () => ({ user: mocks.auth }) }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((accept) => (resolve = accept));
  return { promise, resolve };
}

let sequence = 0;
let scope: { workspace: string; conversation_id: string; projectId: string; eventPrefix: 'acp' };
let serverPage: WorkspaceArtifactPage;
let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

function page(versionId: string, sizeBytes: number): WorkspaceArtifactPage {
  return {
    items: [
      {
        name: 'project-files',
        fullPath: `${scope.workspace}/project-files`,
        relativePath: 'project-files',
        isFile: false,
        isDir: true,
        children: [
          {
            name: 'report.md',
            fullPath: `${scope.workspace}/project-files/report.md`,
            relativePath: 'project-files/report.md',
            isFile: true,
            isDir: false,
            artifactId: 'artifact-1',
            versionId,
            sizeBytes,
          },
        ],
      },
    ],
    total: 1,
    hasMore: false,
  };
}

function useWorkspace() {
  const tree = useWorkspaceTree(scope);
  useWorkspaceEvents({
    ...scope,
    ...tree,
    closeContextMenu: noop,
    setContextMenu: noop,
    closeRenameModal: noop,
    closeDeleteModal: noop,
  });
  return tree;
}

async function mount() {
  const view = renderHook(useWorkspace);
  await act(async () => {
    await Promise.resolve();
  });
  return view;
}

function save() {
  return createXScienceTextArtifactVersion({
    artifactId: 'artifact-1',
    content: 'updated',
    contentType: 'text/markdown',
    parentVersionId: 'version-1',
    expectedHeadVersionId: 'version-1',
    operationId: 'save-operation',
  });
}

function acknowledge(value: unknown = { artifact_id: 'artifact-1', version_id: 'version-2' }, status = 201) {
  fetchMock.mockResolvedValueOnce(Response.json(value, { status }));
}

describe('conditional version publication refreshes the mounted artifact library owner', () => {
  beforeEach(() => {
    sequence += 1;
    mocks.auth = { id: `artifact-refresh-owner-${sequence}` };
    setRendererAccountOwner(mocks.auth.id);
    scope = {
      workspace: `x-science://artifact-refresh-project-${sequence}`,
      projectId: `artifact-refresh-project-${sequence}`,
      conversation_id: `artifact-refresh-conversation-${sequence}`,
      eventPrefix: 'acp',
    };
    serverPage = page('version-1', 89);
    mocks.page.mockReset().mockImplementation(async () => serverPage);
    fetchMock = vi.fn<typeof fetch>().mockRejectedValue(new Error('unexpected HTTP request'));
    vi.stubGlobal('fetch', fetchMock);
  });
  afterEach(() => {
    cleanup();
    emitter.removeAllListeners('acp.workspace.refresh');
    vi.unstubAllGlobals();
  });

  it('re-reads the current query after a real conditional-save ACK without waiting for a tool or terminal event', async () => {
    const view = await mount();
    await act(async () => {
      await view.result.current.searchWorkspace('kept-filter');
    });
    expect(mocks.page).toHaveBeenCalledTimes(2);
    expect(view.result.current.files[0]?.children?.[0]).toMatchObject({ versionId: 'version-1', sizeBytes: 89 });
    serverPage = page('version-2', 126);
    acknowledge();
    await act(async () => {
      await expect(save()).resolves.toMatchObject({ versionId: 'version-2' });
    });
    expect(mocks.page).toHaveBeenCalledTimes(3);
    expect(mocks.page.mock.calls[2]?.[0]).toMatchObject({ search: 'kept-filter', limit: 24 });
    expect(mocks.page.mock.calls[2]?.[0]).not.toHaveProperty('cursor');
    expect(view.result.current.files[0]?.children?.[0]).toMatchObject({ versionId: 'version-2', sizeBytes: 126 });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(String(url)).toContain('/api/artifacts/artifact-1/versions');
    expect(JSON.parse(String(init?.body))).toMatchObject({
      parent_version_id: 'version-1',
      expected_head_version_id: 'version-1',
    });
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe('save-operation');
  });

  it('keeps the last committed page when the conditional save fails', async () => {
    const view = await mount();
    const refresh = vi.fn();
    emitter.on('acp.workspace.refresh', refresh);
    acknowledge({ detail: 'head conflict' }, 409);
    await act(async () => {
      await expect(save()).rejects.toBeInstanceOf(Error);
    });
    expect(refresh).not.toHaveBeenCalled();
    expect(mocks.page).toHaveBeenCalledTimes(1);
    expect(view.result.current.files[0]?.children?.[0]?.versionId).toBe('version-1');
  });

  it.each([
    { artifact_id: 'other-artifact', version_id: 'version-2' },
    { artifact_id: 'artifact-1', version_id: null },
  ])('does not refresh for a mismatched or incomplete publication identity: %j', async (ack) => {
    await mount();
    const refresh = vi.fn();
    emitter.on('acp.workspace.refresh', refresh);
    acknowledge(ack);
    await act(async () => {
      await save();
    });
    expect(refresh).not.toHaveBeenCalled();
    expect(mocks.page).toHaveBeenCalledTimes(1);
  });

  it('does not refresh the next account when a previous-account save ACK arrives late', async () => {
    const view = await mount();
    const response = deferred<Response>();
    fetchMock.mockReturnValueOnce(response.promise);
    const pendingSave = save();
    mocks.auth = { id: `next-owner-${sequence}` };
    setRendererAccountOwner(mocks.auth.id);
    serverPage = page('next-account-version', 44);
    view.rerender();
    await act(async () => {
      await Promise.resolve();
    });
    expect(mocks.page).toHaveBeenCalledTimes(2);
    const refresh = vi.fn();
    emitter.on('acp.workspace.refresh', refresh);
    await act(async () => {
      response.resolve(Response.json({ artifact_id: 'artifact-1', version_id: 'version-2' }, { status: 201 }));
      await pendingSave;
    });
    expect(refresh).not.toHaveBeenCalled();
    expect(mocks.page).toHaveBeenCalledTimes(2);
    expect(view.result.current.files[0]?.children?.[0]?.versionId).toBe('next-account-version');
  });

  it('retires an older in-flight page and its cache before accepting the post-save page', async () => {
    const view = await mount();
    const oldPage = deferred<WorkspaceArtifactPage>();
    mocks.page.mockReturnValueOnce(oldPage.promise);
    let oldRead!: Promise<unknown>;
    act(() => {
      oldRead = view.result.current.refreshWorkspace();
    });
    const oldSignal = mocks.page.mock.calls[1]?.[1] as AbortSignal;
    serverPage = page('version-2', 126);
    acknowledge();
    await act(async () => {
      await save();
    });
    expect(mocks.page).toHaveBeenCalledTimes(3);
    expect(oldSignal.aborted).toBe(true);
    expect(view.result.current.files[0]?.children?.[0]?.versionId).toBe('version-2');
    await act(async () => {
      oldPage.resolve(page('version-1', 89));
      await oldRead;
    });
    expect(view.result.current.files[0]?.children?.[0]?.versionId).toBe('version-2');
    view.unmount();
    const revisit = await mount();
    expect(mocks.page).toHaveBeenCalledTimes(3);
    expect(revisit.result.current.files[0]?.children?.[0]?.versionId).toBe('version-2');
  });

  it('preserves the committed result when a refresh listener throws', async () => {
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const brokenListener = vi.fn(() => {
      throw new Error('private listener payload must not be logged');
    });
    // Register before the workspace consumer so the throw prevents its refresh.
    emitter.on('acp.workspace.refresh', brokenListener);
    try {
      const view = await mount();
      serverPage = page('version-2', 126);
      acknowledge();
      await act(async () => {
        await expect(save()).resolves.toMatchObject({ artifactId: 'artifact-1', versionId: 'version-2' });
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(brokenListener).toHaveBeenCalledTimes(1);
      expect(mocks.page).toHaveBeenCalledTimes(1);
      expect(view.result.current.files[0]?.children?.[0]?.versionId).toBe('version-1');
      expect(warning.mock.calls).toEqual([
        ['[xScienceArtifacts] Version saved; workspace refresh notification failed.'],
      ]);
    } finally {
      warning.mockRestore();
    }
  });
});
