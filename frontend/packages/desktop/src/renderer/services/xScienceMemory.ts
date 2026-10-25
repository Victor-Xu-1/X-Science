export type XScienceMemoryOptions = {
  baseUrl?: string;
  fetchImpl?: typeof fetch;
  signal?: AbortSignal;
  timeoutMs?: number;
  operationId?: string;
};

export type XScienceMemoryRow = {
  id: string;
  body: string;
  userId: string | null;
  categoryId: string | null;
  subjectProjectId: string | null;
  subjectArtifactId: string | null;
  subjectVersionId: string | null;
  subjectFrameId: string | null;
  sourceFrameId: string | null;
  origin: string;
  evidence: string;
  createdAt: string | null;
  updatedAt: string | null;
  lastSurfacedAt: string | null;
};

export type XScienceMemoryEntity = {
  entityKey: string;
  label: string;
  projectId: string | null;
  rows: XScienceMemoryRow[];
};

export type XScienceMemorySession = {
  frameId: string;
  projectId: string;
  label: string;
  rowCount: number;
  newestAt: string | null;
};

export type XScienceMemoryCategory = {
  id: string;
  name: string;
  guidance: string;
  autoRecall: boolean;
  rowCount: number;
};

export type XScienceMemoryContext = {
  profileMarkdown: string;
  listingMarkdown: string;
  enabled: boolean | null;
  totalRows: number;
  entities: XScienceMemoryEntity[];
  sessions: XScienceMemorySession[];
  categories: XScienceMemoryCategory[];
};

export type XScienceCreateMemoryInput = {
  text: string;
  entity: string;
  category?: string;
};

export type XScienceUpdateMemoryInput = {
  text: string;
  evidence?: string;
  category?: string | null;
};

export type XScienceMemoryCategoryInput = {
  name: string;
  guidance: string;
  autoRecall: boolean;
};

export type XScienceCreateMemoryCategoryInput = Omit<XScienceMemoryCategoryInput, 'autoRecall'> & {
  autoRecall?: boolean;
};

type RecordValue = Record<string, unknown>;

export async function loadXScienceMemoryContext(
  query: { projectId?: string } = {},
  options: XScienceMemoryOptions = {}
): Promise<XScienceMemoryContext> {
  const search = new URLSearchParams();
  if (query.projectId) search.set('project_id', query.projectId);
  const payload = asRecord(await requestJson(`/api/memory/context${search.size ? `?${search}` : ''}`, {}, options));
  return {
    profileMarkdown: stringValue(payload?.profile_md),
    listingMarkdown: stringValue(payload?.listing_md),
    enabled: nullableBooleanValue(payload?.memory_enabled),
    totalRows: numberValue(payload?.total_user_rows),
    entities: arrayValue(payload?.entities).map(toMemoryEntity).filter(isMemoryEntity),
    sessions: arrayValue(payload?.sessions).map(toMemorySession).filter(isMemorySession),
    categories: arrayValue(payload?.categories).map(toMemoryCategory).filter(isMemoryCategory),
  };
}

export async function loadXScienceMemoryEnabled(options: XScienceMemoryOptions = {}): Promise<boolean> {
  const payload = asRecord(await requestJson('/api/memory/enabled', {}, options));
  if (typeof payload?.enabled !== 'boolean') throw new Error('X-Science memory enabled response is invalid');
  return payload.enabled;
}

export async function loadXScienceAutoMemoryEnabled(options: XScienceMemoryOptions = {}): Promise<boolean> {
  const payload = asRecord(await requestJson('/api/memory/auto-enabled', {}, options));
  return payload?.enabled === true;
}

export async function loadXScienceSessionMemories(
  frameId: string,
  options: XScienceMemoryOptions = {}
): Promise<XScienceMemoryRow[]> {
  const payload = asRecord(await requestJson(`/api/memory/sessions/${encodeURIComponent(frameId)}`, {}, options));
  return arrayValue(payload?.rows).map(toMemoryRow).filter(isMemoryRow);
}

export async function setXScienceMemoryEnabled(enabled: boolean, options: XScienceMemoryOptions = {}): Promise<void> {
  await requestJson('/api/memory/enabled', jsonRequest('PUT', { enabled }), options);
}

export async function setXScienceAutoMemoryEnabled(
  enabled: boolean,
  options: XScienceMemoryOptions = {}
): Promise<void> {
  await requestJson('/api/memory/auto-enabled', jsonRequest('PUT', { enabled }), options);
}

export async function setXScienceProjectMemoryEnabled(
  projectId: string,
  enabled: boolean,
  options: XScienceMemoryOptions = {}
): Promise<void> {
  const normalizedProjectId = projectId.trim();
  if (!normalizedProjectId) throw new Error('X-Science project id is required');
  await requestJson(
    `/api/projects/${encodeURIComponent(normalizedProjectId)}/memory/enabled`,
    jsonRequest('PUT', { enabled }),
    options
  );
}

export async function createXScienceMemory(
  input: XScienceCreateMemoryInput,
  options: XScienceMemoryOptions = {}
): Promise<XScienceMemoryRow> {
  const payload = await requestJson('/api/memories', jsonRequest('POST', input), options);
  const row = toMemoryRow(payload);
  if (!row) throw new Error('X-Science memory create response is invalid');
  return row;
}

export async function updateXScienceMemory(
  memoryId: string,
  input: XScienceUpdateMemoryInput,
  options: XScienceMemoryOptions = {}
): Promise<XScienceMemoryRow | null> {
  const payload = await requestJson(
    `/api/memories/${encodeURIComponent(memoryId)}`,
    jsonRequest('PUT', input),
    options
  );
  return payload === undefined ? null : toMemoryRow(payload);
}

export async function deleteXScienceMemory(memoryId: string, options: XScienceMemoryOptions = {}): Promise<void> {
  await requestJson(`/api/memories/${encodeURIComponent(memoryId)}`, { method: 'DELETE' }, options);
}

export async function clearXScienceMemories(options: XScienceMemoryOptions = {}): Promise<void> {
  await requestJson('/api/memories', { method: 'DELETE' }, options);
}

export async function createXScienceMemoryCategory(
  input: XScienceCreateMemoryCategoryInput,
  options: XScienceMemoryOptions = {}
): Promise<XScienceMemoryCategory> {
  const payload = await requestJson(
    '/api/memory/categories',
    categoryRequest(
      'POST',
      {
        name: input.name,
        guidance: input.guidance,
        auto_recall: input.autoRecall ?? true,
      },
      options
    ),
    options
  );
  const category = toMemoryCategory(payload);
  if (!category) throw new Error('X-Science memory category create response is invalid');
  return category;
}

export async function updateXScienceMemoryCategory(
  categoryId: string,
  input: XScienceMemoryCategoryInput,
  options: XScienceMemoryOptions = {}
): Promise<XScienceMemoryCategory | null> {
  const payload = await requestJson(
    `/api/memory/categories/${encodeURIComponent(categoryId)}`,
    categoryRequest(
      'PUT',
      {
        name: input.name,
        guidance: input.guidance,
        auto_recall: input.autoRecall,
      },
      options
    ),
    options
  );
  return payload === undefined ? null : toMemoryCategory(payload);
}

export async function deleteXScienceMemoryCategory(
  categoryId: string,
  deleteFacts: boolean,
  options: XScienceMemoryOptions = {}
): Promise<void> {
  await requestJson(
    `/api/memory/categories/${encodeURIComponent(categoryId)}?delete_facts=${String(deleteFacts)}`,
    { method: 'DELETE' },
    options
  );
}

export const MEMORY_METADATA_TIMEOUT_MS = 30_000;
const MAX_TIMER_MS = 2_147_483_647;
export type MemoryCategoryWriteOutcome = 'rejected' | 'unknown' | 'committed-missing' | 'conflict';
type MemoryRequestStage = 'headers' | 'body' | 'decode';
type MemoryRequestReason = 'deadline' | 'abort' | 'transport' | 'http' | 'decode';

/** Transport facts only: no category fields, response body or operation key in diagnostics. */
export class XScienceMemoryRequestError extends Error {
  readonly reason: MemoryRequestReason;
  readonly stage: MemoryRequestStage;
  readonly status?: number;
  readonly code: string;
  readonly categoryOutcome: MemoryCategoryWriteOutcome;

  constructor(input: {
    reason: MemoryRequestReason;
    stage: MemoryRequestStage;
    path?: string;
    status?: number;
    code?: string;
  }) {
    const path = (input.path ?? '/api/memory').split(/[?#]/, 1)[0];
    super(
      `X-Science memory request ${input.reason === 'http' ? 'failed:' : input.reason + ':'} ${input.status ?? ''} ${path}`
    );
    this.name = 'XScienceMemoryRequestError';
    this.reason = input.reason;
    this.stage = input.stage;
    this.status = input.status;
    this.code = [
      'MEMORY_CATEGORY_WRITE_REJECTED',
      'MEMORY_CATEGORY_OUTCOME_UNKNOWN',
      'MEMORY_CATEGORY_COMMITTED_RESULT_MISSING',
      'MEMORY_CATEGORY_IDEMPOTENCY_CONFLICT',
    ].includes(input.code ?? '')
      ? input.code!
      : '';
    this.categoryOutcome =
      input.reason === 'http' &&
      input.status !== undefined &&
      input.status >= 400 &&
      input.status < 500 &&
      this.code === 'MEMORY_CATEGORY_WRITE_REJECTED'
        ? 'rejected'
        : input.reason === 'http' && input.status === 410 && this.code === 'MEMORY_CATEGORY_COMMITTED_RESULT_MISSING'
          ? 'committed-missing'
          : input.reason === 'http' && input.status === 409 && this.code === 'MEMORY_CATEGORY_IDEMPOTENCY_CONFLICT'
            ? 'conflict'
            : 'unknown';
  }
}

export function memoryCategoryWriteOutcome(error: unknown): MemoryCategoryWriteOutcome {
  return error instanceof XScienceMemoryRequestError ? error.categoryOutcome : 'unknown';
}

function categoryRequest(method: 'POST' | 'PUT', body: unknown, options: XScienceMemoryOptions): RequestInit {
  const init = jsonRequest(method, body);
  if (options.operationId === undefined) return init;
  const key = options.operationId;
  if (typeof key !== 'string' || !/^[!#$%&'*+\-.^_`|~0-9A-Za-z]{1,256}$/.test(key)) {
    throw new RangeError('A valid memory operation identity is required');
  }
  return { ...init, headers: { ...init.headers, 'Idempotency-Key': key } };
}

async function requestJson(path: string, init: RequestInit, options: XScienceMemoryOptions): Promise<unknown> {
  const timeoutMs = options.timeoutMs ?? MEMORY_METADATA_TIMEOUT_MS;
  if (!Number.isInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > MAX_TIMER_MS) {
    throw new RangeError('Memory request timeout must be a positive platform timer integer');
  }
  const controller = new AbortController();
  const caller = options.signal ?? init.signal;
  let stage: MemoryRequestStage = 'headers';
  let stopped: XScienceMemoryRequestError | null = null;
  let response: Response | undefined;
  let rejectStop!: (error: XScienceMemoryRequestError) => void;
  const stop = new Promise<never>((_resolve, reject) => {
    rejectStop = reject;
  });
  const cancelBody = () => {
    try {
      void response?.body?.cancel().catch((): void => undefined);
    } catch {
      /* The fetch signal owns a locked reader. */
    }
  };
  const stopRequest = (reason: 'deadline' | 'abort') => {
    if (stopped) return;
    stopped = new XScienceMemoryRequestError({ reason, stage, path });
    controller.abort(reason === 'deadline' ? 'memory_metadata_deadline' : caller?.reason);
    cancelBody();
    rejectStop(stopped);
  };
  const fromCaller = () => stopRequest('abort');
  const timer = setTimeout(() => stopRequest('deadline'), timeoutMs);
  if (caller?.aborted) fromCaller();
  else caller?.addEventListener('abort', fromCaller, { once: true });
  const requireLive = () => {
    if (stopped) throw stopped;
  };
  const work = Promise.resolve().then(async () => {
    requireLive();
    response = await (options.fetchImpl ?? fetch)(toGatewayUrl(path, options.baseUrl), {
      ...init,
      credentials: 'include',
      headers: { Accept: 'application/json', ...init.headers },
      signal: controller.signal,
    });
    if (stopped) {
      cancelBody();
      throw stopped;
    }
    stage = 'body';
    if (response.status === 204 && response.ok) return undefined;
    const text = await response.text();
    requireLive();
    stage = 'decode';
    if (!response.ok) {
      let payload: RecordValue | null = null;
      try {
        payload = asRecord(text ? JSON.parse(text) : null);
      } catch {
        /* Preserve the HTTP fact without unsafe body retention. */
      }
      throw new XScienceMemoryRequestError({
        reason: 'http',
        stage,
        path,
        status: response.status,
        code: stringValue(payload?.code),
      });
    }
    return text ? JSON.parse(text) : undefined;
  });
  try {
    return await Promise.race([work, stop]);
  } catch (error) {
    if (stopped) throw stopped;
    if (error instanceof XScienceMemoryRequestError) throw error;
    throw new XScienceMemoryRequestError({ reason: stage === 'decode' ? 'decode' : 'transport', stage, path });
  } finally {
    clearTimeout(timer);
    caller?.removeEventListener('abort', fromCaller);
  }
}

function jsonRequest(method: 'POST' | 'PUT', body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  };
}

function toGatewayUrl(path: string, baseUrl = ''): string {
  return `${baseUrl.replace(/\/+$/, '')}${path}`;
}

function toMemoryRow(value: unknown): XScienceMemoryRow | null {
  const record = asRecord(value);
  const id = stringValue(record?.id);
  const body = stringValue(record?.body);
  if (!id || !body) return null;
  return {
    id,
    body,
    userId: nullableStringValue(record?.userId ?? record?.user_id),
    categoryId: nullableStringValue(record?.categoryId ?? record?.category_id),
    subjectProjectId: nullableStringValue(record?.subjectProjectId ?? record?.subject_project_id),
    subjectArtifactId: nullableStringValue(record?.subjectArtifactId ?? record?.subject_artifact_id),
    subjectVersionId: nullableStringValue(record?.subjectVersionId ?? record?.subject_version_id),
    subjectFrameId: nullableStringValue(record?.subjectFrameId ?? record?.subject_frame_id),
    sourceFrameId: nullableStringValue(record?.sourceFrameId ?? record?.source_frame_id),
    origin: stringValue(record?.origin) || 'user',
    evidence: stringValue(record?.evidence) || 'stated',
    createdAt: nullableStringValue(record?.createdAt ?? record?.created_at),
    updatedAt: nullableStringValue(record?.updatedAt ?? record?.updated_at),
    lastSurfacedAt: nullableStringValue(record?.lastSurfacedAt ?? record?.last_surfaced_at),
  };
}

function toMemoryEntity(value: unknown): XScienceMemoryEntity | null {
  const record = asRecord(value);
  const entityKey = stringValue(record?.entity_key ?? record?.entityKey);
  if (!entityKey) return null;
  return {
    entityKey,
    label: stringValue(record?.label) || entityKey,
    projectId: nullableStringValue(record?.project_id ?? record?.projectId),
    rows: arrayValue(record?.rows).map(toMemoryRow).filter(isMemoryRow),
  };
}

function toMemorySession(value: unknown): XScienceMemorySession | null {
  const record = asRecord(value);
  const frameId = stringValue(record?.frame_id ?? record?.frameId);
  const projectId = stringValue(record?.project_id ?? record?.projectId);
  if (!frameId || !projectId) return null;
  return {
    frameId,
    projectId,
    label: stringValue(record?.label) || frameId,
    rowCount: numberValue(record?.row_count ?? record?.rowCount),
    newestAt: nullableStringValue(record?.newest_at ?? record?.newestAt),
  };
}

function toMemoryCategory(value: unknown): XScienceMemoryCategory | null {
  const record = asRecord(value);
  const id = stringValue(record?.id);
  const name = stringValue(record?.name);
  if (!id || !name) return null;
  return {
    id,
    name,
    guidance: stringValue(record?.guidance),
    autoRecall: record?.auto_recall === true || record?.autoRecall === true,
    rowCount: numberValue(record?.row_count ?? record?.rowCount),
  };
}

function asRecord(value: unknown): RecordValue | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordValue) : null;
}

function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function nullableStringValue(value: unknown): string | null {
  const result = stringValue(value);
  return result || null;
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function nullableBooleanValue(value: unknown): boolean | null {
  return typeof value === 'boolean' ? value : null;
}

function isMemoryRow(value: XScienceMemoryRow | null): value is XScienceMemoryRow {
  return value !== null;
}

function isMemoryEntity(value: XScienceMemoryEntity | null): value is XScienceMemoryEntity {
  return value !== null;
}

function isMemorySession(value: XScienceMemorySession | null): value is XScienceMemorySession {
  return value !== null;
}

function isMemoryCategory(value: XScienceMemoryCategory | null): value is XScienceMemoryCategory {
  return value !== null;
}
