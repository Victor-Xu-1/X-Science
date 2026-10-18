/** Immutable wire evidence and mutable reading bindings have distinct types. */
export const MAX_READING_PDFS = 3;
export const MAX_READING_PDF_BYTES = 50 * 1024 * 1024;

export type PdfReadingScope = {
  frame_id: string;
  frame_incarnation_id: string;
  root_frame_id: string;
  root_frame_incarnation_id: string;
  project_id: string;
};
export type PdfReadingSource = { artifact_id: string; version_id: string };
export type PdfReadingBinding = PdfReadingSource & {
  binding_id: string;
  source_kind: 'artifact-version';
  name: string;
  mime_type: 'application/pdf';
  size_bytes: number;
  checksum: string;
  page_count: number;
  linked_at: string;
};
export type PdfReadingContext = {
  schema: 'x-science.pdf-reading-context.v1';
  scope: PdfReadingScope;
  revision: number;
  changed: boolean;
  bindings: PdfReadingBinding[];
};
export type PdfReadingExpected = {
  expected_frame_incarnation_id: string;
  expected_root_frame_id: string;
  expected_root_frame_incarnation_id: string;
  expected_revision: number;
};
export type PdfReadingSelection = PdfReadingExpected & {
  schema: 'x-science.pdf-reading-selection.v1';
  binding_ids: string[];
  active_binding_id?: string;
  page_number?: number;
};
export type PdfReadingSnapshot = Omit<PdfReadingContext, 'schema' | 'changed'> & {
  schema: 'x-science.pdf-reading-snapshot.v1';
  active_binding_id?: string;
  page_number?: number;
};
export type PdfReadingMutation = PdfReadingExpected & {
  schema: 'x-science.pdf-reading-link.v1' | 'x-science.pdf-reading-unlink.v1';
  operation_id: string;
  sources?: PdfReadingSource[];
  binding_id?: string;
};
export type PdfReadingPendingMutation = {
  ownerId: string;
  frameId: string;
  request: PdfReadingMutation;
};
export type PdfReadingPosition = { pageNumber: number; pageCount: number };
export type PdfReadingCandidate = {
  source: PdfReadingSource;
  eligible: boolean;
  reason?: string;
  page_count?: number;
  size_bytes?: number;
  checksum?: string;
};

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const identity = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 512 && value === value.trim();
const revision = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const checksum = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
const invalid = (): never => {
  throw new Error('Invalid PDF reading contract');
};

export function decodePdfReadingScope(value: unknown): PdfReadingScope {
  if (
    !record(value) ||
    !identity(value.frame_id) ||
    !identity(value.frame_incarnation_id) ||
    !identity(value.root_frame_id) ||
    !identity(value.root_frame_incarnation_id) ||
    !identity(value.project_id)
  )
    return invalid();
  return {
    frame_id: value.frame_id,
    frame_incarnation_id: value.frame_incarnation_id,
    root_frame_id: value.root_frame_id,
    root_frame_incarnation_id: value.root_frame_incarnation_id,
    project_id: value.project_id,
  };
}
export function decodePdfReadingSource(value: unknown): PdfReadingSource {
  if (!record(value) || !identity(value.artifact_id) || !identity(value.version_id)) return invalid();
  return { artifact_id: value.artifact_id, version_id: value.version_id };
}
export function decodePdfReadingBinding(value: unknown): PdfReadingBinding {
  const source = decodePdfReadingSource(value);
  if (
    !record(value) ||
    !identity(value.binding_id) ||
    value.source_kind !== 'artifact-version' ||
    typeof value.name !== 'string' ||
    !value.name ||
    value.name.length > 4096 ||
    value.mime_type !== 'application/pdf' ||
    !revision(value.size_bytes) ||
    value.size_bytes < 1 ||
    value.size_bytes > MAX_READING_PDF_BYTES ||
    !checksum(value.checksum) ||
    !revision(value.page_count) ||
    value.page_count < 2 ||
    typeof value.linked_at !== 'string' ||
    !Number.isFinite(Date.parse(value.linked_at))
  )
    return invalid();
  return {
    ...source,
    binding_id: value.binding_id,
    source_kind: 'artifact-version',
    name: value.name,
    mime_type: 'application/pdf',
    size_bytes: value.size_bytes,
    checksum: value.checksum,
    page_count: value.page_count,
    linked_at: value.linked_at,
  };
}
function decodeBindings(value: unknown): PdfReadingBinding[] {
  if (!Array.isArray(value) || value.length > MAX_READING_PDFS) return invalid();
  const bindings = value.map(decodePdfReadingBinding);
  if (
    new Set(bindings.map((item) => item.binding_id)).size !== bindings.length ||
    new Set(bindings.map((item) => item.version_id)).size !== bindings.length
  )
    return invalid();
  return bindings;
}
export function decodePdfReadingContext(value: unknown): PdfReadingContext {
  if (
    !record(value) ||
    value.schema !== 'x-science.pdf-reading-context.v1' ||
    !revision(value.revision) ||
    typeof value.changed !== 'boolean'
  )
    return invalid();
  return {
    schema: value.schema,
    scope: decodePdfReadingScope(value.scope),
    revision: value.revision,
    changed: value.changed,
    bindings: decodeBindings(value.bindings),
  };
}
function decodeExpected(value: Record<string, unknown>): PdfReadingExpected {
  if (
    !identity(value.expected_frame_incarnation_id) ||
    !identity(value.expected_root_frame_id) ||
    !identity(value.expected_root_frame_incarnation_id) ||
    !revision(value.expected_revision)
  )
    return invalid();
  return {
    expected_frame_incarnation_id: value.expected_frame_incarnation_id,
    expected_root_frame_id: value.expected_root_frame_id,
    expected_root_frame_incarnation_id: value.expected_root_frame_incarnation_id,
    expected_revision: value.expected_revision,
  };
}
export function decodePdfReadingSelection(value: unknown): PdfReadingSelection {
  if (
    !record(value) ||
    value.schema !== 'x-science.pdf-reading-selection.v1' ||
    !Array.isArray(value.binding_ids) ||
    value.binding_ids.length > MAX_READING_PDFS ||
    !value.binding_ids.every(identity) ||
    new Set(value.binding_ids).size !== value.binding_ids.length
  )
    return invalid();
  if (
    value.active_binding_id !== undefined &&
    (!identity(value.active_binding_id) || !value.binding_ids.includes(value.active_binding_id))
  )
    return invalid();
  if (
    value.page_number !== undefined &&
    (!revision(value.page_number) || (value.page_number > 0 && !value.active_binding_id))
  )
    return invalid();
  return {
    schema: value.schema,
    ...decodeExpected(value),
    binding_ids: [...value.binding_ids],
    ...(typeof value.active_binding_id === 'string' ? { active_binding_id: value.active_binding_id } : {}),
    ...(typeof value.page_number === 'number' ? { page_number: value.page_number } : {}),
  };
}
export function decodePdfReadingSnapshot(value: unknown): PdfReadingSnapshot {
  if (!record(value) || value.schema !== 'x-science.pdf-reading-snapshot.v1' || !revision(value.revision))
    return invalid();
  const scope = decodePdfReadingScope(value.scope);
  const bindings = decodeBindings(value.bindings);
  const selection = decodePdfReadingSelection({
    schema: 'x-science.pdf-reading-selection.v1',
    ...pdfReadingExpected({ scope, revision: value.revision }),
    binding_ids: bindings.map((item) => item.binding_id),
    active_binding_id: value.active_binding_id,
    page_number: value.page_number,
  });
  const active = bindings.find((item) => item.binding_id === selection.active_binding_id);
  if (active && (selection.page_number ?? 0) > active.page_count) return invalid();
  return {
    schema: value.schema,
    scope,
    revision: value.revision,
    bindings,
    ...(selection.active_binding_id ? { active_binding_id: selection.active_binding_id } : {}),
    ...(selection.page_number ? { page_number: selection.page_number } : {}),
  };
}
export function decodePdfReadingMutation(value: unknown): PdfReadingMutation {
  if (
    !record(value) ||
    typeof value.operation_id !== 'string' ||
    !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value.operation_id)
  )
    return invalid();
  const common = { ...decodeExpected(value), operation_id: value.operation_id };
  if (value.schema === 'x-science.pdf-reading-link.v1') {
    if (
      !Array.isArray(value.sources) ||
      value.sources.length < 1 ||
      value.sources.length > MAX_READING_PDFS ||
      value.binding_id !== undefined
    )
      return invalid();
    const sources = value.sources.map(decodePdfReadingSource);
    if (new Set(sources.map((item) => item.version_id)).size !== sources.length) return invalid();
    return { ...common, schema: value.schema, sources };
  }
  if (value.schema !== 'x-science.pdf-reading-unlink.v1' || !identity(value.binding_id) || value.sources !== undefined)
    return invalid();
  return { ...common, schema: value.schema, binding_id: value.binding_id };
}
export function decodePdfReadingPending(value: unknown): PdfReadingPendingMutation | undefined {
  if (!record(value) || !identity(value.ownerId) || !identity(value.frameId)) return undefined;
  try {
    return { ownerId: value.ownerId, frameId: value.frameId, request: decodePdfReadingMutation(value.request) };
  } catch {
    return undefined;
  }
}
export function pdfReadingExpected(context: Pick<PdfReadingContext, 'scope' | 'revision'>): PdfReadingExpected {
  return {
    expected_frame_incarnation_id: context.scope.frame_incarnation_id,
    expected_root_frame_id: context.scope.root_frame_id,
    expected_root_frame_incarnation_id: context.scope.root_frame_incarnation_id,
    expected_revision: context.revision,
  };
}
export function samePdfReadingScope(left: PdfReadingScope, right: PdfReadingScope): boolean {
  return (
    left.frame_id === right.frame_id &&
    left.frame_incarnation_id === right.frame_incarnation_id &&
    left.root_frame_id === right.root_frame_id &&
    left.root_frame_incarnation_id === right.root_frame_incarnation_id &&
    left.project_id === right.project_id
  );
}

export function decodePdfReadingProjection(value: {
  pdf_reading_context?: unknown;
  pdf_reading_context_unavailable?: unknown;
}): {
  pdf_reading_context?: PdfReadingSnapshot;
  pdf_reading_context_unavailable?: true;
} {
  if (value.pdf_reading_context_unavailable === true) return { pdf_reading_context_unavailable: true };
  if (value.pdf_reading_context === undefined) return {};
  try {
    return { pdf_reading_context: decodePdfReadingSnapshot(value.pdf_reading_context) };
  } catch {
    return { pdf_reading_context_unavailable: true };
  }
}
