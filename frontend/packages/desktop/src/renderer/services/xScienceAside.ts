import { decodeConversationSendAccepted } from '@/common/adapter/conversationRuntimeProtocol';
import type { ISendMessageParams } from '@/common/adapter/ipcBridgeTypes';
import {
  getSelectedXScienceBranch,
  getXScienceBranchSelectionRevision,
  normalizeXScienceConversationBranches,
} from './xScienceConversationBranches';
import { requestXScienceJson, type XScienceGatewayOptions } from './xScienceHttp';
import { getRendererAccountScopeToken } from './rendererAccountScope';

export type XScienceAsideIntent = Readonly<{
  parentFrameId: string;
  intentId: string;
  request: string;
  model: string | null;
  asSession: boolean;
  sourceBranchId: string;
  expectedGeneration: number;
}>;

export type XScienceAsideReceipt = Readonly<{
  frameId: string;
  intentId: string;
  inputMessageId: string;
  sourceBranchId: string;
  sourceGeneration: number;
  throughPublicationSequence: number;
  asSession: boolean;
  replayed: boolean;
}>;

export type XScienceAsideLookup =
  | { status: 'absent' }
  | { status: 'cancelled-before-acceptance'; intentId: string }
  | { status: 'accepted'; receipt: XScienceAsideReceipt; cancelledInputMessageId?: string };

export type XScienceAsideClose =
  | { status: 'cancelled-before-acceptance'; intentId: string }
  | { status: 'cancelled' | 'already-terminal'; receipt: XScienceAsideReceipt; inputMessageId: string };

export type XScienceAsideFollowup = Readonly<{
  loadingId: string;
  text: string;
  sessionOptions: NonNullable<ISendMessageParams['session_options']>;
}>;

type MessageCancellationIdentity = Readonly<{
  frameId: string;
  loadingId: string;
  requestSha256: string;
  replayed: boolean;
}>;
export type XScienceAsideMessageCancellation =
  | (MessageCancellationIdentity & { status: 'cancelled-before-acceptance'; inputMessageId: null })
  | (MessageCancellationIdentity & { status: 'cancelled' | 'already-terminal'; inputMessageId: string });

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const BRANCH_PATTERN = /^br_[0-9a-f]{8}$/;
const validId = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 256;

export function isXScienceAsideIntent(value: unknown): value is XScienceAsideIntent {
  if (!isRecord(value)) return false;
  return (
    validId(value.parentFrameId) &&
    typeof value.intentId === 'string' &&
    UUID_PATTERN.test(value.intentId) &&
    typeof value.request === 'string' &&
    value.request.trim().length > 0 &&
    (value.model === null || typeof value.model === 'string') &&
    typeof value.asSession === 'boolean' &&
    typeof value.sourceBranchId === 'string' &&
    BRANCH_PATTERN.test(value.sourceBranchId) &&
    Number.isSafeInteger(value.expectedGeneration) &&
    Number(value.expectedGeneration) > 0
  );
}

/** Capture once, then replay exactly this source and body, never the current UI controls. */
export async function captureXScienceAsideIntent(
  parentFrameId: string,
  text: string,
  options: { model?: string | null; asSession?: boolean; intentId?: string; fetchImpl?: typeof fetch } = {}
): Promise<XScienceAsideIntent> {
  if (!validId(parentFrameId) || !text.trim()) throw new Error('aside_request_invalid');
  const selectedBranch = getSelectedXScienceBranch(parentFrameId);
  const selectedRevision = getXScienceBranchSelectionRevision(parentFrameId);
  const branches = await loadXScienceAsideBranches(parentFrameId, { fetchImpl: options.fetchImpl });
  if (
    getSelectedXScienceBranch(parentFrameId) !== selectedBranch ||
    getXScienceBranchSelectionRevision(parentFrameId) !== selectedRevision
  )
    throw new Error('aside_source_changed');
  const sourceBranchId = selectedBranch ?? branches.activeBranchId;
  const intent = {
    parentFrameId,
    intentId: options.intentId ?? crypto.randomUUID(),
    request: text.trim(),
    model: options.model ?? null,
    asSession: options.asSession === true,
    sourceBranchId: sourceBranchId ?? '',
    expectedGeneration: branches.generation,
  };
  if (!isXScienceAsideIntent(intent) || !branches.branches.some((branch) => branch.id === sourceBranchId)) {
    throw new Error('aside_source_unavailable');
  }
  return Object.freeze(intent);
}

/** Keep a late account read outside the shared branch-selection normalizer. */
export async function loadXScienceAsideBranches(frameId: string, options: XScienceGatewayOptions = {}) {
  const account = getRendererAccountScopeToken();
  const value = await requestXScienceJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/branches`,
    { method: 'GET' },
    { timeoutMs: 15_000, ...options }
  );
  if (getRendererAccountScopeToken() !== account) throw new Error('aside_action_owner_changed');
  return normalizeXScienceConversationBranches(frameId, value);
}

export function asideRequestBody(intent: XScienceAsideIntent) {
  if (!isXScienceAsideIntent(intent)) throw new Error('aside_request_invalid');
  return {
    request: intent.request,
    model: intent.model,
    as_session: intent.asSession,
    intent_id: intent.intentId,
    source_branch_id: intent.sourceBranchId,
    expected_generation: intent.expectedGeneration,
  };
}

export async function createXScienceAside(
  intent: XScienceAsideIntent,
  options: XScienceGatewayOptions = {}
): Promise<XScienceAsideReceipt> {
  const value = await requestXScienceJson<unknown>(
    asidePath(intent),
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(asideRequestBody(intent)),
    },
    options
  );
  return requireXScienceAsideReceipt(value, intent);
}

export async function lookupXScienceAside(
  intent: XScienceAsideIntent,
  options: XScienceGatewayOptions = {}
): Promise<XScienceAsideLookup> {
  const value = await requestXScienceJson<unknown>(intentPath(intent), { method: 'GET' }, options);
  if (!isRecord(value) || value.intent_id !== intent.intentId) throw new Error('aside_lookup_unverified');
  if (value.accepted === false) {
    if (value.cancelled === true && value.cancellation_status === 'cancelled_before_acceptance') {
      return { status: 'cancelled-before-acceptance', intentId: intent.intentId };
    }
    if (value.cancelled === true || value.cancellation_status !== undefined) throw new Error('aside_lookup_unverified');
    return { status: 'absent' };
  }
  const receipt = requireXScienceAsideReceipt(value, intent);
  const cancelledInputMessageId =
    value.cancelled === true && value.cancellation_status === 'cancelled' && validId(value.cancelled_input_message_id)
      ? value.cancelled_input_message_id
      : undefined;
  return { status: 'accepted', receipt, ...(cancelledInputMessageId ? { cancelledInputMessageId } : {}) };
}

/** Atomic Close fences a delayed create before admission, or cancels one exact owned input. */
export async function closeXScienceAside(
  intent: XScienceAsideIntent,
  inputMessageId?: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceAsideClose> {
  if (inputMessageId !== undefined && !validId(inputMessageId)) throw new Error('aside_cancel_target_invalid');
  const value = await requestXScienceJson<unknown>(
    intentPath(intent),
    {
      method: 'DELETE',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        ...asideRequestBody(intent),
        ...(inputMessageId ? { expected_input_message_id: inputMessageId } : {}),
      }),
    },
    options
  );
  if (!isRecord(value) || value.intent_id !== intent.intentId) throw new Error('aside_close_unverified');
  if (
    value.accepted === false &&
    value.cancelled === true &&
    value.cancellation_status === 'cancelled_before_acceptance'
  ) {
    return { status: 'cancelled-before-acceptance', intentId: intent.intentId };
  }
  const receipt = requireXScienceAsideReceipt(value, intent);
  const expected = inputMessageId ?? receipt.inputMessageId;
  if (
    value.cancelled === true &&
    value.cancellation_status === 'cancelled' &&
    value.cancelled_input_message_id === expected
  ) {
    return { status: 'cancelled', receipt, inputMessageId: expected };
  }
  if (
    value.cancelled === false &&
    value.cancellation_status === 'already_terminal' &&
    value.target_input_message_id === expected
  ) {
    return { status: 'already-terminal', receipt, inputMessageId: expected };
  }
  throw new Error('aside_close_unverified');
}

export async function cancelXScienceAsideInput(
  frameId: string,
  inputMessageId: string,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  if (!validId(frameId) || !validId(inputMessageId)) throw new Error('aside_cancel_target_invalid');
  const value = await requestXScienceJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/cancel?reason=user`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ expected_input_message_id: inputMessageId }),
    },
    options
  );
  if (
    !isRecord(value) ||
    value.root_frame_id !== frameId ||
    !Array.isArray(value.cancelled_frames) ||
    !value.cancelled_frames.includes(frameId)
  )
    throw new Error('aside_cancel_unverified');
}

export async function sendXScienceAsideFollowup(
  frameId: string,
  command: XScienceAsideFollowup,
  options: XScienceGatewayOptions = {}
) {
  const value = await requestXScienceJson<unknown>(
    `/api/conversations/${encodeURIComponent(frameId)}/messages`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(asideFollowupRequestBody(command)),
    },
    options
  );
  const accepted = decodeConversationSendAccepted(value, frameId);
  // A branch continuation can return a canonical message ID different from
  // loading_id. Only the server's accepted tuple is cancellation authority.
  return accepted;
}

export function asideFollowupRequestBody(command: XScienceAsideFollowup): {
  content: string;
  files: string[];
  loading_id: string;
  session_options: XScienceAsideFollowup['sessionOptions'];
} {
  return { content: command.text, files: [], loading_id: command.loadingId, session_options: command.sessionOptions };
}

/** Cancels the exact frozen mutation, even when its POST acceptance was lost. */
export async function cancelXScienceAsideMessageIntent(
  frameId: string,
  command: XScienceAsideFollowup,
  options: XScienceGatewayOptions = {}
): Promise<XScienceAsideMessageCancellation> {
  const value = await requestXScienceJson<unknown>(
    `/api/conversations/${encodeURIComponent(frameId)}/message-intents/${encodeURIComponent(command.loadingId)}`,
    {
      method: 'DELETE',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(asideFollowupRequestBody(command)),
    },
    options
  );
  if (
    !isRecord(value) ||
    value.frame_id !== frameId ||
    value.loading_id !== command.loadingId ||
    typeof value.request_sha256 !== 'string' ||
    !/^[0-9a-f]{64}$/.test(value.request_sha256) ||
    typeof value.replayed !== 'boolean'
  )
    throw new Error('aside_message_cancellation_unverified');
  const identity = {
    frameId,
    loadingId: command.loadingId,
    requestSha256: value.request_sha256,
    replayed: value.replayed,
  };
  if (
    value.accepted === false &&
    value.cancelled === true &&
    value.cancellation_status === 'cancelled_before_acceptance'
  ) {
    return { ...identity, status: 'cancelled-before-acceptance', inputMessageId: null };
  }
  if (!validId(value.input_message_id)) throw new Error('aside_message_cancellation_unverified');
  if (value.accepted === true && value.cancelled === true && value.cancellation_status === 'cancelled') {
    return { ...identity, status: 'cancelled', inputMessageId: value.input_message_id };
  }
  if (value.accepted === true && value.cancelled === false && value.cancellation_status === 'already_terminal') {
    return { ...identity, status: 'already-terminal', inputMessageId: value.input_message_id };
  }
  throw new Error('aside_message_cancellation_unverified');
}

export function requireXScienceAsideReceipt(value: unknown, intent: XScienceAsideIntent): XScienceAsideReceipt {
  if (
    !isRecord(value) ||
    value.accepted !== true ||
    !validId(value.frame_id) ||
    value.frame_id === intent.parentFrameId ||
    value.intent_id !== intent.intentId ||
    value.input_message_id !== intent.intentId ||
    value.source_branch_id !== intent.sourceBranchId ||
    value.source_generation !== intent.expectedGeneration ||
    value.as_session !== intent.asSession ||
    !Number.isSafeInteger(value.through_publication_sequence) ||
    Number(value.through_publication_sequence) < 0 ||
    typeof value.replayed !== 'boolean'
  )
    throw new Error('aside_receipt_unverified');
  return Object.freeze({
    frameId: value.frame_id,
    intentId: intent.intentId,
    inputMessageId: value.input_message_id,
    sourceBranchId: value.source_branch_id,
    sourceGeneration: value.source_generation,
    throughPublicationSequence: Number(value.through_publication_sequence),
    asSession: value.as_session,
    replayed: value.replayed,
  });
}

function asidePath(intent: XScienceAsideIntent) {
  if (!isXScienceAsideIntent(intent)) throw new Error('aside_request_invalid');
  return `/api/frames/${encodeURIComponent(intent.parentFrameId)}/aside`;
}
function intentPath(intent: XScienceAsideIntent) {
  return `${asidePath(intent)}?intent_id=${encodeURIComponent(intent.intentId)}`;
}
function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}
