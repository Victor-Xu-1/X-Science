import { getRendererAccountOwnerId } from '../rendererAccountScope';
import {
  isXScienceAsideIntent,
  requireXScienceAsideReceipt,
  type XScienceAsideFollowup,
  type XScienceAsideIntent,
  type XScienceAsideReceipt,
} from '../xScienceAside';
import type { SpecialistInputReference } from '@/common/adapter/specialistBinding';
import { decodeAsideFollowupSpecialist, freezeAsideSpecialist, sameAsideSpecialist } from './asideSpecialist';
import {
  normalizeXScienceResumeTarget,
  type XScienceContinueInput,
} from '@/renderer/components/xScience/runtime/resumeCommandIntent';

/** Recovery hints only. Every execution/close still requires authenticated server receipts. */
export type AsidePresentation = {
  intent: XScienceAsideIntent;
  receipt: XScienceAsideReceipt | null;
  inputMessageId: string | null;
  pendingFollowup: XScienceAsideFollowup | null;
  pendingResume: XScienceContinueInput | null;
  closeRequested: boolean;
  draft: string;
  consumer: AsideConsumer | null;
};

export type AsideConsumer = {
  kind: 'draft' | 'queue' | 'branch';
  id: string;
  content: string;
  sourceSpecialist?: SpecialistInputReference;
};

const MAX_HINT_BYTES = 256 * 1024;
const MAX_CLOSING_HINTS = 4;
const MAX_ENVELOPE_BYTES = (MAX_CLOSING_HINTS + 1) * MAX_HINT_BYTES + 256;
type AsidePresentations = { current: AsidePresentation | null; closing: AsidePresentation[] };
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const key = (parent: string, asSession: boolean) => {
  const owner = getRendererAccountOwnerId();
  return owner
    ? `x-science-aside/v1/${encodeURIComponent(owner)}/${encodeURIComponent(parent)}/${asSession ? 'branch' : 'side-chat'}`
    : '';
};
const record = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);

export function readAsidePresentation(parent: string, asSession: boolean): AsidePresentation | null {
  return readAsidePresentations(parent, asSession).current;
}

export function readAsidePresentations(parent: string, asSession: boolean): AsidePresentations {
  const storageKey = key(parent, asSession);
  try {
    if (storageKey) return readStoredPresentations(storageKey, parent, asSession);
  } catch {
    // An unreadable hint cannot authorize a request or be overwritten by a new intent.
  }
  return { current: null, closing: [] };
}

function readStoredPresentations(storageKey: string, parent: string, asSession: boolean): AsidePresentations {
  const text = localStorage.getItem(storageKey);
  if (text === null) return { current: null, closing: [] };
  if (byteLength(text) > MAX_ENVELOPE_BYTES) throw new Error('aside_presentation_invalid');
  const value: unknown = JSON.parse(text);
  if (!record(value)) throw new Error('aside_presentation_invalid');
  if (value.version !== 2) {
    const current = normalizePresentation(value, parent, asSession);
    if (!current || byteLength(text) > MAX_HINT_BYTES) throw new Error('aside_presentation_invalid');
    return { current, closing: [] };
  }
  if (!Array.isArray(value.closing) || value.closing.length > MAX_CLOSING_HINTS || (asSession && value.closing.length))
    throw new Error('aside_presentation_invalid');
  const current = value.current === null ? null : normalizePresentation(value.current, parent, asSession);
  if (value.current !== null && !current) throw new Error('aside_presentation_invalid');
  const seen = new Set(current ? [current.intent.intentId] : []);
  const closing = value.closing.map((entry: unknown) => {
    const hint = normalizePresentation(entry, parent, asSession);
    if (!hint?.closeRequested || seen.has(hint.intent.intentId)) throw new Error('aside_presentation_invalid');
    seen.add(hint.intent.intentId);
    return hint;
  });
  return { current, closing };
}

function normalizePresentation(value: unknown, parent: string, asSession: boolean): AsidePresentation | null {
  if (
    !record(value) ||
    !isXScienceAsideIntent(value.intent) ||
    value.intent.parentFrameId !== parent ||
    value.intent.asSession !== asSession ||
    typeof value.closeRequested !== 'boolean' ||
    typeof value.draft !== 'string'
  )
    return null;
  const sourceSpecialist = freezeAsideSpecialist(value.intent.sourceSpecialist, parent);
  const intent = Object.freeze({ ...value.intent, ...(sourceSpecialist ? { sourceSpecialist } : {}) });
  const receipt = normalizeReceipt(value.receipt, intent);
  if (value.receipt !== null && !receipt) return null;
  const pendingFollowup = normalizeFollowup(value.pendingFollowup, intent, receipt);
  if (value.pendingFollowup !== null && !pendingFollowup) return null;
  let pendingResume: XScienceContinueInput | null = null;
  if (value.pendingResume !== null) {
    if (
      !record(value.pendingResume) ||
      typeof value.pendingResume.client_command_id !== 'string' ||
      !UUID.test(value.pendingResume.client_command_id)
    )
      return null;
    const target = normalizeXScienceResumeTarget(value.pendingResume.resume_target);
    if (!target || (value.pendingResume.model !== undefined && typeof value.pendingResume.model !== 'string'))
      return null;
    pendingResume = {
      client_command_id: value.pendingResume.client_command_id,
      resume_target: target,
      ...(typeof value.pendingResume.model === 'string' && value.pendingResume.model
        ? { model: value.pendingResume.model }
        : {}),
    };
  }
  if (value.inputMessageId !== null && (typeof value.inputMessageId !== 'string' || !value.inputMessageId)) return null;
  let consumer: Record<string, unknown> | null = null;
  if (value.consumer != null) {
    if (!record(value.consumer)) return null;
    consumer = value.consumer;
  }
  if (
    consumer !== null &&
    (!['draft', 'queue', 'branch'].includes(String(consumer.kind)) ||
      typeof consumer.id !== 'string' ||
      !consumer.id ||
      consumer.id.length > 256 ||
      typeof consumer.content !== 'string')
  )
    return null;
  const consumerSource = consumer ? freezeAsideSpecialist(consumer.sourceSpecialist, parent) : undefined;
  if (consumer && !sameAsideSpecialist(consumerSource, sourceSpecialist)) return null;
  return {
    intent,
    receipt,
    inputMessageId: value.inputMessageId as string | null,
    pendingFollowup,
    pendingResume,
    closeRequested: value.closeRequested,
    draft: value.draft,
    consumer: consumer
      ? {
          kind: consumer.kind as AsideConsumer['kind'],
          id: consumer.id as string,
          content: consumer.content as string,
          ...(consumerSource ? { sourceSpecialist: consumerSource } : {}),
        }
      : null,
  };
}

const byteLength = (text: string) => new TextEncoder().encode(text).length;
const sameIntent = (left: XScienceAsideIntent, right: XScienceAsideIntent) =>
  left.intentId === right.intentId &&
  left.parentFrameId === right.parentFrameId &&
  left.asSession === right.asSession &&
  left.request === right.request &&
  left.model === right.model &&
  left.sourceBranchId === right.sourceBranchId &&
  left.expectedGeneration === right.expectedGeneration &&
  sameAsideSpecialist(left.sourceSpecialist, right.sourceSpecialist);

function writePresentations(storageKey: string, value: AsidePresentations) {
  if (!value.current && !value.closing.length) {
    localStorage.removeItem(storageKey);
    return;
  }
  const text = JSON.stringify({ version: 2, ...value });
  if (byteLength(text) > MAX_ENVELOPE_BYTES) throw new Error('aside_presentation_too_large');
  localStorage.setItem(storageKey, text);
}

export function reserveAsidePresentation(value: AsidePresentation): void {
  const { parentFrameId, asSession, intentId } = value.intent;
  const storageKey = key(parentFrameId, asSession);
  if (!storageKey) throw new Error('aside_presentation_owner_unavailable');
  if (byteLength(JSON.stringify(value)) > MAX_HINT_BYTES) throw new Error('aside_presentation_too_large');
  const previous = readStoredPresentations(storageKey, parentFrameId, asSession);
  if (previous.closing.some((hint) => hint.intent.intentId === intentId))
    throw new Error('aside_presentation_replaced');
  if (
    previous.current &&
    (previous.current.intent.intentId === intentId || asSession || !previous.current.closeRequested)
  )
    throw new Error('aside_presentation_replaced');
  if (previous.current && previous.closing.length >= MAX_CLOSING_HINTS)
    throw new Error('aside_presentation_closing_limit');
  writePresentations(storageKey, {
    current: value,
    closing: previous.current ? [...previous.closing, previous.current] : previous.closing,
  });
}

export function saveAsidePresentation(value: AsidePresentation): void {
  const { parentFrameId, asSession } = value.intent;
  const storageKey = key(parentFrameId, asSession);
  if (!storageKey) throw new Error('aside_presentation_owner_unavailable');
  if (byteLength(JSON.stringify(value)) > MAX_HINT_BYTES) throw new Error('aside_presentation_too_large');
  const previous = readStoredPresentations(storageKey, parentFrameId, asSession);
  if (previous.current && sameIntent(previous.current.intent, value.intent)) {
    writePresentations(storageKey, { ...previous, current: value });
    return;
  }
  const index = previous.closing.findIndex((hint) => sameIntent(hint.intent, value.intent));
  if (index === -1 || !value.closeRequested) throw new Error('aside_presentation_replaced');
  previous.closing[index] = value;
  writePresentations(storageKey, previous);
}

export function clearAsidePresentation(intent: XScienceAsideIntent): void {
  const storageKey = key(intent.parentFrameId, intent.asSession);
  if (!storageKey) return;
  const previous = readStoredPresentations(storageKey, intent.parentFrameId, intent.asSession);
  const current = previous.current && !sameIntent(previous.current.intent, intent) ? previous.current : null;
  const closing = previous.closing.filter((hint) => !sameIntent(hint.intent, intent));
  if (current !== previous.current || closing.length !== previous.closing.length)
    writePresentations(storageKey, { current, closing });
}

function normalizeReceipt(value: unknown, intent: XScienceAsideIntent): XScienceAsideReceipt | null {
  if (!record(value)) return null;
  try {
    return requireXScienceAsideReceipt(
      {
        accepted: true,
        frame_id: value.frameId,
        intent_id: value.intentId,
        input_message_id: value.inputMessageId,
        source_branch_id: value.sourceBranchId,
        source_generation: value.sourceGeneration,
        as_session: value.asSession,
        replayed: value.replayed,
        through_publication_sequence: value.throughPublicationSequence,
        ...(value.sourceSpecialist !== undefined ? { source_specialist: value.sourceSpecialist } : {}),
        ...(value.specialistBinding !== undefined ? { specialist_binding: value.specialistBinding } : {}),
      },
      intent
    );
  } catch {
    return null;
  }
}

function normalizeFollowup(
  value: unknown,
  intent: XScienceAsideIntent,
  receipt: XScienceAsideReceipt | null
): XScienceAsideFollowup | null {
  if (
    !record(value) ||
    typeof value.loadingId !== 'string' ||
    !UUID.test(value.loadingId) ||
    typeof value.text !== 'string' ||
    !value.text.trim() ||
    !record(value.sessionOptions)
  )
    return null;
  const options = value.sessionOptions;
  if (
    typeof options.ultra_mode !== 'boolean' ||
    !['on', 'off'].includes(String(options.verifier_mode)) ||
    !['on', 'off'].includes(String(options.memory_mode)) ||
    (options.target_agent !== undefined && (typeof options.target_agent !== 'string' || !options.target_agent)) ||
    (options.target_agent === undefined && !intent.sourceSpecialist) ||
    typeof options.target_branch_id !== 'string' ||
    !/^br_[0-9a-f]{8}$/.test(options.target_branch_id) ||
    options.expected_branch_id !== options.target_branch_id ||
    !Number.isSafeInteger(options.expected_generation) ||
    Number(options.expected_generation) < 1 ||
    (options.model !== undefined && typeof options.model !== 'string')
  )
    return null;
  const specialistBinding = decodeAsideFollowupSpecialist(options.specialist_binding, intent, receipt, {
    id: options.target_branch_id as string,
    generation: Number(options.expected_generation),
  });
  return {
    loadingId: value.loadingId,
    text: value.text,
    sessionOptions: {
      ultra_mode: options.ultra_mode,
      verifier_mode: options.verifier_mode as 'on' | 'off',
      memory_mode: options.memory_mode as 'on' | 'off',
      ...(typeof options.target_agent === 'string' ? { target_agent: options.target_agent } : {}),
      ...(specialistBinding ? { specialist_binding: specialistBinding } : {}),
      target_branch_id: options.target_branch_id,
      expected_branch_id: options.target_branch_id,
      expected_generation: Number(options.expected_generation),
      ...(typeof options.model === 'string' && options.model ? { model: options.model } : {}),
    },
  };
}
