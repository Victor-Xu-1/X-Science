import {
  decodeLiteratureReadingStatus,
  decodeLiteratureReadingStatusCommand,
  decodeLiteratureReadingStatusReceipt,
  LITERATURE_READING_STATUS_BATCH_MAX,
  LITERATURE_READING_STATUS_BODY_BYTES,
  type LiteratureReadingStatus,
  type LiteratureReadingStatusCommand,
} from '@/common/adapter/literatureReadingStatus';

export type LiteratureReadingBulkIntent = Readonly<{
  schema: 'x-science.library-reading-bulk.v1';
  kind: 'reading-status-bulk';
  operation_id: string;
  scope_key: string;
  reading_status: LiteratureReadingStatus;
  batches: readonly LiteratureReadingStatusCommand[];
  confirmed_batches: number;
  phase: 'ready' | 'sending';
  paused: boolean;
}>;

const keys = [
  'schema',
  'kind',
  'operation_id',
  'scope_key',
  'reading_status',
  'batches',
  'confirmed_batches',
  'phase',
  'paused',
];
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const fail = (): never => {
  throw new Error('Invalid Library reading bulk intent');
};
function scopeKey(value: unknown): string {
  if (
    typeof value !== 'string' ||
    !value ||
    value.trim() !== value ||
    /[\p{Cc}]/u.test(value) ||
    [...value].some((char) => char.length === 1 && char.charCodeAt(0) >= 0xd800 && char.charCodeAt(0) <= 0xdfff)
  )
    return fail();
  return value;
}
const utf8Bytes = (value: string) => new TextEncoder().encode(value).byteLength;

/** Match the existing POST body, which does not send the local command kind. */
function bodyBytes(command: LiteratureReadingStatusCommand): number {
  return utf8Bytes(
    JSON.stringify({
      operation_id: command.operation_id,
      item_ids: command.item_ids,
      reading_status: command.reading_status,
    })
  );
}

/** A discriminator for already decoded pending records; untrusted values still require decode. */
export function isLiteratureReadingBulk(value: unknown): value is LiteratureReadingBulkIntent {
  return record(value) && value.schema === 'x-science.library-reading-bulk.v1' && value.kind === 'reading-status-bulk';
}

export function decodeLiteratureReadingBulk(value: unknown): LiteratureReadingBulkIntent {
  if (
    !record(value) ||
    Reflect.ownKeys(value).length !== keys.length ||
    !Reflect.ownKeys(value).every((key) => typeof key === 'string' && keys.includes(key)) ||
    !isLiteratureReadingBulk(value) ||
    !Array.isArray(value.batches) ||
    value.batches.length === 0 ||
    !Number.isSafeInteger(value.confirmed_batches) ||
    (value.confirmed_batches as number) < 0 ||
    (value.confirmed_batches as number) > value.batches.length ||
    typeof value.paused !== 'boolean' ||
    (value.phase !== 'ready' && value.phase !== 'sending')
  )
    return fail();
  const status = decodeLiteratureReadingStatus(value.reading_status);
  const scope = scopeKey(value.scope_key);
  const batches: LiteratureReadingStatusCommand[] = [];
  const operations = new Set<string>();
  const ids = new Set<string>();
  for (const candidate of value.batches) {
    const command = decodeLiteratureReadingStatusCommand(candidate);
    if (
      command.reading_status !== status ||
      operations.has(command.operation_id) ||
      bodyBytes(command) > LITERATURE_READING_STATUS_BODY_BYTES
    )
      return fail();
    operations.add(command.operation_id);
    for (const id of command.item_ids) {
      if (typeof id !== 'string' || ids.has(id)) return fail();
      ids.add(id);
    }
    batches.push(command);
  }
  // Reuse the existing UUID authority instead of defining another UUID grammar.
  const root = decodeLiteratureReadingStatusCommand({ ...batches[0], operation_id: value.operation_id }).operation_id;
  if (operations.has(root)) return fail();
  return Object.freeze({
    schema: 'x-science.library-reading-bulk.v1',
    kind: 'reading-status-bulk',
    operation_id: root,
    scope_key: scope,
    reading_status: status,
    batches: Object.freeze(batches),
    confirmed_batches: value.confirmed_batches as number,
    phase: value.phase,
    paused: value.paused,
  });
}

export function createLiteratureReadingBulk(
  ids: readonly string[],
  readingStatus: LiteratureReadingStatus,
  queryScope: string,
  uuid: () => string
): LiteratureReadingBulkIntent {
  if (!Array.isArray(ids) || ids.length === 0 || typeof uuid !== 'function') return fail();
  const status = decodeLiteratureReadingStatus(readingStatus);
  const scope = scopeKey(queryScope);
  const root = uuid();
  const unique = new Set<string>();
  for (let offset = 0; offset < ids.length; offset += LITERATURE_READING_STATUS_BATCH_MAX) {
    const checked = decodeLiteratureReadingStatusCommand({
      kind: 'set-reading-status',
      operation_id: root,
      item_ids: ids.slice(offset, offset + LITERATURE_READING_STATUS_BATCH_MAX),
      reading_status: status,
    });
    for (const id of checked.item_ids) {
      if (typeof id !== 'string') return fail();
      unique.add(id);
    }
  }
  const emptyBytes = bodyBytes({
    kind: 'set-reading-status',
    operation_id: root,
    item_ids: [],
    reading_status: status,
  });
  const operations = new Set([root]);
  const batches: LiteratureReadingStatusCommand[] = [];
  let items: string[] = [];
  let bytes = emptyBytes;
  const finish = () => {
    const operationId = uuid();
    if (operations.has(operationId)) return fail();
    const command = decodeLiteratureReadingStatusCommand({
      kind: 'set-reading-status',
      operation_id: operationId,
      item_ids: items,
      reading_status: status,
    });
    if (bodyBytes(command) > LITERATURE_READING_STATUS_BODY_BYTES) return fail();
    operations.add(operationId);
    batches.push(command);
    items = [];
    bytes = emptyBytes;
  };
  for (const id of unique) {
    const size = utf8Bytes(JSON.stringify(id));
    if (
      items.length &&
      (items.length === LITERATURE_READING_STATUS_BATCH_MAX || bytes + size + 1 > LITERATURE_READING_STATUS_BODY_BYTES)
    )
      finish();
    if (bytes + size > LITERATURE_READING_STATUS_BODY_BYTES) return fail();
    bytes += size + (items.length ? 1 : 0);
    items.push(id);
  }
  if (items.length) finish();
  return Object.freeze({
    schema: 'x-science.library-reading-bulk.v1',
    kind: 'reading-status-bulk',
    operation_id: root,
    scope_key: scope,
    reading_status: status,
    batches: Object.freeze(batches),
    confirmed_batches: 0,
    phase: 'ready',
    paused: false,
  });
}

/** A fully confirmed restore replays the last original child to obtain its real ACK. */
export function currentLiteratureReadingBulkCommand(
  intent: LiteratureReadingBulkIntent
): LiteratureReadingStatusCommand {
  if (
    !Number.isSafeInteger(intent.confirmed_batches) ||
    intent.confirmed_batches < 0 ||
    intent.confirmed_batches > intent.batches.length
  )
    return fail();
  return intent.batches[Math.min(intent.confirmed_batches, intent.batches.length - 1)] ?? fail();
}

export function claimLiteratureReadingBulk(intent: LiteratureReadingBulkIntent): LiteratureReadingBulkIntent {
  currentLiteratureReadingBulkCommand(intent);
  return intent.phase === 'sending' ? intent : Object.freeze({ ...intent, phase: 'sending' });
}

/** Only an exact decoded child ACK can move the confirmed position. The slot owner persists it. */
export function advanceLiteratureReadingBulk(
  intent: LiteratureReadingBulkIntent,
  actualAck: unknown
): LiteratureReadingBulkIntent {
  if (intent.phase !== 'sending') return fail();
  decodeLiteratureReadingStatusReceipt(actualAck, currentLiteratureReadingBulkCommand(intent));
  return Object.freeze({
    ...intent,
    phase: 'ready',
    confirmed_batches: Math.min(intent.confirmed_batches + 1, intent.batches.length),
  });
}

export function literatureReadingBulkProgress(intent: LiteratureReadingBulkIntent) {
  currentLiteratureReadingBulkCommand(intent);
  return {
    confirmedBatches: intent.confirmed_batches,
    totalBatches: intent.batches.length,
    confirmedItems: intent.batches
      .slice(0, intent.confirmed_batches)
      .reduce((sum, batch) => sum + batch.item_ids.length, 0),
    totalItems: intent.batches.reduce((sum, batch) => sum + batch.item_ids.length, 0),
    phase: intent.phase,
  };
}
