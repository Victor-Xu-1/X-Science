/** Personal reading state; independent of PDF progress and metadata revisions. */
export const literatureReadingStatuses = ['unread', 'reading', 'read'] as const;
export type LiteratureReadingStatus = (typeof literatureReadingStatuses)[number];

export function decodeLiteratureReadingStatus(value: unknown): LiteratureReadingStatus {
  if (typeof value !== 'string' || !literatureReadingStatuses.some((status) => status === value))
    throw new Error('Invalid literature reading status');
  return value as LiteratureReadingStatus;
}

export type LiteratureReadingStatusCommand = Readonly<{
  kind: 'set-reading-status';
  operation_id: string;
  item_ids: readonly string[];
  reading_status: LiteratureReadingStatus;
}>;
export type LiteratureReadingStatusReceipt = Readonly<{
  schema: 'x-science.literature-reading-status-receipt.v1';
  operation_id: string;
  item_ids: readonly string[];
  reading_status: LiteratureReadingStatus;
}>;

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const exactKeys = (value: Record<string, unknown>, names: string[]) =>
  Reflect.ownKeys(value).length === names.length &&
  Reflect.ownKeys(value).every((key) => typeof key === 'string' && names.includes(key));
const uuid = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value);
const itemId = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 512 &&
  value.trim() === value &&
  !/[\p{Cc}]/u.test(value) &&
  [...value].every((char) => char.length !== 1 || char.charCodeAt(0) < 0xd800 || char.charCodeAt(0) > 0xdfff);
const fail = (): never => {
  throw new Error('Invalid literature reading-status contract');
};

export function decodeLiteratureReadingStatusCommand(value: unknown): LiteratureReadingStatusCommand {
  if (
    !record(value) ||
    !exactKeys(value, ['kind', 'operation_id', 'item_ids', 'reading_status']) ||
    value.kind !== 'set-reading-status' ||
    !uuid(value.operation_id) ||
    !Array.isArray(value.item_ids) ||
    value.item_ids.length < 1 ||
    value.item_ids.length > 200 ||
    !value.item_ids.every(itemId)
  )
    return fail();
  // Keep the original target order and repetitions for exact request identity.
  // The Store alone normalizes execution targets after binding the receipt.
  return Object.freeze({
    kind: value.kind,
    operation_id: value.operation_id,
    item_ids: Object.freeze([...value.item_ids]),
    reading_status: decodeLiteratureReadingStatus(value.reading_status),
  });
}

export function decodeLiteratureReadingStatusReceipt(
  value: unknown,
  command: LiteratureReadingStatusCommand
): LiteratureReadingStatusReceipt {
  const expected = [...new Set(command.item_ids)];
  if (
    !record(value) ||
    !exactKeys(value, ['schema', 'operation_id', 'item_ids', 'reading_status']) ||
    value.schema !== 'x-science.literature-reading-status-receipt.v1' ||
    value.operation_id !== command.operation_id ||
    value.reading_status !== command.reading_status ||
    !Array.isArray(value.item_ids) ||
    value.item_ids.length !== expected.length ||
    !value.item_ids.every((id, index) => id === expected[index])
  )
    return fail();
  return Object.freeze({
    schema: value.schema,
    operation_id: command.operation_id,
    item_ids: Object.freeze([...expected]),
    reading_status: command.reading_status,
  });
}
