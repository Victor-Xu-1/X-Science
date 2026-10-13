export type ResearchSourceState = 'available' | 'empty' | 'unrecognized';

/** Facts collected by the same bounded source traversal, not a second parser. */
export type ResearchSourceShape = {
  collectionSeen: boolean;
  nonemptyCollection: boolean;
  positiveReturnedClaim: boolean;
};

export function observeResearchCollection(value: unknown, shape: ResearchSourceShape): void {
  if (!Array.isArray(value)) return;
  shape.collectionSeen = true;
  shape.nonemptyCollection ||= value.length > 0;
}

export function observeResearchReturnClaim(record: Record<string, unknown>, shape: ResearchSourceShape): void {
  // The native search contract may explicitly carry zero canonical sources.
  // Generic results/api_total/n_cids_total are not current-page record counts.
  if (record.sources === 0) shape.collectionSeen = true;
  for (const key of [
    'n_records_returned',
    'rows_retrieved',
    'n_retrieved',
    'n_returned',
    'results_returned',
    'returnedResults',
    'retrieved',
  ]) {
    const value = record[key];
    if (typeof value === 'number' && Number.isFinite(value) && value > 0) shape.positiveReturnedClaim = true;
  }
}

export function researchSourceState(shape: ResearchSourceShape, visibleCount: number): ResearchSourceState {
  if (visibleCount > 0) return 'available';
  return shape.collectionSeen && !shape.nonemptyCollection && !shape.positiveReturnedClaim ? 'empty' : 'unrecognized';
}

export function pubchemCompoundIdentity(value: unknown): { cid: string; url: string } | null {
  if (typeof value !== 'string' && typeof value !== 'number') return null;
  const cid = String(value);
  if (!/^[1-9]\d*$/u.test(cid) || !Number.isSafeInteger(Number(cid))) return null;
  return { cid, url: `https://pubchem.ncbi.nlm.nih.gov/compound/${cid}` };
}
