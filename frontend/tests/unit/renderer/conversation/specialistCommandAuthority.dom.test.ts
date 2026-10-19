import { renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useAcpCommandAuthority } from '@/renderer/pages/conversation/platforms/acp/useAcpCommandAuthority';
import {
  createQueuedCommandItem,
  normalizeQueueState,
} from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import {
  loadSendBoxDraft,
  saveSendBoxDraft,
  sendBoxDraftStorageKey,
  type AcpSendBoxDraft,
} from '@/renderer/hooks/chat/sendBoxDraftPersistence';
import { specialistIdentity, specialistSnapshot } from '../../xscience/sessionSpecialistFixtures';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
const { selected, revision, load } = vi.hoisted(() => ({ selected: vi.fn(), revision: vi.fn(), load: vi.fn() }));
vi.mock('@/renderer/services/xScienceConversationBranches', () => ({
  getSelectedXScienceBranch: selected,
  getXScienceBranchSelectionRevision: revision,
  loadXScienceConversationBranches: load,
}));
const options = { delegation: true, autoReview: false, memory: true, targetAgent: 'OBSOLETE_LOCAL_HINT' };
const reference = (() => {
  const value = specialistSnapshot(1, 'AGENT_B');
  return { scope: value.scope, branch: value.prepared_branch, revision: 1, snapshot_id: value.desired.snapshot_id };
})();
const captured = { reference, targetAgent: 'AGENT_B' };
afterEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});
it('captures the ordinary reference synchronously before branch loading yields', async () => {
  selected.mockReturnValue('branch-a');
  revision.mockReturnValue(1);
  let finish!: (value: unknown) => void;
  load.mockReturnValue(
    new Promise((done) => {
      finish = done;
    })
  );
  const capture = vi.fn(() => captured),
    controller = { capture } as unknown as XScienceSpecialistControl;
  const { result } = renderHook(() =>
    useAcpCommandAuthority(
      specialistIdentity.frameId,
      options,
      { current: null },
      { current: false },
      undefined,
      controller
    )
  );
  const pending = result.current();
  expect(capture).toHaveBeenCalledWith('branch-a');
  expect(load).toHaveBeenCalledTimes(1);
  capture.mockReturnValue({ reference: { ...reference, revision: 2 }, targetAgent: 'AGENT_C' });
  finish({ activeBranchId: 'branch-a', generation: 1, branches: [{ id: 'branch-a' }] });
  expect((await pending).sessionOptions).toMatchObject({ target_agent: 'AGENT_B', specialist_binding: reference });
});
it('preserves a queued frozen reference and attaches no fresh specialist to control inputs', async () => {
  selected.mockReturnValue(null);
  revision.mockReturnValue(0);
  const capture = vi.fn(() => {
    throw new Error('must not recapture');
  });
  const { result } = renderHook(() =>
    useAcpCommandAuthority('frame', options, { current: null }, { current: false }, undefined, {
      capture,
    } as unknown as XScienceSpecialistControl)
  );
  expect((await result.current(undefined, true, false, captured)).sessionOptions).toMatchObject({
    target_agent: 'AGENT_B',
    specialist_binding: reference,
  });
  const control = (await result.current(undefined, false)).sessionOptions;
  expect(control).not.toHaveProperty('specialist_binding');
  expect(control).not.toHaveProperty('target_agent');
  expect(capture).not.toHaveBeenCalled();
});
it('keeps the same immutable definition through durable queue and edited-draft roundtrips', () => {
  const item = createQueuedCommandItem({ input: 'synthetic work', files: [], specialistAuthority: captured });
  const restored = normalizeQueueState(JSON.parse(JSON.stringify({ items: [item], isPaused: false }))).items[0];
  expect(restored.specialistAuthority).toEqual(captured);
  expect(restored.specialistAuthority?.reference).not.toBe(reference);
  const draft: AcpSendBoxDraft = {
    _type: 'acp',
    content: restored.input,
    atPath: [],
    uploadFile: [],
    contextItems: [],
    specialistQueuedAuthority: restored.specialistAuthority,
  };
  expect(saveSendBoxDraft('owner', 'frame', draft)).toBe(true);
  expect(loadSendBoxDraft('owner', 'frame')?.specialistQueuedAuthority).toEqual(captured);
  expect(
    normalizeQueueState({ items: [{ ...item, specialistAuthority: undefined }], isPaused: false }).items[0]
  ).not.toHaveProperty('specialistAuthority');
});
it('retains invalid recovered authority as blocked evidence instead of silently choosing the current specialist', () => {
  const item = {
    id: 'item',
    input: 'original',
    files: [],
    created_at: 1,
    specialistAuthority: { reference: { ...reference, snapshot_id: '' }, targetAgent: 'AGENT_B' },
  };
  const recovered = normalizeQueueState({ items: [item], isPaused: false }).items[0];
  expect(recovered).toMatchObject({ input: 'original', pausedReason: 'send_failed', specialistAuthorityInvalid: true });
  localStorage.setItem(
    sendBoxDraftStorageKey('owner', 'frame')!,
    JSON.stringify({
      version: 1,
      draft: {
        _type: 'acp',
        content: 'original',
        atPath: [],
        uploadFile: [],
        contextItems: [],
        specialistQueuedAuthority: item.specialistAuthority,
      },
    })
  );
  expect(loadSendBoxDraft('owner', 'frame')).toMatchObject({ content: 'original', specialistQueuedInvalid: true });
});
