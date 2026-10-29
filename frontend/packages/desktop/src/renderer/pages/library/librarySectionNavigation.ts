type LibraryDestination =
  | { kind: 'collection'; id: string }
  | { kind: 'project'; id: string }
  | { kind: 'trash' }
  | { kind: 'inbox' };

/** URL projection only; Page remains the sole navigation/read owner. */
export function librarySectionParams(params: URLSearchParams, destination: LibraryDestination): URLSearchParams {
  const next = new URLSearchParams(params);
  next.delete('lifecycle');
  if (destination.kind === 'inbox') {
    next.set('inbox', '1');
    return next;
  }
  for (const key of ['inbox', 'cursor', 'item']) next.delete(key);
  if (destination.kind === 'trash') {
    next.delete('project');
    next.delete('collection');
    next.set('lifecycle', 'deleted');
  } else {
    next.delete(destination.kind === 'project' ? 'collection' : 'project');
    if (destination.id) next.set(destination.kind, destination.id);
    else next.delete(destination.kind);
  }
  return next;
}
