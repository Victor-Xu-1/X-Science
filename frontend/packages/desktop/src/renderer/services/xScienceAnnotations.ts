import { invalidateXScienceFrameReads } from './xScienceFrameReads';

export type XScienceAnnotationType = 'point' | 'text_selection' | 'screenshot' | 'html_element';

export interface XScienceArtifactAnnotation {
  id: string;
  artifactId: string | null;
  targetKey: string | null;
  label: string;
  contentChecksum: string | null;
  type: XScienceAnnotationType;
  text: string;
  xPercent: number | null;
  yPercent: number | null;
  startLine: number | null;
  startColumn: number | null;
  endLine: number | null;
  endColumn: number | null;
  selectionText: string | null;
  pageNumber: number | null;
  selectionPrefix: string | null;
  screenshotArtifactId: string | null;
  elementSelector: string | null;
  elementDescriptor: string | null;
  addressedAt: string | null;
  addressedInFrameId: string | null;
  createdAt: string;
}

export interface XScienceArtifactAnnotationCollection {
  targetKey: string;
  currentChecksum: string | null;
  annotations: XScienceArtifactAnnotation[];
}

export type XScienceArtifactEditMode = 'edit' | 'ask';

export interface SuggestXScienceArtifactEditInput {
  selectedText: string;
  annotationText: string;
  currentIteration?: string;
  mode: XScienceArtifactEditMode;
}

export interface ApplyXScienceArtifactEditInput {
  selectedText: string;
  replacementText: string;
  contextBefore?: string;
  contextAfter?: string;
}

export interface XScienceAppliedArtifactEdit {
  versionId: string;
  versionNumber: number;
  artifactId: string;
  parentVersionId: string | null;
  carriedAnnotations: XScienceArtifactAnnotation[];
}

export type CreateXScienceArtifactAnnotationInput = {
  text: string;
  type?: XScienceAnnotationType;
  xPercent?: number | null;
  yPercent?: number | null;
  startLine?: number | null;
  startColumn?: number | null;
  endLine?: number | null;
  endColumn?: number | null;
  selectionText?: string | null;
  pageNumber?: number | null;
  selectionPrefix?: string | null;
  screenshotArtifactId?: string | null;
  elementSelector?: string | null;
  elementDescriptor?: string | null;
};

export type XScienceTranscriptAnnotationSource = 'assistant' | 'tool_input' | 'tool_result';
export type XScienceTranscriptAnnotationKind = 'annotation' | 'bookmark';

export interface XScienceTranscriptAnnotation {
  id: string;
  rootFrameId: string;
  messageUuid: string | null;
  messageIndex: number;
  blockIndex: number;
  source: XScienceTranscriptAnnotationSource;
  toolName: string | null;
  anchorText: string;
  startOffset: number | null;
  endOffset: number | null;
  kind: XScienceTranscriptAnnotationKind;
  origin: 'user' | 'agent';
  readAt: string | null;
  note: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateXScienceTranscriptAnnotationInput {
  messageUuid?: string | null;
  messageIndex: number;
  blockIndex?: number;
  source: XScienceTranscriptAnnotationSource;
  toolName?: string | null;
  anchorText: string;
  startOffset?: number | null;
  endOffset?: number | null;
  kind: XScienceTranscriptAnnotationKind;
  note?: string;
}

export type XScienceVerificationVerdict = 'pass' | 'warn' | 'fail' | 'inconclusive';
export type XScienceVerificationStatus = 'open' | 'resolved' | 'unaddressed';

export interface XScienceVerificationCheck {
  id: string;
  rootFrameId: string;
  artifactVersionId: string | null;
  claimId: string | null;
  claim: string | null;
  verdict: XScienceVerificationVerdict;
  severity: string | null;
  evidence: string | null;
  rebuttal: string | null;
  reviewerIndex: number | null;
  reviewerModel: string | null;
  reviewerFrameId: string | null;
  sourceRef: Record<string, unknown> | null;
  status: XScienceVerificationStatus;
  reflagCount: number | null;
  createdAt: string;
}

export interface XScienceFrameVerification {
  checks: XScienceVerificationCheck[];
  claims: unknown[];
  running: unknown[];
}

type FetchLike = typeof fetch;

export const TRANSCRIPT_ANNOTATION_SETTLED_REUSE_MS = 500;

type TranscriptAnnotationRequestEntry = {
  frameId: string;
  promise: Promise<XScienceTranscriptAnnotation[]>;
  cleanupTimer?: ReturnType<typeof setTimeout>;
};

export type TranscriptAnnotationReadOptions = {
  ownerId: string;
  fetchImpl?: FetchLike;
};

const transcriptAnnotationRequests = new WeakMap<FetchLike, Map<string, TranscriptAnnotationRequestEntry>>();
const MAX_TRANSCRIPT_ANNOTATION_REQUESTS = 64;

function transcriptAnnotationRequestMap(fetchImpl: FetchLike): Map<string, TranscriptAnnotationRequestEntry> {
  let requests = transcriptAnnotationRequests.get(fetchImpl);
  if (!requests) {
    requests = new Map();
    transcriptAnnotationRequests.set(fetchImpl, requests);
  }
  return requests;
}

function removeTranscriptAnnotationRequest(
  requests: Map<string, TranscriptAnnotationRequestEntry>,
  key: string,
  expected?: TranscriptAnnotationRequestEntry
): void {
  const current = requests.get(key);
  if (!current || (expected && current !== expected)) return;
  if (current.cleanupTimer) clearTimeout(current.cleanupTimer);
  requests.delete(key);
}

function invalidateTranscriptAnnotationReads(frameId: string, fetchImpl: FetchLike): void {
  const requests = transcriptAnnotationRequests.get(fetchImpl);
  if (!requests) return;
  for (const [key, entry] of requests) {
    if (entry.frameId === frameId) removeTranscriptAnnotationRequest(requests, key, entry);
  }
}

export async function loadXScienceArtifactAnnotations(
  artifactId: string,
  versionId: string,
  fetchImpl: FetchLike = fetch
): Promise<XScienceArtifactAnnotationCollection> {
  const payload = recordValue(
    await requestJson(
      `/api/artifacts/${encodeURIComponent(artifactId)}/versions/${encodeURIComponent(versionId)}/annotations`,
      {},
      fetchImpl
    )
  );
  return {
    targetKey: stringValue(payload?.target_key),
    currentChecksum: nullableString(payload?.current_checksum),
    annotations: arrayValue(payload?.annotations).map(toArtifactAnnotation).filter(isArtifactAnnotation),
  };
}

export async function createXScienceArtifactAnnotation(
  artifactId: string,
  versionId: string,
  input: CreateXScienceArtifactAnnotationInput,
  fetchImpl: FetchLike = fetch
): Promise<XScienceArtifactAnnotation> {
  const payload = await requestJson(
    `/api/artifacts/${encodeURIComponent(artifactId)}/versions/${encodeURIComponent(versionId)}/annotations`,
    jsonRequest('POST', toArtifactAnnotationBody(input)),
    fetchImpl
  );
  const annotation = toArtifactAnnotation(payload);
  if (!annotation) throw new Error('X-Science artifact annotation response is invalid');
  return annotation;
}

export async function updateXScienceArtifactAnnotation(
  annotationId: string,
  patch: { text: string },
  fetchImpl: FetchLike = fetch
): Promise<XScienceArtifactAnnotation> {
  const payload = await requestJson(
    `/api/annotations/${encodeURIComponent(annotationId)}`,
    jsonRequest('PATCH', patch),
    fetchImpl
  );
  const annotation = toArtifactAnnotation(payload);
  if (!annotation) throw new Error('X-Science artifact annotation response is invalid');
  return annotation;
}

export async function deleteXScienceArtifactAnnotation(
  annotationId: string,
  fetchImpl: FetchLike = fetch
): Promise<void> {
  await requestJson(`/api/annotations/${encodeURIComponent(annotationId)}`, { method: 'DELETE' }, fetchImpl);
}

export async function suggestXScienceArtifactEdit(
  artifactId: string,
  versionId: string,
  input: SuggestXScienceArtifactEditInput,
  fetchImpl: FetchLike = fetch
): Promise<string> {
  const payload = recordValue(
    await requestJson(
      `/api/artifacts/${encodeURIComponent(artifactId)}/versions/${encodeURIComponent(versionId)}/suggest-edits`,
      jsonRequest('POST', {
        selected_text: input.selectedText,
        annotation_text: input.annotationText,
        ...(input.currentIteration ? { current_iteration: input.currentIteration } : {}),
        mode: input.mode,
      }),
      fetchImpl
    )
  );
  const suggestion = stringValue(payload?.suggestion);
  if (!suggestion) throw new Error('X-Science artifact edit suggestion response is invalid');
  return suggestion;
}

export async function applyXScienceArtifactEdit(
  artifactId: string,
  versionId: string,
  input: ApplyXScienceArtifactEditInput,
  fetchImpl: FetchLike = fetch
): Promise<XScienceAppliedArtifactEdit> {
  const payload = recordValue(
    await requestJson(
      `/api/artifacts/${encodeURIComponent(artifactId)}/versions/${encodeURIComponent(versionId)}/apply-edit`,
      jsonRequest('POST', {
        selected_text: input.selectedText,
        replacement_text: input.replacementText,
        ...(input.contextBefore ? { context_before: input.contextBefore } : {}),
        ...(input.contextAfter ? { context_after: input.contextAfter } : {}),
      }),
      fetchImpl
    )
  );
  const applied = {
    versionId: stringValue(payload?.version_id),
    versionNumber: numberValue(payload?.version_number),
    artifactId: stringValue(payload?.artifact_id),
    parentVersionId: nullableString(payload?.parent_version_id),
    carriedAnnotations: arrayValue(payload?.carried_annotations).map(toArtifactAnnotation).filter(isArtifactAnnotation),
  };
  if (!applied.versionId || !applied.artifactId || applied.versionNumber < 1) {
    throw new Error('X-Science applied artifact edit response is invalid');
  }
  return applied;
}

export function loadXScienceTranscriptAnnotations(
  frameId: string,
  options: TranscriptAnnotationReadOptions
): Promise<XScienceTranscriptAnnotation[]> {
  const fetchImpl = options.fetchImpl ?? fetch;
  const key = frameId.trim();
  const requests = transcriptAnnotationRequestMap(fetchImpl);
  const requestKey = JSON.stringify([options.ownerId.trim(), key]);
  const existing = requests.get(requestKey);
  if (existing) return existing.promise;

  while (requests.size >= MAX_TRANSCRIPT_ANNOTATION_REQUESTS) {
    const oldest = requests.keys().next().value;
    if (oldest === undefined) break;
    removeTranscriptAnnotationRequest(requests, oldest);
  }

  let entry!: TranscriptAnnotationRequestEntry;
  const promise = requestJson(`/api/frames/${encodeURIComponent(key)}/transcript-annotations`, {}, fetchImpl).then(
    (payload) => {
      if (options.ownerId.trim()) {
        entry.cleanupTimer = setTimeout(
          () => removeTranscriptAnnotationRequest(requests, requestKey, entry),
          TRANSCRIPT_ANNOTATION_SETTLED_REUSE_MS
        );
      } else {
        removeTranscriptAnnotationRequest(requests, requestKey, entry);
      }
      return arrayValue(payload).map(toTranscriptAnnotation).filter(isTranscriptAnnotation);
    },
    (error) => {
      removeTranscriptAnnotationRequest(requests, requestKey, entry);
      throw error;
    }
  );
  entry = { frameId: key, promise };
  requests.set(requestKey, entry);
  return promise;
}

export async function createXScienceTranscriptAnnotation(
  frameId: string,
  input: CreateXScienceTranscriptAnnotationInput,
  fetchImpl: FetchLike = fetch
): Promise<XScienceTranscriptAnnotation> {
  const payload = await requestJson(
    `/api/frames/${encodeURIComponent(frameId)}/transcript-annotations`,
    jsonRequest('POST', toTranscriptAnnotationBody(input)),
    fetchImpl
  );
  const annotation = toTranscriptAnnotation(payload);
  if (!annotation) throw new Error('X-Science transcript annotation response is invalid');
  invalidateTranscriptAnnotationReads(frameId, fetchImpl);
  return annotation;
}

export async function updateXScienceTranscriptAnnotation(
  frameId: string,
  annotationId: string,
  patch: { note?: string; read?: boolean },
  fetchImpl: FetchLike = fetch
): Promise<XScienceTranscriptAnnotation> {
  const payload = await requestJson(
    `/api/frames/${encodeURIComponent(frameId)}/transcript-annotations/${encodeURIComponent(annotationId)}`,
    jsonRequest('PATCH', patch),
    fetchImpl
  );
  const annotation = toTranscriptAnnotation(payload);
  if (!annotation) throw new Error('X-Science transcript annotation response is invalid');
  invalidateTranscriptAnnotationReads(frameId, fetchImpl);
  return annotation;
}

export async function deleteXScienceTranscriptAnnotation(
  frameId: string,
  annotationId: string,
  fetchImpl: FetchLike = fetch
): Promise<void> {
  await requestJson(
    `/api/frames/${encodeURIComponent(frameId)}/transcript-annotations/${encodeURIComponent(annotationId)}`,
    { method: 'DELETE' },
    fetchImpl
  );
  invalidateTranscriptAnnotationReads(frameId, fetchImpl);
}

export async function drainXScienceTranscriptAnnotations(
  frameId: string,
  ids: string[],
  fetchImpl: FetchLike = fetch
): Promise<{ deleted: number }> {
  const payload = recordValue(
    await requestJson(
      `/api/frames/${encodeURIComponent(frameId)}/transcript-annotations/drain`,
      jsonRequest('POST', { ids }),
      fetchImpl
    )
  );
  invalidateTranscriptAnnotationReads(frameId, fetchImpl);
  return { deleted: numberValue(payload?.deleted) };
}

export async function loadXScienceArtifactVerification(
  versionId: string,
  fetchImpl: FetchLike = fetch
): Promise<XScienceVerificationCheck[]> {
  const payload = recordValue(
    await requestJson(`/api/artifacts/versions/${encodeURIComponent(versionId)}/verification`, {}, fetchImpl)
  );
  return arrayValue(payload?.checks).map(toVerificationCheck).filter(isVerificationCheck);
}

export async function loadXScienceFrameVerification(
  frameId: string,
  status?: XScienceVerificationStatus,
  fetchImpl: FetchLike = fetch
): Promise<XScienceFrameVerification> {
  const query = status ? `?status=${encodeURIComponent(status)}` : '';
  const payload = recordValue(
    await requestJson(`/api/frames/${encodeURIComponent(frameId)}/verification${query}`, {}, fetchImpl)
  );
  return {
    checks: arrayValue(payload?.checks).map(toVerificationCheck).filter(isVerificationCheck),
    claims: arrayValue(payload?.claims),
    running: arrayValue(payload?.running),
  };
}

export async function requestXScienceFrameAudit(
  frameId: string,
  fetchImpl: FetchLike = fetch
): Promise<Record<string, unknown>> {
  const payload = await requestJson(
    `/api/frames/${encodeURIComponent(frameId)}/audit`,
    jsonRequest('POST', {}),
    fetchImpl
  );
  invalidateXScienceFrameReads(frameId);
  return recordValue(payload) ?? {};
}

function toArtifactAnnotationBody(input: CreateXScienceArtifactAnnotationInput): Record<string, unknown> {
  return {
    text: input.text,
    type: input.type ?? 'point',
    x_percent: input.xPercent ?? null,
    y_percent: input.yPercent ?? null,
    start_line: input.startLine ?? null,
    start_col: input.startColumn ?? null,
    end_line: input.endLine ?? null,
    end_col: input.endColumn ?? null,
    selection_text: input.selectionText ?? null,
    page_number: input.pageNumber ?? null,
    selection_prefix: input.selectionPrefix ?? null,
    screenshot_artifact_id: input.screenshotArtifactId ?? null,
    element_selector: input.elementSelector ?? null,
    element_descriptor: input.elementDescriptor ?? null,
  };
}

function toTranscriptAnnotationBody(input: CreateXScienceTranscriptAnnotationInput): Record<string, unknown> {
  return {
    message_uuid: input.messageUuid ?? null,
    message_index: input.messageIndex,
    block_index: input.blockIndex ?? 0,
    source: input.source,
    tool_name: input.toolName ?? null,
    anchor_text: input.anchorText,
    start_offset: input.startOffset ?? null,
    end_offset: input.endOffset ?? null,
    kind: input.kind,
    note: input.note ?? '',
  };
}

function toArtifactAnnotation(value: unknown): XScienceArtifactAnnotation | null {
  const row = recordValue(value);
  const id = stringValue(row?.id);
  const text = stringValue(row?.text);
  if (!id || !text) return null;
  const type = stringValue(row?.type);
  return {
    id,
    artifactId: nullableString(row?.artifact_id),
    targetKey: nullableString(row?.target_key),
    label: stringValue(row?.label),
    contentChecksum: nullableString(row?.content_checksum),
    type: isAnnotationType(type) ? type : 'point',
    text,
    xPercent: nullableNumber(row?.x_percent),
    yPercent: nullableNumber(row?.y_percent),
    startLine: nullableNumber(row?.start_line),
    startColumn: nullableNumber(row?.start_col),
    endLine: nullableNumber(row?.end_line),
    endColumn: nullableNumber(row?.end_col),
    selectionText: nullableString(row?.selection_text),
    pageNumber: nullableNumber(row?.page_number),
    selectionPrefix: nullableString(row?.selection_prefix),
    screenshotArtifactId: nullableString(row?.screenshot_artifact_id),
    elementSelector: nullableString(row?.element_selector),
    elementDescriptor: nullableString(row?.element_descriptor),
    addressedAt: nullableString(row?.addressed_at),
    addressedInFrameId: nullableString(row?.addressed_in_frame_id),
    createdAt: stringValue(row?.created_at),
  };
}

function toTranscriptAnnotation(value: unknown): XScienceTranscriptAnnotation | null {
  const row = recordValue(value);
  const id = stringValue(row?.id);
  const source = stringValue(row?.source);
  const kind = stringValue(row?.kind);
  if (!id || !isTranscriptSource(source) || !isTranscriptKind(kind)) return null;
  return {
    id,
    rootFrameId: stringValue(row?.root_frame_id),
    messageUuid: nullableString(row?.message_uuid),
    messageIndex: numberValue(row?.message_index),
    blockIndex: numberValue(row?.block_index),
    source,
    toolName: nullableString(row?.tool_name),
    anchorText: stringValue(row?.anchor_text),
    startOffset: nullableNumber(row?.start_offset),
    endOffset: nullableNumber(row?.end_offset),
    kind,
    origin: stringValue(row?.origin) === 'agent' ? 'agent' : 'user',
    readAt: nullableString(row?.read_at),
    note: stringValue(row?.note),
    createdAt: stringValue(row?.created_at),
    updatedAt: stringValue(row?.updated_at),
  };
}

function toVerificationCheck(value: unknown): XScienceVerificationCheck | null {
  const row = recordValue(value);
  const id = stringValue(row?.id);
  const verdict = stringValue(row?.verdict);
  const status = stringValue(row?.status);
  if (!id || !isVerificationVerdict(verdict) || !isVerificationStatus(status)) return null;
  return {
    id,
    rootFrameId: stringValue(row?.root_frame_id),
    artifactVersionId: nullableString(row?.artifact_version_id),
    claimId: nullableString(row?.claim_id),
    claim: nullableString(row?.claim),
    verdict,
    severity: nullableString(row?.severity),
    evidence: nullableString(row?.evidence),
    rebuttal: nullableString(row?.rebuttal),
    reviewerIndex: nullableNumber(row?.reviewer_idx),
    reviewerModel: nullableString(row?.reviewer_model),
    reviewerFrameId: nullableString(row?.reviewer_frame_id),
    sourceRef: recordValue(row?.source_ref),
    status,
    reflagCount: nullableNumber(row?.reflag_count),
    createdAt: stringValue(row?.created_at),
  };
}

async function requestJson(path: string, init: RequestInit, fetchImpl: FetchLike): Promise<unknown> {
  const response = await fetchImpl(path, { credentials: 'include', ...init });
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    throw new Error(`X-Science annotation request failed with ${response.status}${detail ? `: ${detail}` : ''}`);
  }
  if (response.status === 204) return null;
  return response.json();
}

function jsonRequest(method: string, body: unknown): RequestInit {
  return { method, headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) };
}

function isAnnotationType(value: string): value is XScienceAnnotationType {
  return ['point', 'text_selection', 'screenshot', 'html_element'].includes(value);
}

function isTranscriptSource(value: string): value is XScienceTranscriptAnnotationSource {
  return ['assistant', 'tool_input', 'tool_result'].includes(value);
}

function isTranscriptKind(value: string): value is XScienceTranscriptAnnotationKind {
  return value === 'annotation' || value === 'bookmark';
}

function isVerificationVerdict(value: string): value is XScienceVerificationVerdict {
  return ['pass', 'warn', 'fail', 'inconclusive'].includes(value);
}

function isVerificationStatus(value: string): value is XScienceVerificationStatus {
  return ['open', 'resolved', 'unaddressed'].includes(value);
}

function isArtifactAnnotation(value: XScienceArtifactAnnotation | null): value is XScienceArtifactAnnotation {
  return value !== null;
}

function isTranscriptAnnotation(
  value: XScienceTranscriptAnnotation | null
): value is XScienceTranscriptAnnotation {
  return value !== null;
}

function isVerificationCheck(value: XScienceVerificationCheck | null): value is XScienceVerificationCheck {
  return value !== null;
}

function recordValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function nullableString(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function nullableNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}
