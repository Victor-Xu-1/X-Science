export type LiteratureDirection = 'references' | 'citations';
export type RelatedIssue =
  | 'no-match'
  | 'reference-changed'
  | 'expired'
  | 'rate-limited'
  | 'authentication'
  | 'timeout'
  | 'unavailable'
  | 'response-invalid';
export type RelatedRecord = {
  paper_id: string;
  title: string;
  abstract: string;
  authors: string[];
  year: number | null;
  issued_text: string;
  container_title: string;
  item_type: string;
  url: string;
  identifiers: Array<{ scheme: 'doi' | 'pmid' | 'pmcid' | 'arxiv'; value: string }>;
  volume: string;
  pages: string;
};
export type RelatedRequest = {
  item_id: string;
  expected_metadata_revision: number;
  direction: LiteratureDirection;
  resolution_token?: string;
  offset: number;
};
export type RelatedResult = {
  schema: 'x-science.literature-related.v1';
  item_id: string;
  metadata_revision: number;
  direction: LiteratureDirection;
  state: 'ready' | 'match-required' | 'unavailable';
  resolution_token?: string;
  seed?: RelatedRecord;
  records: RelatedRecord[];
  next_offset?: number;
  issue?: RelatedIssue;
  retry_at?: number;
  omitted?: number;
};
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length <= max;
const integer = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const token = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value);
const invalid = (): never => {
  throw new Error('Invalid related literature response');
};
const issues = new Set<RelatedIssue>([
  'no-match',
  'reference-changed',
  'expired',
  'rate-limited',
  'authentication',
  'timeout',
  'unavailable',
  'response-invalid',
]);

function decodeRecord(value: unknown): RelatedRecord {
  if (
    !object(value) ||
    typeof value.paper_id !== 'string' ||
    !/^[a-f0-9]{40}$/i.test(value.paper_id) ||
    !text(value.title, 10000) ||
    !value.title.trim() ||
    !text(value.abstract, 100000) ||
    !Array.isArray(value.authors) ||
    value.authors.length > 2000 ||
    !value.authors.every((author) => text(author, 1000) && author.trim()) ||
    !(value.year === null || (integer(value.year) && value.year <= 9999)) ||
    !text(value.issued_text, 100000) ||
    !text(value.container_title, 100000) ||
    !text(value.volume, 100000) ||
    !text(value.pages, 100000) ||
    !['review', 'conferencePaper', 'journalArticle', 'document'].includes(String(value.item_type)) ||
    value.url !== `https://www.semanticscholar.org/paper/${value.paper_id}` ||
    !Array.isArray(value.identifiers) ||
    value.identifiers.length > 4
  )
    return invalid();
  const identifiers = value.identifiers.map((entry) => {
    if (
      !object(entry) ||
      !['doi', 'pmid', 'pmcid', 'arxiv'].includes(String(entry.scheme)) ||
      !text(entry.value, 4096) ||
      !entry.value.trim()
    )
      return invalid();
    return { scheme: entry.scheme as RelatedRecord['identifiers'][number]['scheme'], value: entry.value };
  });
  if (new Set(identifiers.map((entry) => entry.scheme)).size !== identifiers.length) return invalid();
  return {
    paper_id: value.paper_id,
    title: value.title,
    abstract: value.abstract,
    authors: [...value.authors] as string[],
    year: value.year as number | null,
    issued_text: value.issued_text,
    container_title: value.container_title,
    item_type: value.item_type as string,
    url: value.url as string,
    identifiers,
    volume: value.volume,
    pages: value.pages,
  };
}

export function decodeRelatedResult(value: unknown, request: RelatedRequest): RelatedResult {
  if (
    !object(value) ||
    value.schema !== 'x-science.literature-related.v1' ||
    value.item_id !== request.item_id ||
    value.metadata_revision !== request.expected_metadata_revision ||
    value.direction !== request.direction ||
    !['ready', 'match-required', 'unavailable'].includes(String(value.state)) ||
    !Array.isArray(value.records) ||
    value.records.length > 20 ||
    (value.resolution_token !== undefined && !token(value.resolution_token)) ||
    (value.next_offset !== undefined && (!integer(value.next_offset) || value.next_offset <= request.offset)) ||
    (value.retry_at !== undefined && !integer(value.retry_at)) ||
    (value.omitted !== undefined && (!integer(value.omitted) || value.omitted > 20)) ||
    (value.issue !== undefined && !issues.has(value.issue as RelatedIssue))
  )
    return invalid();
  if (value.state === 'unavailable') {
    if (!value.issue || value.records.length || value.next_offset !== undefined) return invalid();
  } else if (!value.resolution_token || !value.seed || value.issue !== undefined) return invalid();
  if (
    value.state === 'match-required' &&
    (value.records.length || value.next_offset !== undefined || request.offset !== 0)
  )
    return invalid();
  const records = value.records.map(decodeRecord);
  if (new Set(records.map((record) => record.paper_id)).size !== records.length) return invalid();
  return {
    schema: value.schema,
    item_id: request.item_id,
    metadata_revision: request.expected_metadata_revision,
    direction: request.direction,
    state: value.state as RelatedResult['state'],
    records,
    ...(value.resolution_token ? { resolution_token: value.resolution_token as string } : {}),
    ...(value.seed ? { seed: decodeRecord(value.seed) } : {}),
    ...(value.next_offset !== undefined ? { next_offset: value.next_offset as number } : {}),
    ...(value.issue ? { issue: value.issue as RelatedIssue } : {}),
    ...(value.retry_at !== undefined ? { retry_at: value.retry_at as number } : {}),
    ...(value.omitted !== undefined ? { omitted: value.omitted as number } : {}),
  };
}
