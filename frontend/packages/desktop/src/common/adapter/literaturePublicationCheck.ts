export type LiteraturePublicationIssue =
  | 'doi-required'
  | 'unavailable'
  | 'rate-limited'
  | 'timeout'
  | 'response-invalid'
  | 'not-found';

export type LiteraturePublicationNotice = {
  doi: string;
  type: string;
  date?: string;
  source: 'crossref' | 'publisher' | 'retraction-watch';
};
export type LiteraturePublicationSnapshot = {
  doi: string;
  checked_at: string;
  notices: LiteraturePublicationNotice[];
};
export type LiteraturePublicationCheck = {
  schema: 'x-science.literature-publication-check.v1';
  item_id: string;
  metadata_revision: number;
  doi?: string;
  snapshot?: LiteraturePublicationSnapshot;
  issue?: LiteraturePublicationIssue;
};
export type LiteraturePublicationCommand = {
  kind: 'check-publication';
  operation_id: string;
  item_id: string;
  expected_metadata_revision: number;
};
export type LiteraturePublicationReceipt = {
  schema: 'x-science.literature-publication-receipt.v1';
  operation_id: string;
  result: LiteraturePublicationCheck;
};

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const keys = (value: Record<string, unknown>, required: string[], optional: string[] = []) =>
  required.every((name) => Object.hasOwn(value, name)) &&
  Reflect.ownKeys(value).every((name) => typeof name === 'string' && [...required, ...optional].includes(name));
const identity = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 512 && value.trim() === value;
const positive = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
const doi = (value: unknown): value is string =>
  typeof value === 'string' && value.length <= 2048 && /^10\.[0-9]{4,9}\/[^\s\p{Cc}]+$/u.test(value);
const issue = (value: unknown): value is LiteraturePublicationIssue =>
  typeof value === 'string' &&
  ['doi-required', 'unavailable', 'rate-limited', 'timeout', 'response-invalid', 'not-found'].includes(value);
const fail = (): never => {
  throw new Error('Invalid literature publication-check contract');
};
function date(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^[1-9][0-9]{3}-[0-9]{2}-[0-9]{2}$/.test(value) &&
    Number.isFinite(Date.parse(value + 'T00:00:00Z')) &&
    new Date(value + 'T00:00:00Z').toISOString().slice(0, 10) === value
  );
}
function timestamp(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^[1-9][0-9]{3}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) &&
    Number.isFinite(Date.parse(value)) &&
    new Date(value).toISOString().slice(0, 19) === value.slice(0, 19)
  );
}
function decodeSnapshot(value: unknown): LiteraturePublicationSnapshot {
  if (
    !record(value) ||
    !keys(value, ['doi', 'checked_at', 'notices']) ||
    !doi(value.doi) ||
    !timestamp(value.checked_at) ||
    !Array.isArray(value.notices) ||
    value.notices.length > 100
  )
    return fail();
  const notices = value.notices.map((notice): LiteraturePublicationNotice => {
    if (
      !record(notice) ||
      !keys(notice, ['doi', 'type', 'source'], ['date']) ||
      !doi(notice.doi) ||
      typeof notice.type !== 'string' ||
      !notice.type.trim() ||
      notice.type.trim() !== notice.type ||
      notice.type.length > 100 ||
      typeof notice.source !== 'string' ||
      !['crossref', 'publisher', 'retraction-watch'].includes(notice.source) ||
      (Object.hasOwn(notice, 'date') && !date(notice.date))
    )
      return fail();
    return {
      doi: notice.doi,
      type: notice.type,
      source: notice.source as LiteraturePublicationNotice['source'],
      ...(notice.date !== undefined ? { date: notice.date as string } : {}),
    };
  });
  return { doi: value.doi, checked_at: value.checked_at, notices };
}
export function decodeLiteraturePublicationCheck(value: unknown): LiteraturePublicationCheck {
  if (
    !record(value) ||
    !keys(value, ['schema', 'item_id', 'metadata_revision'], ['doi', 'snapshot', 'issue']) ||
    value.schema !== 'x-science.literature-publication-check.v1' ||
    !identity(value.item_id) ||
    !positive(value.metadata_revision) ||
    (Object.hasOwn(value, 'doi') && !doi(value.doi)) ||
    (Object.hasOwn(value, 'issue') && !issue(value.issue))
  )
    return fail();
  const snapshot = Object.hasOwn(value, 'snapshot') ? decodeSnapshot(value.snapshot) : undefined;
  if (
    (snapshot && snapshot.doi !== value.doi) ||
    (value.issue === 'doi-required' && (value.doi !== undefined || snapshot)) ||
    (value.doi === undefined && value.issue !== 'doi-required')
  )
    return fail();
  return {
    schema: value.schema,
    item_id: value.item_id,
    metadata_revision: value.metadata_revision,
    ...(value.doi !== undefined ? { doi: value.doi as string } : {}),
    ...(snapshot ? { snapshot } : {}),
    ...(value.issue !== undefined ? { issue: value.issue as LiteraturePublicationIssue } : {}),
  };
}
export function decodeLiteraturePublicationCommand(value: unknown): LiteraturePublicationCommand {
  if (
    !record(value) ||
    !keys(value, ['kind', 'operation_id', 'item_id', 'expected_metadata_revision']) ||
    value.kind !== 'check-publication' ||
    typeof value.operation_id !== 'string' ||
    !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value.operation_id) ||
    !identity(value.item_id) ||
    !positive(value.expected_metadata_revision)
  )
    return fail();
  return {
    kind: value.kind,
    operation_id: value.operation_id,
    item_id: value.item_id,
    expected_metadata_revision: value.expected_metadata_revision,
  };
}
export function decodeLiteraturePublicationReceipt(value: unknown, operationId: string): LiteraturePublicationReceipt {
  if (
    !record(value) ||
    !keys(value, ['schema', 'operation_id', 'result']) ||
    value.schema !== 'x-science.literature-publication-receipt.v1' ||
    value.operation_id !== operationId
  )
    return fail();
  const result = decodeLiteraturePublicationCheck(value.result);
  if (!result.snapshot && !result.issue) return fail();
  return { schema: value.schema, operation_id: operationId, result };
}
