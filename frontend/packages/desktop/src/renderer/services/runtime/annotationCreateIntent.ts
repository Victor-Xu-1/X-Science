import type {
  CreateXScienceArtifactAnnotationInput,
  XScienceAnnotationTarget,
  XScienceArtifactAnnotation,
  XScienceAnnotationType,
} from '../xScienceAnnotations';
import { isAnnotationNoteSaveable } from '@/renderer/utils/annotationNote';

export type AnnotationCreateSource = Readonly<{ targetKey: string; checksum: string }>;
export type AnnotationCreateIntent = Readonly<{
  schema: 'x-science.annotation-create.v1';
  ownerId: string;
  operationId: string;
  target: Readonly<XScienceAnnotationTarget>;
  source: AnnotationCreateSource;
  body: Readonly<CreateXScienceArtifactAnnotationInput>;
}>;
const record = (value: unknown): value is Record<string, unknown> =>
  Boolean(value && typeof value === 'object' && !Array.isArray(value));
const identity = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.trim() === value;
const fail = (): never => {
  throw new Error('Invalid original annotation request');
};
const numbers = ['xPercent', 'yPercent', 'startLine', 'startColumn', 'endLine', 'endColumn', 'pageNumber'] as const;
const strings = [
  'selectionText',
  'selectionPrefix',
  'screenshotArtifactId',
  'elementSelector',
  'elementDescriptor',
] as const;
const types = new Set(['point', 'text_selection', 'screenshot', 'html_element']);

export function annotationTargetKey(target: XScienceAnnotationTarget): string {
  return `${target.kind === 'artifact' ? 'av' : 'litv'}:${target.versionId}`;
}
export function annotationTargetIdentity(target: XScienceAnnotationTarget): string {
  return JSON.stringify([target.kind, target.kind === 'artifact' ? target.artifactId : null, target.versionId]);
}
export function annotationIntentStorageKey(ownerId: string, target: XScienceAnnotationTarget): string {
  return `x-science.annotation-create.v1:${encodeURIComponent(ownerId)}:${encodeURIComponent(annotationTargetIdentity(target))}`;
}
export function canonicalAnnotationTarget(value: unknown): Readonly<XScienceAnnotationTarget> {
  if (!record(value) || !identity(value.versionId)) return fail();
  if (value.kind === 'literature' && Object.keys(value).length === 2)
    return Object.freeze({ kind: 'literature', versionId: value.versionId });
  if (value.kind === 'artifact' && identity(value.artifactId) && Object.keys(value).length === 3)
    return Object.freeze({ kind: 'artifact', artifactId: value.artifactId, versionId: value.versionId });
  return fail();
}
/** This is the original public create DTO, not a new wire envelope. */
export function freezeAnnotationCreateBody(value: unknown): Readonly<CreateXScienceArtifactAnnotationInput> {
  if (!record(value) || typeof value.text !== 'string' || !isAnnotationNoteSaveable(value.text)) return fail();
  const type = value.type ?? 'point';
  if (typeof type !== 'string' || !types.has(type)) return fail();
  const allowed = new Set<string>(['type', 'text', ...numbers, ...strings]);
  if (Object.keys(value).some((key) => !allowed.has(key))) return fail();
  const body: CreateXScienceArtifactAnnotationInput = { type: type as XScienceAnnotationType, text: value.text };
  for (const key of numbers) {
    const field = value[key];
    if (field !== undefined) {
      if (field !== null && (typeof field !== 'number' || !Number.isFinite(field))) return fail();
      body[key] = field as number | null;
    }
  }
  for (const key of strings) {
    const field = value[key];
    if (field !== undefined) {
      if (field !== null && typeof field !== 'string') return fail();
      body[key] = field as string | null;
    }
  }
  // Mirror the existing server create DTO before persisting a retryable intent.
  // A structurally rejected new draft must remain editable, never occupy the durable slot.
  for (const coordinate of [body.xPercent, body.yPercent]) {
    if (coordinate != null && (coordinate < 0 || coordinate > 100)) return fail();
  }
  if (type === 'point' && (body.xPercent == null || body.yPercent == null)) return fail();
  if (type === 'text_selection' && !body.selectionText?.trim()) return fail();
  if (type === 'screenshot' && !body.screenshotArtifactId?.trim()) return fail();
  if (type === 'html_element' && !body.elementSelector?.trim()) return fail();
  if (body.pageNumber != null && (body.pageNumber <= 0 || !Number.isInteger(body.pageNumber))) return fail();
  return Object.freeze(body);
}
export function canonicalAnnotationSource(target: XScienceAnnotationTarget, value: unknown): AnnotationCreateSource {
  if (
    !record(value) ||
    Object.keys(value).length !== 2 ||
    typeof value.targetKey !== 'string' ||
    value.targetKey !== annotationTargetKey(target) ||
    typeof value.checksum !== 'string' ||
    !/^[a-f0-9]{64}$/i.test(value.checksum)
  )
    return fail();
  return Object.freeze({ targetKey: value.targetKey, checksum: value.checksum.toLowerCase() });
}
export function decodeAnnotationCreateIntent(
  value: unknown,
  ownerId: string,
  target: XScienceAnnotationTarget
): AnnotationCreateIntent {
  if (
    !record(value) ||
    Object.keys(value).length !== 6 ||
    value.schema !== 'x-science.annotation-create.v1' ||
    value.ownerId !== ownerId ||
    !identity(value.operationId) ||
    !/^[a-zA-Z0-9!#$%&'*+.^_`|~-]{1,256}$/.test(value.operationId)
  )
    return fail();
  const frozenTarget = canonicalAnnotationTarget(value.target);
  if (annotationTargetIdentity(frozenTarget) !== annotationTargetIdentity(target)) return fail();
  return Object.freeze({
    schema: 'x-science.annotation-create.v1',
    ownerId,
    operationId: value.operationId,
    target: frozenTarget,
    source: canonicalAnnotationSource(target, value.source),
    body: freezeAnnotationCreateBody(value.body),
  });
}
export const encodeAnnotationCreateIntent = (value: AnnotationCreateIntent) => JSON.stringify(value);
export const sameAnnotationCreateIntent = (left: AnnotationCreateIntent, right: AnnotationCreateIntent) =>
  encodeAnnotationCreateIntent(left) === encodeAnnotationCreateIntent(right);
export function validateAnnotationCreateAck(intent: AnnotationCreateIntent, value: XScienceArtifactAnnotation): void {
  // A later edit can change text/addressed metadata; creation identity and source cannot change.
  if (
    !value ||
    !identity(value.id) ||
    typeof value.text !== 'string' ||
    !value.text ||
    value.targetKey !== intent.source.targetKey ||
    value.contentChecksum !== intent.source.checksum
  )
    throw new Error('The annotation acknowledgement does not match the original source');
}
