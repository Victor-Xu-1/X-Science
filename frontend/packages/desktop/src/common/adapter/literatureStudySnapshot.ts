import type { LiteratureItem, LiteratureVersion } from './literatureLibrary';

export const STUDY_FIELDS = ['question', 'population', 'methods', 'results', 'limitations'] as const;
export type LiteratureStudySections = Record<(typeof STUDY_FIELDS)[number], string | null>;
export type LiteratureStudySnapshot = {
  source_key: string;
  coverage: 'abstract' | 'full-text' | 'passages';
  attachment_version_id?: string;
  model: string;
  sections: LiteratureStudySections;
};
export type LiteratureStudyView = {
  schema: 'x-science.literature-study.v1';
  source_key: string;
  revision: number;
  available: boolean;
  snapshot?: LiteratureStudySnapshot;
};
export type LiteratureStudyGenerate = {
  operation_id: string;
  language: string;
  expected_source_key: string;
  expected_revision: number;
};
const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const keys = (value: Record<string, unknown>, allowed: string[]) =>
  Object.keys(value).every((key) => allowed.includes(key));
export function literatureStudyVersion(item: LiteratureItem): LiteratureVersion | undefined {
  for (const attachment of [...item.attachments].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))) {
    if (attachment.kind !== 'fullText') continue;
    const latest = [...attachment.versions].sort(
      (a, b) => b.version_number - a.version_number || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
    )[0];
    if (latest?.mime_type === 'application/pdf') return latest;
  }
  return undefined;
}
/** Freshness token only. It never grants access to a paper or immutable PDF. */
export function literatureStudySourceKey(item: LiteratureItem): string {
  const v = literatureStudyVersion(item);
  return JSON.stringify([
    item.id,
    item.metadata_revision,
    !item.deleted,
    v ? [v.attachment_id, v.id, v.checksum, v.size_bytes] : null,
  ]);
}
export class LiteratureStudyScopeError extends Error {}
export function decodeLiteratureStudy(value: unknown, item: LiteratureItem): LiteratureStudyView {
  if (
    !record(value) ||
    !keys(value, ['schema', 'source_key', 'revision', 'available', 'snapshot']) ||
    value.schema !== 'x-science.literature-study.v1' ||
    typeof value.source_key !== 'string' ||
    !Number.isSafeInteger(value.revision) ||
    (value.revision as number) < 0 ||
    typeof value.available !== 'boolean'
  )
    throw new Error('Invalid study overview response');
  if (value.source_key !== literatureStudySourceKey(item)) throw new LiteratureStudyScopeError('Study source changed');
  if (value.snapshot !== undefined) {
    const result = value.snapshot;
    if (
      !record(result) ||
      !keys(result, ['source_key', 'coverage', 'attachment_version_id', 'model', 'sections']) ||
      result.source_key !== value.source_key ||
      typeof result.model !== 'string' ||
      !result.model.trim() ||
      result.model.length > 512 ||
      typeof result.coverage !== 'string' ||
      !['abstract', 'full-text', 'passages'].includes(result.coverage) ||
      !record(result.sections) ||
      Object.keys(result.sections).length !== 5 ||
      !STUDY_FIELDS.every((key) => Object.hasOwn(result.sections as object, key))
    )
      throw new Error('Invalid study overview snapshot');
    for (const field of STUDY_FIELDS) {
      const text = result.sections[field];
      if (text !== null && (typeof text !== 'string' || !text.trim() || Array.from(text).length > 2400))
        throw new Error('Invalid study overview section');
    }
    if (result.coverage === 'abstract') {
      if (result.attachment_version_id !== undefined) throw new Error('Invalid study overview evidence');
    } else {
      const selected = literatureStudyVersion(item);
      if (
        !selected ||
        typeof result.attachment_version_id !== 'string' ||
        !result.attachment_version_id ||
        result.attachment_version_id !== selected.id
      )
        throw new Error('Invalid study overview evidence');
    }
    if ((value.revision as number) < 1) throw new Error('Invalid study overview revision');
  }
  return value as LiteratureStudyView;
}
