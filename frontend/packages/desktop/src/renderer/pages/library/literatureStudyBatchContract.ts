import type { LiteratureStudyGenerate } from '@/common/adapter/literatureStudySnapshot';

export type LiteratureStudyBatchRow = Readonly<{
  item_id: string;
  request: LiteratureStudyGenerate;
  state: 'pending' | 'running' | 'succeeded' | 'failed';
  issue?: string;
  result_revision?: number;
  reused?: boolean;
}>;
export type LiteratureStudyBatch = Readonly<{
  id: string;
  revision: number;
  paused: boolean;
  rows: readonly LiteratureStudyBatchRow[];
  request_expires_at?: number;
}>;
export type LiteratureStudyBatchView = Readonly<{
  schema: 'x-science.literature-study-batch.v1';
  limit: number;
  concurrency: number;
  batch: LiteratureStudyBatch | null;
}>;
export type LiteratureStudyBatchCommand =
  | { action: 'start'; operation_id: string; language: string; item_ids: readonly string[] }
  | { action: 'advance'; batch_id: string; rows: readonly number[] }
  | { action: 'pause' | 'resume' | 'retry'; operation_id: string; batch_id: string; expected_revision: number };

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const keys = (value: Record<string, unknown>, required: string[], optional: string[] = []) =>
  required.every((key) => Object.hasOwn(value, key)) &&
  Object.keys(value).every((key) => [...required, ...optional].includes(key));
const uuid = (v: unknown): v is string =>
  typeof v === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v);
const integer = (v: unknown, min = 0): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= min;
const identity = (v: unknown): v is string =>
  typeof v === 'string' && v.length > 0 && v.length <= 4096 && v.trim() === v;
const fail = (): never => {
  throw new Error('Invalid Library batch overview contract');
};

export function decodeLiteratureStudyBatch(value: unknown): LiteratureStudyBatchView {
  if (
    !record(value) ||
    !keys(value, ['schema', 'limit', 'concurrency', 'batch']) ||
    value.schema !== 'x-science.literature-study-batch.v1' ||
    !integer(value.limit, 1) ||
    value.limit > 5 ||
    !integer(value.concurrency, 1) ||
    value.concurrency > 2
  )
    return fail();
  if (value.batch === null) return value as LiteratureStudyBatchView;
  const batch = value.batch;
  if (
    !record(batch) ||
    !keys(batch, ['id', 'revision', 'paused', 'rows'], ['request_expires_at']) ||
    !uuid(batch.id) ||
    !integer(batch.revision, 1) ||
    typeof batch.paused !== 'boolean' ||
    !Array.isArray(batch.rows) ||
    !batch.rows.length ||
    batch.rows.length > value.limit ||
    (batch.request_expires_at !== undefined && !integer(batch.request_expires_at, 1))
  )
    return fail();
  const seen = new Set<string>();
  for (const row of batch.rows) {
    if (
      !record(row) ||
      !keys(row, ['item_id', 'request', 'state'], ['issue', 'result_revision', 'reused']) ||
      !identity(row.item_id) ||
      seen.has(row.item_id) ||
      typeof row.state !== 'string' ||
      !['pending', 'running', 'succeeded', 'failed'].includes(row.state) ||
      (row.issue !== undefined && (typeof row.issue !== 'string' || row.issue.length > 128)) ||
      (row.result_revision !== undefined && !integer(row.result_revision, 1)) ||
      (row.reused !== undefined && typeof row.reused !== 'boolean')
    )
      return fail();
    const request = row.request;
    if (
      !record(request) ||
      !keys(request, ['operation_id', 'language', 'expected_source_key', 'expected_revision']) ||
      !uuid(request.operation_id) ||
      !identity(request.language) ||
      request.language.length > 64 ||
      !identity(request.expected_source_key) ||
      !integer(request.expected_revision) ||
      (row.state === 'succeeded' && !integer(row.result_revision, 1)) ||
      (row.state !== 'succeeded' && row.result_revision !== undefined)
    )
      return fail();
    seen.add(row.item_id);
  }
  return value as LiteratureStudyBatchView;
}
