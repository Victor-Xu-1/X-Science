import { sha256 } from '@noble/hashes/sha2.js';
import { bytesToHex, utf8ToBytes } from '@noble/hashes/utils.js';
import type { IMessageToolCall, TMessage, XScienceMessageCoordinates } from './chatLib';
import { toolObservationBranch, toolObservationInputsCompatible } from './toolProgressObservation';

const identifier = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 512 &&
  value.trim() === value &&
  !Array.from(value).some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127);
const positive = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) > 0;
const inputOf = (message: IMessageToolCall) => message.content.input ?? message.content.args;

function canonicalCoordinates(message: IMessageToolCall): XScienceMessageCoordinates | undefined {
  const value = message.content.xScience;
  if (
    !value ||
    Object.keys(value).length !== 3 ||
    Object.keys(value).some((key) => !['messageIndex', 'blockIndex', 'branchId'].includes(key)) ||
    !Number.isSafeInteger(value.messageIndex) ||
    value.messageIndex < 0 ||
    !Number.isSafeInteger(value.blockIndex) ||
    value.blockIndex < 0 ||
    !toolObservationBranch(value.branchId)
  )
    return undefined;
  return value;
}

function coordinatesCompatible(message: IMessageToolCall, source: XScienceMessageCoordinates): boolean {
  const value = message.content.xScience;
  if (value === undefined) return true;
  if (!value || (value.branchId !== undefined && value.branchId !== null)) return false;
  // Unknown extra origin coordinates (including a future stream/frame
  // incarnation) are not silently discarded to acquire branch authority.
  return Object.keys(value).every(
    (key) =>
      key === 'branchId' ||
      (key === 'messageIndex' && value.messageIndex === source.messageIndex) ||
      (key === 'blockIndex' && value.blockIndex === source.blockIndex)
  );
}

function hasDurableScope(message: IMessageToolCall): boolean {
  return (
    identifier(message.id) &&
    identifier(message.conversation_id) &&
    identifier(message.content.name) &&
    identifier(message.content.call_id) &&
    identifier(message.content.operation_id) &&
    positive(message.content.attempt) &&
    positive(message.content.revision)
  );
}

function sameDurableScope(source: IMessageToolCall, message: IMessageToolCall): boolean {
  const sourceInput = inputOf(source);
  return (
    source.id === message.id &&
    source.conversation_id === message.conversation_id &&
    source.content.name === message.content.name &&
    source.content.call_id === message.content.call_id &&
    source.content.operation_id === message.content.operation_id &&
    source.content.attempt === message.content.attempt &&
    sourceInput !== undefined &&
    sourceInput !== null &&
    toolObservationInputsCompatible(sourceInput, inputOf(message)) &&
    (!source.content.parent_operation_id ||
      !message.content.parent_operation_id ||
      source.content.parent_operation_id === message.content.parent_operation_id)
  );
}

/** The existing server ID namespace only rejects coarse compatibility merging;
 * it never supplies a branch, an attempt or permission to execute anything. */
export function isNativeToolPublication(message: IMessageToolCall): boolean {
  return identifier(message.id) && message.id.startsWith('transcript-tool:');
}

export function canonicalToolPublicationScopeMatches(source: IMessageToolCall, message: IMessageToolCall): boolean {
  if (
    !hasDurableScope(source) ||
    !hasDurableScope(message) ||
    (!sameDurableScope(source, message) && !sameDurableScope(message, source))
  )
    return false;
  const previous = source.content.xScience;
  const next = message.content.xScience;
  if (previous === undefined || next === undefined) return previous === next;
  if (!previous || !next) return false;
  const oldCoordinates = previous as unknown as Record<string, unknown>;
  const newCoordinates = next as unknown as Record<string, unknown>;
  const keys = Object.keys(oldCoordinates);
  return (
    keys.length === Object.keys(newCoordinates).length &&
    keys.every(
      (key) =>
        Object.hasOwn(newCoordinates, key) &&
        JSON.stringify(oldCoordinates[key]) === JSON.stringify(newCoordinates[key])
    )
  );
}

/** Presentation only: never a request, hydration or physical-execution ID.
 * Hashing keeps immutable input/code out of DOM and disclosure keys. */
export function nativeToolPresentationKey(message: IMessageToolCall): string {
  const coordinates = message.content.xScience;
  let scope: string;
  try {
    scope = JSON.stringify([
      message.id,
      message.conversation_id,
      message.content.name,
      message.content.call_id,
      message.content.operation_id,
      message.content.parent_operation_id,
      message.content.attempt,
      coordinates && [coordinates.branchId, coordinates.messageIndex, coordinates.blockIndex],
      inputOf(message),
    ]);
  } catch {
    // A malformed non-JSON observation cannot acquire a shared detail scope.
    scope = JSON.stringify([message.id]);
  }
  return `native-tool:${bytesToHex(sha256(utf8ToBytes(scope)))}`;
}

/**
 * A short-lived index of the current account-owned message list. Only an exact
 * durable row counterpart can bind a branchless live publication. It does not
 * use selected/window branch, text similarity, a cache or a transport owner.
 */
export function createCanonicalToolBranchResolver(messages: readonly TMessage[]) {
  const byId = new Map<string, IMessageToolCall[]>();
  for (const message of messages) {
    if (message.type !== 'tool_call' || !hasDurableScope(message) || !canonicalCoordinates(message)) continue;
    const sources = byId.get(message.id) ?? [];
    sources.push(message);
    byId.set(message.id, sources);
  }
  return (message: IMessageToolCall): IMessageToolCall => {
    if (toolObservationBranch(message.content.xScience?.branchId) || !hasDurableScope(message)) return message;
    let matched: IMessageToolCall | undefined;
    for (const source of byId.get(message.id) ?? []) {
      const coordinates = canonicalCoordinates(source)!;
      if (!sameDurableScope(source, message) || !coordinatesCompatible(message, coordinates)) continue;
      if (matched && !canonicalToolPublicationScopeMatches(matched, source)) return message;
      matched = source;
    }
    return matched
      ? { ...message, content: { ...message.content, xScience: { ...matched.content.xScience! } } }
      : message;
  };
}
