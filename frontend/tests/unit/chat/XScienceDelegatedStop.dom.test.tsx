import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { XScienceDelegateDock } from '@/renderer/pages/conversation/components/XScienceDelegateDock';
import { selectXScienceBranch } from '@/renderer/services/xScienceConversationBranches';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import type { XScienceDelegateFrame, XScienceDelegateLineage } from '@/renderer/services/xScienceDelegateLineage';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/hooks/context/LayoutContext', () => ({
  useLayoutContext: () => ({ isMobile: false }),
}));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/renderer/services/xScienceConversationWake', () => ({
  subscribeXScienceConversationWake: () => () => {},
}));

// Wire-only fixture: no import from the future delegated-Stop implementation.
// The actual Dock and its actual Drawer must remain executable on the baseline.
const parentId = 'parent-frame';
const branchId = 'br_1234abcd';
const scope = {
  parent_frame_id: parentId,
  parent_frame_incarnation_id: 'parent-incarnation',
  root_frame_id: 'root-frame',
  root_frame_incarnation_id: 'root-incarnation',
};
const branch = { branch_id: branchId, generation: 7 };
const target = {
  frame_id: 'child-review',
  frame_incarnation_id: 'child-review-incarnation',
  stream_uid: 'child-review-stream',
  stream_epoch: 3,
  input_message_id: 'child-review-input',
  input_revision: 4,
  runner_attempt: 2,
};
type FrozenRequest = {
  schema: 'x-science.delegated-stop.v1';
  request_id: string;
  scope: typeof scope;
  branch: typeof branch;
  parent_input_revision: number;
  expected_fence_generation: number;
  mode: 'branch' | 'child';
  target?: typeof target;
};
const snapshot = {
  schema: 'x-science.delegated-stop-snapshot.v1',
  scope,
  branch,
  parent_input_revision: 9,
  fence_generation: 2,
  can_stop_branch: true,
  targets: [
    {
      target,
      frame_id: target.frame_id,
      origin: 'verified',
      in_selected_branch: true,
      can_stop: true,
      status: 'running',
    },
    {
      target: null,
      frame_id: 'other-branch-child',
      origin: 'verified',
      in_selected_branch: false,
      can_stop: false,
      status: 'running',
    },
  ],
};

const frame = (frameId: string, label: string, ordinal: number): XScienceDelegateFrame => ({
  frameId,
  rootFrameId: scope.root_frame_id,
  parentFrameId: frameId === scope.root_frame_id ? null : frameId === parentId ? scope.root_frame_id : parentId,
  ordinal,
  label,
  agentName: 'RESEARCHER',
  status: frameId === parentId || frameId === scope.root_frame_id ? 'completed' : 'running',
  statusDescription: null,
  taskSummary: null,
  messageCount: 1,
  directChildCount: 0,
});
const children = [frame(target.frame_id, 'Literature review', 1), frame('other-branch-child', 'Other branch', 2)];
const lineage: XScienceDelegateLineage = {
  rootFrameId: scope.root_frame_id,
  currentFrameId: parentId,
  current: frame(parentId, 'Parent', 0),
  ancestors: [frame(scope.root_frame_id, 'Root', 0)],
  directChildren: children,
  rootChildren: [frame(parentId, 'Parent', 1)],
  totals: { needsInput: 0, running: 2, failed: 0, completed: 0, stopped: 0 },
};
const receipt = (request: FrozenRequest, settled = false) => ({
  schema: 'x-science.delegated-stop-receipt.v1',
  request,
  accepted_at: '2026-10-19T01:00:00Z',
  state: settled ? 'settled' : 'stopping',
  replayed: false,
  targets: [
    {
      target,
      outcome: settled ? 'cancelled' : 'cancel_requested',
      execution_observation: settled ? 'unavailable' : 'running',
    },
  ],
});
const jsonResponse = (payload: unknown, status = 200): Response =>
  new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } });

const requests: FrozenRequest[] = [];
const requestBodies: string[] = [];
const receiptReads: string[] = [];
const snapshotBranches: Array<string | null> = [];
const broadCancelPaths: string[] = [];
let postResponse: (request: FrozenRequest) => Promise<Response>;

async function renderDock() {
  // Extra identity props are carried by an ordinary object spread, which also
  // compiles against the old component's conversationId/polling-only Props.
  const props = {
    conversationId: parentId,
    polling: false,
    ownerId: 'account-a',
    frameIncarnationId: scope.parent_frame_incarnation_id,
    rootTurnBusy: false,
  };
  return renderWithI18n(
    <MemoryRouter>
      <XScienceDelegateDock {...props} />
    </MemoryRouter>,
    'en-US'
  );
}

describe('delegated Stop actual entry contract', () => {
  beforeEach(() => {
    // Each case is a distinct task despite reusing these fixture coordinates.
    // Production intentionally retains unresolved requests across unmount/login.
    sessionStorage.removeItem(
      'xscience.delegated-stop-hints.v1:' + JSON.stringify(['account-a', parentId, scope.parent_frame_incarnation_id])
    );
    requests.length = 0;
    requestBodies.length = 0;
    receiptReads.length = 0;
    snapshotBranches.length = 0;
    broadCancelPaths.length = 0;
    setRendererAccountOwner('account-a');
    selectXScienceBranch(parentId, branchId);
    postResponse = async (request) => jsonResponse(receipt(request));
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const address = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
        const url = new URL(address, 'http://localhost');
        const method = init?.method?.toUpperCase() ?? 'GET';
        if (url.pathname.endsWith('/cancel')) {
          broadCancelPaths.push(url.pathname);
          throw new Error('Scoped delegated Stop must never call the ordinary root-tree cancel endpoint');
        }
        if (url.pathname === `/api/conversations/${parentId}/lineage`) return jsonResponse(lineage);
        if (url.pathname === `/api/conversations/${target.frame_id}/messages`) {
          return jsonResponse({
            items: [{ id: 'saved-output', type: 'text', position: 'left', content: 'Retained child output' }],
            has_more_before: false,
          });
        }
        if (url.pathname === `/api/frames/${parentId}/delegated-stops`) {
          if (method === 'GET') {
            snapshotBranches.push(url.searchParams.get('branch_id'));
            return jsonResponse(snapshot);
          }
          if (method === 'POST' && typeof init?.body === 'string') {
            const request = JSON.parse(init.body) as FrozenRequest;
            requestBodies.push(init.body);
            requests.push(request);
            return postResponse(request);
          }
        }
        const receiptPrefix = `/api/frames/${parentId}/delegated-stops/`;
        if (method === 'GET' && url.pathname.startsWith(receiptPrefix)) {
          receiptReads.push(decodeURIComponent(url.pathname.slice(receiptPrefix.length)));
          return jsonResponse({ code: 'DELEGATED_STOP_UNAVAILABLE', message: 'Stop request was not found' }, 404);
        }
        throw new Error(`Unexpected request ${method} ${url.pathname}`);
      })
    );
  });

  afterEach(() => {
    cleanup();
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('offers selected-branch Stop separately from root Stop when the parent is idle', async () => {
    await renderDock();
    await screen.findByRole('button', { name: '2 subagents, open list' });
    const stop = await screen.findByRole('button', { name: 'Stop subagents' });
    fireEvent.click(stop);
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(snapshotBranches).toContain(branchId);
    expect(requests[0]).toEqual({
      schema: 'x-science.delegated-stop.v1',
      request_id: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i),
      scope,
      branch,
      parent_input_revision: 9,
      expected_fence_generation: 2,
      mode: 'branch',
    });
    expect(broadCancelPaths).toEqual([]);
    expect(screen.queryByText(/^Stopped$/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '2 subagents, open list' })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Retry stop' })).toBeEnabled();
    expect(requests).toHaveLength(1);
  });

  it('reserves one exact child Stop from the real preview and keeps its output', async () => {
    let complete!: (response: Response) => void;
    postResponse = () =>
      new Promise((resolve) => {
        complete = resolve;
      });
    await renderDock();
    fireEvent.click(await screen.findByRole('button', { name: '2 subagents, open list' }));
    fireEvent.click(screen.getByRole('button', { name: 'Open subagent 1 Literature review' }));
    expect(await screen.findByText('Retained child output')).toBeInTheDocument();
    const stop = await screen.findByRole('button', { name: 'Stop this subagent' });
    fireEvent.click(stop);
    fireEvent.click(stop);
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0]).toEqual({
      schema: 'x-science.delegated-stop.v1',
      request_id: expect.any(String),
      scope,
      branch,
      parent_input_revision: 9,
      expected_fence_generation: 2,
      mode: 'child',
      target,
    });
    expect(stop).toBeDisabled();
    expect(broadCancelPaths).toEqual([]);
    expect(screen.getByText('Retained child output')).toBeInTheDocument();
    await act(async () => {
      complete(jsonResponse(receipt(requests[0], true)));
    });
    expect(screen.getByText('Retained child output')).toBeInTheDocument();
    expect(screen.queryByText(/^Stopped$/)).not.toBeInTheDocument();
  });

  it('keeps a lost acknowledgement uncertain after 404 and retries only the original UUID and body', async () => {
    postResponse = async (request) => {
      if (requests.length === 1) throw new Error('lost Stop acknowledgement');
      return jsonResponse(receipt(request, true));
    };
    await renderDock();
    // The optional lineage read replaces the provisional recovery surface.
    // Click the current, enabled Dock control rather than an earlier loading node.
    await screen.findByRole('button', { name: '2 subagents, open list' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Stop subagents' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Stop subagents' }));
    await waitFor(() => expect(requests).toHaveLength(1));
    const check = await screen.findByRole('button', { name: 'Check stop request' });
    expect(requests).toHaveLength(1);
    const originalBody = requestBodies[0];
    const originalId = requests[0].request_id;
    fireEvent.click(check);
    await waitFor(() => expect(receiptReads).toContain(originalId));
    expect(screen.getByRole('alert')).not.toBeEmptyDOMElement();
    expect(screen.queryByText(/^(Stopped|Cancelled)$/)).not.toBeInTheDocument();
    expect(requests).toHaveLength(1);
    fireEvent.click(await screen.findByRole('button', { name: 'Retry stop' }));
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requestBodies).toEqual([originalBody, originalBody]);
    expect(requests[1].request_id).toBe(originalId);
    expect(broadCancelPaths).toEqual([]);
  });

  it('closes the real preview with Escape without another mutation and restores the child trigger', async () => {
    let complete!: (response: Response) => void;
    postResponse = () =>
      new Promise((resolve) => {
        complete = resolve;
      });
    await renderDock();
    fireEvent.click(await screen.findByRole('button', { name: '2 subagents, open list' }));
    const childTrigger = screen.getByRole('button', { name: 'Open subagent 1 Literature review' });
    fireEvent.click(childTrigger);
    expect(await screen.findByText('Retained child output')).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: 'Stop this subagent' }));
    await waitFor(() => expect(requests).toHaveLength(1));
    fireEvent.keyDown(document, { key: 'Escape', code: 'Escape' });
    await waitFor(() => expect(screen.queryByText('Retained child output')).not.toBeInTheDocument());
    expect(childTrigger).toHaveFocus();
    expect(requests).toHaveLength(1);
    expect(broadCancelPaths).toEqual([]);
    await act(async () => {
      complete(jsonResponse(receipt(requests[0], true)));
    });
    expect(requests).toHaveLength(1);
  });

  it('does not present an old-account acknowledgement in the next account and frame incarnation', async () => {
    let complete!: (response: Response) => void;
    postResponse = () =>
      new Promise((resolve) => {
        complete = resolve;
      });
    const view = await renderDock();
    // Lineage replaces the provisional recovery surface. Establish the current
    // enabled entry before testing the later account/acknowledgement boundary.
    await screen.findByRole('button', { name: '2 subagents, open list' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Stop subagents' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Stop subagents' }));
    await waitFor(() => expect(requests).toHaveLength(1));
    act(() => {
      setRendererAccountOwner('account-b');
    });
    view.rerender(
      <MemoryRouter>
        <XScienceDelegateDock
          conversationId={parentId}
          polling={false}
          ownerId='account-b'
          frameIncarnationId='new-parent-incarnation'
        />
      </MemoryRouter>
    );
    await act(async () => {
      complete(jsonResponse(receipt(requests[0])));
    });
    expect(screen.queryByText('Cancellation requested; execution is still running')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Check stop request' })).not.toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(broadCancelPaths).toEqual([]);
  });
});
