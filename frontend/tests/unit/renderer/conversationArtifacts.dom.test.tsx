/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { ConversationArtifactReference, IConversationArtifact } from '@/common/adapter/ipcBridge';
import {
  ConversationArtifactProvider,
  type ConversationArtifactIndex,
  useConversationArtifactIndex,
  useConversationArtifacts,
  useResolveConversationArtifactWindow,
  useSyncConversationArtifactWindow,
} from '@/renderer/pages/conversation/Messages/artifacts';
import { act, render, screen, waitFor } from '@testing-library/react';
import React, { useEffect } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const bridgeMocks = vi.hoisted(() => ({ invoke: vi.fn() }));
let mockOwnerId = 'owner-a';

vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      listArtifacts: { invoke: bridgeMocks.invoke },
    },
  },
}));

vi.mock('@/renderer/hooks/context/AuthContext', () => ({
  useAuth: () => ({ user: { id: mockOwnerId, username: 'victor' } }),
}));

const ArtifactProbe = () => {
  const artifacts = useConversationArtifacts();
  const files = artifacts.flatMap((artifact) =>
    artifact.kind === 'scientific_files' ? artifact.payload.files.map((file) => file.version_id) : []
  );
  return <div data-testid='artifact-versions'>{files.join(',')}</div>;
};

const WindowProbe: React.FC<{ references: ConversationArtifactReference[] }> = ({ references }) => {
  const sync = useSyncConversationArtifactWindow();
  useEffect(() => sync(references), [references, sync]);
  return null;
};

const WindowResolverProbe: React.FC<{ references: ConversationArtifactReference[] }> = ({ references }) => {
  const resolve = useResolveConversationArtifactWindow();
  useEffect(() => {
    void resolve(references);
  }, [references, resolve]);
  return null;
};

const WindowVersionResolverProbe: React.FC<{ versionId: string }> = ({ versionId }) => {
  const resolve = useResolveConversationArtifactWindow();
  useEffect(() => {
    void resolve([], [versionId]);
  }, [resolve, versionId]);
  return null;
};

const ResolverCapture: React.FC<{
  capture: (resolve: ReturnType<typeof useResolveConversationArtifactWindow>) => void;
}> = ({ capture }) => {
  const resolve = useResolveConversationArtifactWindow();
  useEffect(() => capture(resolve), [capture, resolve]);
  return null;
};

const ArtifactIndexProbe: React.FC<{
  name: string;
  capture: (name: string, index: ConversationArtifactIndex) => void;
}> = ({ name, capture }) => {
  const index = useConversationArtifactIndex();
  capture(name, index);
  return null;
};

describe('ConversationArtifactProvider', () => {
  beforeEach(() => {
    bridgeMocks.invoke.mockReset();
    mockOwnerId = 'owner-a';
  });

  it('does not enumerate conversation artifacts and requests only exact visible message references', async () => {
    bridgeMocks.invoke.mockResolvedValue([
      scientificCollection('conversation-window', 'version-visible', 'artifact-visible'),
    ]);
    const references = [{ artifact_id: 'artifact-visible', version_id: 'version-visible' }];

    render(
      <ConversationArtifactProvider conversation_id='conversation-window'>
        <ArtifactProbe />
        <WindowProbe references={references} />
      </ConversationArtifactProvider>
    );

    await screen.findByText('version-visible');
    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.invoke).toHaveBeenCalledWith(
      { conversation_id: 'conversation-window', references },
      expect.any(AbortSignal)
    );
  });

  it('shares the active artifact window with message-level link resolvers', async () => {
    const pending = deferred<IConversationArtifact[]>();
    bridgeMocks.invoke.mockReturnValueOnce(pending.promise);
    const references = [{ artifact_id: 'artifact-shared', version_id: 'version-shared-window' }];

    render(
      <ConversationArtifactProvider conversation_id='conversation-shared-window'>
        <WindowProbe references={references} />
        <WindowResolverProbe references={references} />
        <WindowVersionResolverProbe versionId='version-shared-window' />
      </ConversationArtifactProvider>
    );

    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(1);
    await act(async () =>
      pending.resolve([scientificCollection('conversation-shared-window', 'version-shared-window', 'artifact-shared')])
    );
    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(1);
  });

  it('reuses exact immutable versions and fetches only references added to the visible window', async () => {
    bridgeMocks.invoke
      .mockResolvedValueOnce([scientificCollection('conversation-delta', 'version-a', 'artifact-a')])
      .mockResolvedValueOnce([scientificCollection('conversation-delta', 'version-b', 'artifact-b')]);
    const first = [{ artifact_id: 'artifact-a', version_id: 'version-a' }];
    const expanded = [...first, { artifact_id: 'artifact-b', version_id: 'version-b' }];

    const rendered = render(
      <ConversationArtifactProvider conversation_id='conversation-delta'>
        <ArtifactProbe />
        <WindowProbe references={first} />
      </ConversationArtifactProvider>
    );
    await screen.findByText('version-a');
    rendered.rerender(
      <ConversationArtifactProvider conversation_id='conversation-delta'>
        <ArtifactProbe />
        <WindowProbe references={expanded} />
      </ConversationArtifactProvider>
    );

    await screen.findByText('version-a,version-b');
    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(2);
    expect(bridgeMocks.invoke.mock.calls[1]?.[0]).toEqual({
      conversation_id: 'conversation-delta',
      references: [{ artifact_id: 'artifact-b', version_id: 'version-b' }],
    });
  });

  it('releases the previous window and rejects its late response', async () => {
    const oldWindow = deferred<IConversationArtifact[]>();
    const currentWindow = deferred<IConversationArtifact[]>();
    bridgeMocks.invoke.mockReturnValueOnce(oldWindow.promise).mockReturnValueOnce(currentWindow.promise);
    const oldReferences = [{ artifact_id: 'artifact-old', version_id: 'version-old' }];
    const currentReferences = [{ artifact_id: 'artifact-current', version_id: 'version-current' }];

    const rendered = render(
      <ConversationArtifactProvider conversation_id='conversation-replace'>
        <ArtifactProbe />
        <WindowProbe references={oldReferences} />
      </ConversationArtifactProvider>
    );
    rendered.rerender(
      <ConversationArtifactProvider conversation_id='conversation-replace'>
        <ArtifactProbe />
        <WindowProbe references={currentReferences} />
      </ConversationArtifactProvider>
    );

    const replacedRequestSignal = bridgeMocks.invoke.mock.calls[0]?.[1];
    expect(replacedRequestSignal).toBeInstanceOf(AbortSignal);
    expect((replacedRequestSignal as AbortSignal).aborted).toBe(true);

    await act(async () =>
      currentWindow.resolve([scientificCollection('conversation-replace', 'version-current', 'artifact-current')])
    );
    expect(screen.getByTestId('artifact-versions')).toHaveTextContent('version-current');
    await act(async () =>
      oldWindow.resolve([scientificCollection('conversation-replace', 'version-old', 'artifact-old')])
    );
    expect(screen.getByTestId('artifact-versions')).toHaveTextContent('version-current');
    expect(screen.getByTestId('artifact-versions')).not.toHaveTextContent('version-old');
  });

  it('chunks a large visible reference window into bounded exact-pair requests', async () => {
    bridgeMocks.invoke.mockResolvedValue([]);
    const references = Array.from({ length: 801 }, (_, index) => ({
      artifact_id: `artifact-${index}`,
      version_id: `version-${index}`,
    }));

    render(
      <ConversationArtifactProvider conversation_id='conversation-chunks'>
        <WindowProbe references={references} />
      </ConversationArtifactProvider>
    );

    await waitFor(() => expect(bridgeMocks.invoke).toHaveBeenCalledTimes(3));
    expect(bridgeMocks.invoke.mock.calls.map(([request]) => request.references.length)).toEqual([400, 400, 1]);
    expect(bridgeMocks.invoke.mock.calls.flatMap(([request]) => request.references)).toEqual(references);
  });

  it('builds one immutable artifact index shared by every message consumer', async () => {
    bridgeMocks.invoke.mockResolvedValue([scientificCollection('conversation-index', 'version-shared')]);
    const indexes = new Map<string, ConversationArtifactIndex>();

    render(
      <ConversationArtifactProvider conversation_id='conversation-index'>
        <WindowProbe references={[{ artifact_id: 'artifact-a', version_id: 'version-shared' }]} />
        <ArtifactIndexProbe name='first' capture={(name, index) => indexes.set(name, index)} />
        <ArtifactIndexProbe name='second' capture={(name, index) => indexes.set(name, index)} />
      </ConversationArtifactProvider>
    );

    await waitFor(() => expect(indexes.get('first')?.byVersionId.has('version-shared')).toBe(true));
    expect(indexes.get('first')).toBe(indexes.get('second'));
    expect(indexes.get('first')?.byVersionId.get('version-shared')?.filename).toBe('result.csv');
  });

  it('partitions link intent from delivery empty results and deduplicates in the existing store', async () => {
    const pending = deferred<IConversationArtifact[]>();
    bridgeMocks.invoke.mockImplementation((request) =>
      request.intent === 'resolve_link' ? pending.promise : Promise.resolve([])
    );
    const references = [{ artifact_id: 'source-intent', version_id: 'source-old' }];
    let resolve!: ReturnType<typeof useResolveConversationArtifactWindow>;
    const capture = (value: typeof resolve) => {
      resolve = value;
    };
    render(
      <ConversationArtifactProvider conversation_id='conversation-link-intent'>
        <WindowProbe references={references} />
        <ResolverCapture capture={capture} />
        <ArtifactProbe />
      </ConversationArtifactProvider>
    );
    await waitFor(() => expect(bridgeMocks.invoke).toHaveBeenCalledTimes(1));
    let first!: Promise<IConversationArtifact[] | null>;
    let second!: Promise<IConversationArtifact[] | null>;
    act(() => {
      first = resolve(references, [], 'resolve_link');
      second = resolve(references, [], 'resolve_link');
    });
    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(2);
    expect(bridgeMocks.invoke.mock.calls[1]?.[0]).toEqual({
      conversation_id: 'conversation-link-intent',
      references,
      version_ids: [],
      intent: 'resolve_link',
    });
    const sources = [scientificCollection('conversation-link-intent', 'source-old', 'source-intent')];
    await act(async () => pending.resolve(sources));
    await expect(first).resolves.toEqual(sources);
    await expect(second).resolves.toEqual(sources);
    await expect(resolve(references, [], 'resolve_link')).resolves.toEqual(sources);
    await expect(resolve(references)).resolves.toEqual([]);
    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('artifact-versions')).toBeEmptyDOMElement();
  });

  it.each(['conversation', 'owner'])(
    'aborts link requests and rejects late responses after %s changes',
    async (change) => {
      const pending = deferred<IConversationArtifact[]>();
      bridgeMocks.invoke.mockReturnValueOnce(pending.promise).mockResolvedValue([]);
      const references = [{ artifact_id: 'scope-source', version_id: 'scope-old' }];
      let resolve!: ReturnType<typeof useResolveConversationArtifactWindow>;
      const capture = (value: typeof resolve) => {
        resolve = value;
      };
      const view = (conversation: string) => (
        <ConversationArtifactProvider conversation_id={conversation}>
          <ResolverCapture capture={capture} />
          <ArtifactProbe />
        </ConversationArtifactProvider>
      );
      const id = 'conversation-link-scope-' + change;
      const rendered = render(view(id));
      const old = resolve(references, [], 'resolve_link').then(
        () => 'unexpected result',
        (error) => error.name
      );
      const signal = bridgeMocks.invoke.mock.calls[0]?.[1] as AbortSignal;
      if (change === 'owner') mockOwnerId = 'owner-b';
      rendered.rerender(view(change === 'conversation' ? id + '-next' : id));
      expect(signal.aborted).toBe(true);
      await expect(resolve(references, [], 'resolve_link')).resolves.toEqual([]);
      await act(async () => pending.resolve([scientificCollection(id, 'scope-old', 'scope-source')]));
      await expect(old).resolves.toBe('AbortError');
      expect(screen.getByTestId('artifact-versions')).toBeEmptyDOMElement();
    }
  );

  it('keeps the link request owner live after a StrictMode effect replay', async () => {
    bridgeMocks.invoke.mockResolvedValue([]);
    let resolve!: ReturnType<typeof useResolveConversationArtifactWindow>;
    const capture = (value: typeof resolve) => {
      resolve = value;
    };
    render(
      <React.StrictMode>
        <ConversationArtifactProvider conversation_id='conversation-link-strict'>
          <ResolverCapture capture={capture} />
        </ConversationArtifactProvider>
      </React.StrictMode>
    );
    await expect(
      resolve([{ artifact_id: 'source-strict', version_id: 'version-strict' }], [], 'resolve_link')
    ).resolves.toEqual([]);
    expect(bridgeMocks.invoke).toHaveBeenCalledTimes(1);
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function scientificCollection(
  conversationId: string,
  versionId: string,
  artifactId = 'artifact-a'
): IConversationArtifact {
  return {
    id: `scientific-files:${conversationId}`,
    conversation_id: conversationId,
    kind: 'scientific_files',
    status: 'active',
    payload: {
      project_id: 'project-a',
      root_frame_id: conversationId,
      files: [
        {
          artifact_id: artifactId,
          version_id: versionId,
          version_number: 1,
          project_id: 'project-a',
          root_frame_id: conversationId,
          frame_id: conversationId,
          creating_frame_id: conversationId,
          filename: 'result.csv',
          content_type: 'text/csv',
          size_bytes: 128,
          preview_kind: 'csv',
          content_url: `/api/artifacts/${artifactId}/versions/${versionId}`,
          created_at: 1,
          updated_at: 1,
          agent_name: 'OPERON',
          is_user_upload: false,
          is_intermediate: false,
        },
      ],
    },
    created_at: 1,
    updated_at: 1,
  };
}
