/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { ipcBridge } from '@/common';
import type { TChatConversation } from '@/common/config/storage';
import { mutate } from 'swr';
import {
  beginConversationDetailHandoff,
  clearConversationReadCaches,
  getConversationOrNull,
  getConversationOrNullShared,
  invalidateConversationReadCaches,
  preferConversationSnapshot,
  readConversationRouteSnapshot,
  readRecentConversationRouteDetail,
  rememberConversationRouteSnapshot,
  refreshConversationCache,
} from '@/renderer/pages/conversation/utils/conversationCache';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { emitter } from '@/renderer/utils/emitter';

vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      get: {
        invoke: vi.fn(),
      },
    },
  },
}));

vi.mock('swr', () => ({
  mutate: vi.fn(),
}));

const mockConversation = {
  id: 'conv-1',
  name: 'Test conversation',
  type: 'acp',
  status: 'finished',
  extra: {},
} as TChatConversation;

describe('conversationCache', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clearConversationReadCaches();
    resetRendererAccountScopeForTest();
  });
  afterEach(() => vi.restoreAllMocks());

  describe('getConversationOrNull', () => {
    it('returns null when the backend reports a missing conversation', async () => {
      const error = new BackendHttpError({
        method: 'GET',
        path: '/api/conversations/missing',
        status: 404,
        body: {
          success: false,
          error: 'Not found: Conversation missing not found',
          code: 'NOT_FOUND',
        },
      });
      vi.mocked(ipcBridge.conversation.get.invoke).mockRejectedValue(error);

      await expect(getConversationOrNull('missing')).resolves.toBeNull();
    });

    it('returns null for the v1.1 detail-only 404 envelope', async () => {
      const error = new BackendHttpError({
        method: 'GET',
        path: '/api/conversations/missing',
        status: 404,
        body: { detail: 'Conversation missing not found' },
      });
      vi.mocked(ipcBridge.conversation.get.invoke).mockRejectedValue(error);

      await expect(getConversationOrNull('missing')).resolves.toBeNull();
    });

    it('returns the conversation when the backend lookup succeeds', async () => {
      vi.mocked(ipcBridge.conversation.get.invoke).mockResolvedValue(mockConversation);

      await expect(getConversationOrNull('conv-1')).resolves.toBe(mockConversation);
    });

    it('rethrows non-404 backend errors so database failures remain visible', async () => {
      const error = new BackendHttpError({
        method: 'GET',
        path: '/api/conversations/conv-1',
        status: 500,
        body: {
          success: false,
          error: 'Internal error: Database error: no such table: conversations',
          code: 'INTERNAL_ERROR',
        },
      });
      vi.mocked(ipcBridge.conversation.get.invoke).mockRejectedValue(error);

      await expect(getConversationOrNull('conv-1')).rejects.toBe(error);
    });

    it('coalesces concurrent detail consumers but never retains a completed snapshot', async () => {
      let resolveRequest!: (value: TChatConversation) => void;
      const first = new Promise<TChatConversation>((resolve) => {
        resolveRequest = resolve;
      });
      vi.mocked(ipcBridge.conversation.get.invoke)
        .mockReturnValueOnce(first)
        .mockResolvedValueOnce({
          ...mockConversation,
          name: 'fresh detail',
        });

      const routeRead = getConversationOrNullShared('conv-1');
      const runtimeRead = getConversationOrNull('conv-1');
      expect(routeRead).toBe(runtimeRead);
      expect(ipcBridge.conversation.get.invoke).toHaveBeenCalledTimes(1);

      resolveRequest(mockConversation);
      await expect(routeRead).resolves.toBe(mockConversation);
      await expect(getConversationOrNullShared('conv-1')).resolves.toMatchObject({ name: 'fresh detail' });
      expect(ipcBridge.conversation.get.invoke).toHaveBeenCalledTimes(2);
    });

    it('does not coalesce a pending detail read across authenticated owners', async () => {
      let resolveOwnerA!: (value: TChatConversation) => void;
      const ownerA = new Promise<TChatConversation>((resolve) => {
        resolveOwnerA = resolve;
      });
      vi.mocked(ipcBridge.conversation.get.invoke)
        .mockReturnValueOnce(ownerA)
        .mockResolvedValueOnce({ ...mockConversation, name: 'owner-b detail' });

      setRendererAccountOwner('owner-a');
      const first = getConversationOrNull('conv-1');
      setRendererAccountOwner('owner-b');
      await expect(getConversationOrNull('conv-1')).resolves.toMatchObject({ name: 'owner-b detail' });
      resolveOwnerA({ ...mockConversation, name: 'owner-a detail' });
      await expect(first).resolves.toMatchObject({ name: 'owner-a detail' });

      expect(ipcBridge.conversation.get.invoke).toHaveBeenCalledTimes(2);
    });
  });

  describe('refreshConversationCache', () => {
    it('skips cache mutation when the conversation is missing', async () => {
      const error = new BackendHttpError({
        method: 'GET',
        path: '/api/conversations/missing',
        status: 404,
        body: {
          success: false,
          error: 'Not found: Conversation missing not found',
          code: 'NOT_FOUND',
        },
      });
      vi.mocked(ipcBridge.conversation.get.invoke).mockRejectedValue(error);

      await expect(refreshConversationCache('missing', mutate)).resolves.toBeUndefined();

      expect(mutate).not.toHaveBeenCalled();
    });

    it('rethrows non-404 backend errors instead of hiding them', async () => {
      const error = new BackendHttpError({
        method: 'GET',
        path: '/api/conversations/conv-1',
        status: 500,
        body: {
          success: false,
          error: 'Internal error: Database error: no such table: conversations',
          code: 'INTERNAL_ERROR',
        },
      });
      vi.mocked(ipcBridge.conversation.get.invoke).mockRejectedValue(error);

      await expect(refreshConversationCache('conv-1', mutate)).rejects.toBe(error);

      expect(mutate).not.toHaveBeenCalled();
    });
  });

  describe('authoritative detail invalidation', () => {
    it('publishes canonical absence only as read-only missing, never a queue-clearing delete action', async () => {
      setRendererAccountOwner('owner-a');
      const emit = vi.spyOn(emitter, 'emit');
      vi.mocked(ipcBridge.conversation.get.invoke).mockResolvedValue(null);

      await expect(getConversationOrNullShared('conv-1')).resolves.toBeNull();

      expect(emit).toHaveBeenCalledExactlyOnceWith(
        'conversation.detail.missing',
        getRendererAccountScopeToken(),
        'conv-1'
      );
      expect(emit).not.toHaveBeenCalledWith('conversation.deleted', expect.anything());
    });

    it('does not publish a previous account404 into the new owner scope', async () => {
      setRendererAccountOwner('owner-a');
      let rejectOld!: (reason: unknown) => void;
      vi.mocked(ipcBridge.conversation.get.invoke).mockReturnValueOnce(
        new Promise((_, reject) => {
          rejectOld = reject;
        })
      );
      const oldRead = getConversationOrNullShared('conv-1');
      setRendererAccountOwner('owner-b');
      rememberConversationRouteSnapshot('owner-b', mockConversation, 'detail');
      const emit = vi.spyOn(emitter, 'emit');
      rejectOld(new BackendHttpError({ method: 'GET', path: '/api/conversations/conv-1', status: 404, body: {} }));

      await expect(oldRead).rejects.toMatchObject({ name: 'AbortError' });

      expect(emit).not.toHaveBeenCalled();
      expect(readConversationRouteSnapshot('owner-b', 'conv-1')).toBe(mockConversation);
    });

    it('fences a superseded404 without invalidating the fresh same-owner handoff', async () => {
      setRendererAccountOwner('owner-a');
      let rejectOld!: (reason: unknown) => void;
      vi.mocked(ipcBridge.conversation.get.invoke).mockReturnValueOnce(
        new Promise((_, reject) => {
          rejectOld = reject;
        })
      );
      const oldRead = getConversationOrNullShared('conv-1');
      invalidateConversationReadCaches('owner-a', 'conv-1');
      beginConversationDetailHandoff('conv-1');
      vi.mocked(ipcBridge.conversation.get.invoke).mockResolvedValue(mockConversation);
      await expect(getConversationOrNullShared('conv-1')).resolves.toBe(mockConversation);
      rememberConversationRouteSnapshot('owner-a', mockConversation, 'detail');
      const emit = vi.spyOn(emitter, 'emit');
      rejectOld(new BackendHttpError({ method: 'GET', path: '/api/conversations/conv-1', status: 404, body: {} }));
      await expect(oldRead).rejects.toMatchObject({ name: 'AbortError' });

      expect(emit).not.toHaveBeenCalled();
      expect(readRecentConversationRouteDetail('owner-a', 'conv-1')).toBe(mockConversation);
      await expect(getConversationOrNullShared('conv-1')).resolves.toBe(mockConversation);
      expect(ipcBridge.conversation.get.invoke).toHaveBeenCalledTimes(2);
    });

    it('invalidates only the selected owner and conversation snapshots', () => {
      setRendererAccountOwner('owner-a');
      rememberConversationRouteSnapshot('owner-a', mockConversation, 'detail');
      rememberConversationRouteSnapshot('owner-a', { ...mockConversation, id: 'conv-other' }, 'detail');
      rememberConversationRouteSnapshot('owner-b', mockConversation, 'detail');

      invalidateConversationReadCaches('owner-a', 'conv-1');

      expect(readConversationRouteSnapshot('owner-a', 'conv-1')).toBeUndefined();
      expect(readConversationRouteSnapshot('owner-a', 'conv-other')).toMatchObject({ id: 'conv-other' });
      expect(readConversationRouteSnapshot('owner-b', 'conv-1')).toBe(mockConversation);
    });
  });

  describe('route cache priming', () => {
    it('never lets an older sidebar summary replace a newer detail snapshot', () => {
      const current = { ...mockConversation, modified_at: 20, name: 'new detail' } as TChatConversation;
      const candidate = { ...mockConversation, modified_at: 10, name: 'old sidebar' } as TChatConversation;

      expect(preferConversationSnapshot(current, candidate)).toBe(current);
      expect(preferConversationSnapshot(undefined, candidate)).toBe(candidate);
    });

    it('isolates route snapshots by authenticated owner and rejects an empty owner', () => {
      const conversation = { ...mockConversation, id: 'conv-owner-isolation' } as TChatConversation;

      rememberConversationRouteSnapshot('owner-a', conversation, 'summary');

      expect(readConversationRouteSnapshot('owner-a', conversation.id)).toBe(conversation);
      expect(readConversationRouteSnapshot('owner-b', conversation.id)).toBeUndefined();
      expect(readConversationRouteSnapshot('', conversation.id)).toBeUndefined();
    });

    it('keeps detail authoritative when an equal-timestamp summary arrives later', () => {
      const summary = {
        ...mockConversation,
        id: 'conv-detail-priority',
        modified_at: 20,
        name: 'sidebar summary',
        extra: { project_id: 'project-1' },
      } as TChatConversation;
      const detail = {
        ...summary,
        name: 'full detail',
        extra: { project_id: 'project-1', workspace: { path: '/project-1' } },
      } as TChatConversation;

      rememberConversationRouteSnapshot('owner-a', summary, 'summary');
      rememberConversationRouteSnapshot('owner-a', detail, 'detail');
      rememberConversationRouteSnapshot('owner-a', { ...summary, name: 'late summary' }, 'summary');

      expect(readConversationRouteSnapshot('owner-a', summary.id)).toBe(detail);
    });

    it('exposes only a recent detail snapshot for duplicate-read suppression', () => {
      const summary = { ...mockConversation, id: 'conv-recent-summary' } as TChatConversation;
      const detail = { ...mockConversation, id: 'conv-recent-detail' } as TChatConversation;

      rememberConversationRouteSnapshot('owner-a', summary, 'summary');
      rememberConversationRouteSnapshot('owner-a', detail, 'detail');

      expect(readRecentConversationRouteDetail('owner-a', summary.id)).toBeUndefined();
      expect(readRecentConversationRouteDetail('owner-a', detail.id)).toBe(detail);
      expect(readRecentConversationRouteDetail('owner-a', detail.id, -1)).toBeUndefined();
    });

    it('reuses a completed detail read only inside an explicit navigation handoff', async () => {
      const conversation = { ...mockConversation, id: 'conv-navigation-handoff' } as TChatConversation;
      vi.mocked(ipcBridge.conversation.get.invoke).mockResolvedValue(conversation);
      beginConversationDetailHandoff(conversation.id, 500);

      await expect(getConversationOrNull(conversation.id)).resolves.toBe(conversation);
      await expect(getConversationOrNull(conversation.id)).resolves.toBe(conversation);

      expect(ipcBridge.conversation.get.invoke).toHaveBeenCalledTimes(1);
    });
  });
});
