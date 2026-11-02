import { isBackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeResourceTagCommand,
  decodeResourceTagRef,
  type ResourceTagCommand,
  type ResourceTagRef,
  type ResourceTagSnapshot,
} from '@/common/adapter/resourceTags';
import {
  createRendererMutationSlot,
  withRendererMutationLock,
  type RendererMutationStorage,
} from './rendererMutationSlot';

type TagWrite = ResourceTagCommand;
export type ResourceTagRejection = Readonly<{ status: number; code: string }>;
export type ResourceTagQueueEntry = Readonly<{
  group_id: string;
  command: TagWrite;
  continuation: Readonly<{ target: ResourceTagRef; operation_id: string }> | null;
  phase: 'ready' | 'unknown' | 'readback';
  minimum_revision: number;
  rejection: ResourceTagRejection | null;
}>;
export type ResourceTagMutationQueue = Readonly<{
  schema: 'x-science.resource-tag-mutation-queue.v1';
  owner_id: string;
  entries: readonly ResourceTagQueueEntry[];
}>;
const reject = (): never => {
  throw new Error('Invalid resource tag mutation queue');
};
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const keys = (value: Record<string, unknown>, expected: readonly string[]) => {
  if (Reflect.ownKeys(value).length !== expected.length || !expected.every((key) => Object.hasOwn(value, key)))
    reject();
};
const operation = (value: unknown): string =>
  typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value)
    ? value
    : reject();
const rejectionCodes: Readonly<Record<string, number>> = {
  RESOURCE_TAG_INVALID: 400,
  RESOURCE_TAG_UNAVAILABLE: 404,
  RESOURCE_TAG_PROTECTED: 409,
  RESOURCE_TAG_NAME_CONFLICT: 409,
  RESOURCE_TAG_CONFLICT: 409,
  RESOURCE_TAG_OPERATION_CONFLICT: 409,
};
export function resourceTagDefiniteRejection(error: unknown): ResourceTagRejection | null {
  if (!isBackendHttpError(error) || rejectionCodes[error.code] !== error.status) return null;
  return Object.freeze({ status: error.status, code: error.code });
}
export function decodeResourceTagQueueEntry(value: unknown): ResourceTagQueueEntry {
  if (!object(value)) return reject();
  keys(value, ['group_id', 'command', 'continuation', 'phase', 'minimum_revision', 'rejection']);
  const group_id = operation(value.group_id),
    command = decodeResourceTagCommand(value.command);
  if (value.phase !== 'ready' && value.phase !== 'unknown' && value.phase !== 'readback') return reject();
  const minimum_revision = value.minimum_revision;
  if (typeof minimum_revision !== 'number' || !Number.isSafeInteger(minimum_revision) || minimum_revision < 0)
    return reject();
  let continuation: ResourceTagQueueEntry['continuation'] = null;
  if (value.continuation !== null) {
    if (!object(value.continuation)) return reject();
    keys(value.continuation, ['target', 'operation_id']);
    const target = decodeResourceTagRef(value.continuation.target),
      operation_id = operation(value.continuation.operation_id);
    if (
      !object(value.continuation.target) ||
      target.resource_id !== value.continuation.target.resource_id ||
      operation_id === group_id
    )
      return reject();
    continuation = Object.freeze({ target, operation_id });
  }
  if (command.kind === 'create') {
    if (group_id !== command.operation_id) return reject();
  } else if (continuation) {
    if (
      command.kind !== 'assignment' ||
      command.operation_id !== continuation.operation_id ||
      command.resource_type !== continuation.target.resource_type ||
      command.resource_id !== continuation.target.resource_id ||
      !command.assigned
    )
      return reject();
  } else if (group_id !== command.operation_id) return reject();
  let rejection: ResourceTagRejection | null = null;
  if (value.rejection !== null) {
    if (!object(value.rejection)) return reject();
    keys(value.rejection, ['status', 'code']);
    if (
      typeof value.rejection.code !== 'string' ||
      !Object.hasOwn(rejectionCodes, value.rejection.code) ||
      rejectionCodes[value.rejection.code] !== value.rejection.status ||
      value.phase !== 'readback'
    )
      return reject();
    rejection = Object.freeze({ status: Number(value.rejection.status), code: value.rejection.code });
  }
  return Object.freeze({ group_id, command, continuation, phase: value.phase, minimum_revision, rejection });
}
export function decodeResourceTagMutationQueue(value: unknown, ownerId: string): ResourceTagMutationQueue {
  if (!object(value)) return reject();
  keys(value, ['schema', 'owner_id', 'entries']);
  if (
    value.schema !== 'x-science.resource-tag-mutation-queue.v1' ||
    value.owner_id !== ownerId ||
    !Array.isArray(value.entries) ||
    Reflect.ownKeys(value.entries).length !== value.entries.length + 1 ||
    !value.entries.length
  )
    return reject();
  const entries = Array.from(value.entries).map(decodeResourceTagQueueEntry);
  const groups = new Set(entries.map((entry) => entry.group_id));
  const operations = entries.flatMap((entry) => [
    entry.command.operation_id,
    ...(entry.command.kind === 'create' && entry.continuation ? [entry.continuation.operation_id] : []),
  ]);
  if (
    groups.size !== entries.length ||
    new Set(operations).size !== operations.length ||
    entries.slice(1).some((entry) => entry.phase !== 'ready')
  )
    return reject();
  return Object.freeze({ schema: value.schema, owner_id: ownerId, entries: Object.freeze(entries) });
}
const same = (left: unknown, right: unknown) => JSON.stringify(left) === JSON.stringify(right);
/** One stable account slot; mutations hold the existing same-origin lock and preserve concurrent tails. */
export function createResourceTagMutationQueue(options: {
  ownerId: string;
  current: () => boolean;
  storage: () => RendererMutationStorage;
}) {
  if (!options.ownerId || options.ownerId.trim() !== options.ownerId || /[\uD800-\uDFFF]/u.test(options.ownerId))
    return reject();
  const key = `x-science.resource-tag-mutations.v1:${encodeURIComponent(options.ownerId)}`;
  const slot = createRendererMutationSlot<ResourceTagMutationQueue>({
    key,
    storage: options.storage,
    decode: (value) => decodeResourceTagMutationQueue(value, options.ownerId),
    encode: (value) => JSON.stringify(decodeResourceTagMutationQueue(value, options.ownerId)),
    same,
  });
  const locked = <T>(critical: () => T) =>
    withRendererMutationLock(key, options.current, critical, { label: 'Resource tag' });
  const write = (actual: ResourceTagMutationQueue | null, entries: readonly ResourceTagQueueEntry[]) => {
    if (!entries.length) {
      if (actual) slot.settle(actual, true);
    } else
      slot.replace(
        actual,
        decodeResourceTagMutationQueue(
          {
            schema: 'x-science.resource-tag-mutation-queue.v1',
            owner_id: options.ownerId,
            entries,
          },
          options.ownerId
        )
      );
  };
  return {
    key,
    sendKey: `${key}:send`,
    read: slot.read,
    append: (entry: ResourceTagQueueEntry) =>
      locked(() => {
        const actual = slot.read();
        write(actual, [...(actual?.entries ?? []), decodeResourceTagQueueEntry(entry)]);
      }),
    replaceHead: (expected: ResourceTagQueueEntry, next: ResourceTagQueueEntry | null) =>
      locked(() => {
        const actual = slot.read();
        if (!actual || !same(actual.entries[0], expected)) return false;
        write(actual, [...(next ? [decodeResourceTagQueueEntry(next)] : []), ...actual.entries.slice(1)]);
        return true;
      }),
  };
}

/** Checkbox projection is derived, never a second annotation/catalog store. */
export function projectResourceTagTarget(
  snapshot: ResourceTagSnapshot | null,
  pending: readonly ResourceTagQueueEntry[],
  target: ResourceTagRef,
  busy: boolean
) {
  const ref = decodeResourceTagRef(target);
  const matches = (candidate: ResourceTagRef) =>
    candidate.resource_type === ref.resource_type && candidate.resource_id === ref.resource_id;
  const assigned = new Set(snapshot?.assignments.filter(matches).map((item) => item.tag_id) ?? []);
  for (const entry of pending) {
    if (entry.rejection || entry.command.kind !== 'assignment' || !matches(entry.command)) continue;
    if (entry.command.assigned) assigned.add(entry.command.tag_id);
    else assigned.delete(entry.command.tag_id);
  }
  const first = pending[0];
  return {
    assignedIds: [...assigned],
    pendingTagIds:
      busy && first && !first.rejection && first.command.kind === 'assignment' && matches(first.command)
        ? [first.command.tag_id]
        : [],
    creating: pending.some((entry) => !entry.rejection && entry.continuation && matches(entry.continuation.target)),
  };
}

export function resourceTagAssignmentEntry(
  target: ResourceTagRef,
  tagId: string,
  assigned: boolean,
  operationId: string
) {
  return decodeResourceTagQueueEntry({
    group_id: operationId,
    command: {
      kind: 'assignment',
      operation_id: operationId,
      ...decodeResourceTagRef(target),
      tag_id: tagId,
      assigned,
    },
    continuation: null,
    phase: 'ready',
    minimum_revision: 0,
    rejection: null,
  });
}
export function resourceTagCreationEntry(target: ResourceTagRef, name: string, createId: string, assignmentId: string) {
  return decodeResourceTagQueueEntry({
    group_id: createId,
    command: { kind: 'create', operation_id: createId, name, icon_key: 'tag', color_key: 'blue' },
    continuation: { target: decodeResourceTagRef(target), operation_id: assignmentId },
    phase: 'ready',
    minimum_revision: 0,
    rejection: null,
  });
}

/** Catalog and per-resource writes share the same versioned durable queue. */
export function resourceTagCatalogEntry(command: ResourceTagCommand) {
  if (command.kind === 'assignment') return reject();
  return decodeResourceTagQueueEntry({
    group_id: command.operation_id,
    command,
    continuation: null,
    phase: 'ready',
    minimum_revision: 0,
    rejection: null,
  });
}
