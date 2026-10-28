import { literatureValueIdentity } from './literatureLibrary';

export type LiteratureCandidate = {
  id: string;
  title: string;
  metadata: Record<string, unknown>;
  provider: string;
  external_id: string;
  state: 'pending' | 'dismissed' | 'accepted';
  item_id?: string;
};
export type LiteratureCandidateCommand =
  | {
      kind: 'candidate-stage';
      operation_id: string;
      provider: string;
      external_id: string;
      title: string;
      metadata: Record<string, unknown>;
      source?: { item_id: string; metadata_revision: number; direction: 'references' | 'citations' };
    }
  | { kind: 'candidate-accept' | 'candidate-dismiss'; operation_id: string; candidate_id: string };
export type LiteratureCandidateReceipt = {
  schema: 'x-science.literature-candidate-receipt.v1';
  operation_id: string;
  action: 'stage' | 'accept' | 'dismiss';
  candidate: LiteratureCandidate;
};
const record = (v: unknown): v is Record<string, unknown> => Boolean(v && typeof v === 'object' && !Array.isArray(v));
const text = (v: unknown, max = 4096): v is string =>
  typeof v === 'string' && Boolean(v) && new TextEncoder().encode(v).length <= max && v.trim() === v;
const uuid = (v: unknown): v is string =>
  typeof v === 'string' && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v);
const invalid = (): never => {
  throw new Error('Invalid Literature candidate contract');
};
const metadata = (v: unknown): Record<string, unknown> => {
  if (!record(v)) return invalid();
  const encoded = literatureValueIdentity(v);
  // Go encoding/json escapes these characters by default before enforcing the byte limit.
  const serverEncoded = encoded.replace(
    /[<>&\u2028\u2029]/g,
    (char) => '\\u' + char.charCodeAt(0).toString(16).padStart(4, '0')
  );
  if (new TextEncoder().encode(serverEncoded).length > 16384) return invalid();
  return JSON.parse(encoded) as Record<string, unknown>;
};
export function decodeLiteratureCandidate(value: unknown): LiteratureCandidate {
  if (
    !record(value) ||
    !text(value.id, 512) ||
    !text(value.title) ||
    !text(value.provider, 64) ||
    !text(value.external_id, 512) ||
    !['pending', 'dismissed', 'accepted'].includes(String(value.state)) ||
    (value.item_id !== undefined && !text(value.item_id, 512)) ||
    (value.state === 'accepted' && !text(value.item_id, 512)) ||
    (value.state !== 'accepted' && value.item_id !== undefined)
  )
    return invalid();
  return {
    id: value.id,
    title: value.title,
    metadata: metadata(value.metadata),
    provider: value.provider,
    external_id: value.external_id,
    state: value.state as LiteratureCandidate['state'],
    ...(value.item_id ? { item_id: value.item_id as string } : {}),
  };
}
export function decodeLiteratureCandidateCommand(value: unknown): LiteratureCandidateCommand {
  if (!record(value) || !uuid(value.operation_id)) return invalid();
  if (value.kind === 'candidate-stage') {
    if (
      ![
        'external_id,kind,metadata,operation_id,provider,title',
        'external_id,kind,metadata,operation_id,provider,source,title',
      ].includes(Object.keys(value).sort().join(',')) ||
      (value.source !== undefined &&
        (!record(value.source) ||
          Object.keys(value.source).sort().join(',') !== 'direction,item_id,metadata_revision' ||
          !text(value.source.item_id, 512) ||
          !Number.isSafeInteger(value.source.metadata_revision) ||
          Number(value.source.metadata_revision) < 1 ||
          !['references', 'citations'].includes(String(value.source.direction)))) ||
      !text(value.provider, 64) ||
      !text(value.external_id, 512) ||
      !text(value.title)
    )
      return invalid();
    return {
      kind: value.kind,
      operation_id: value.operation_id,
      provider: value.provider,
      external_id: value.external_id,
      title: value.title,
      metadata: metadata(value.metadata),
      ...(value.source
        ? {
            source: {
              item_id: (value.source as Record<string, unknown>).item_id as string,
              metadata_revision: (value.source as Record<string, unknown>).metadata_revision as number,
              direction: (value.source as Record<string, unknown>).direction as 'references' | 'citations',
            },
          }
        : {}),
    };
  }
  if (
    (value.kind !== 'candidate-accept' && value.kind !== 'candidate-dismiss') ||
    Object.keys(value).sort().join(',') !== 'candidate_id,kind,operation_id' ||
    !text(value.candidate_id, 512)
  )
    return invalid();
  return { kind: value.kind, operation_id: value.operation_id, candidate_id: value.candidate_id };
}
export function decodeLiteratureCandidates(
  value: unknown,
  state: 'pending' | 'dismissed'
): { candidates: LiteratureCandidate[]; nextCursor?: string } {
  if (
    !record(value) ||
    value.schema !== 'x-science.literature-candidates.v1' ||
    !Array.isArray(value.candidates) ||
    value.candidates.length > 100 ||
    (value.next_cursor !== undefined && !text(value.next_cursor, 512))
  )
    return invalid();
  const candidates = value.candidates.map(decodeLiteratureCandidate);
  if (
    candidates.some((candidate) => candidate.state !== state) ||
    new Set(candidates.map((candidate) => candidate.id)).size !== candidates.length
  )
    return invalid();
  return { candidates, ...(value.next_cursor ? { nextCursor: value.next_cursor as string } : {}) };
}
export function decodeLiteratureCandidateReceipt(
  value: unknown,
  command: LiteratureCandidateCommand
): LiteratureCandidateReceipt {
  const action = command.kind.slice('candidate-'.length);
  if (
    !record(value) ||
    value.schema !== 'x-science.literature-candidate-receipt.v1' ||
    value.operation_id !== command.operation_id ||
    value.action !== action
  )
    return invalid();
  const candidate = decodeLiteratureCandidate(value.candidate);
  if (command.kind !== 'candidate-stage' && candidate.id !== command.candidate_id) return invalid();
  if (
    (action === 'accept' && candidate.state !== 'accepted') ||
    (action === 'dismiss' && candidate.state !== 'dismissed')
  )
    return invalid();
  return {
    schema: 'x-science.literature-candidate-receipt.v1',
    operation_id: command.operation_id,
    action: action as LiteratureCandidateReceipt['action'],
    candidate,
  };
}
