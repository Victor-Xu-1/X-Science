import { useEffect } from 'react';
import type { SetURLSearchParams } from 'react-router';
import type { LiteratureLifecycle } from '@/common/adapter/literatureLifecycle';
import { literatureReadingStatuses, type LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { librarySectionParams } from './librarySectionNavigation';

/** URL projection only. Receives the Page's original router owner and never starts a read or write. */
export function useLibraryNavigation(params: URLSearchParams, setParams: SetURLSearchParams) {
  const query = params.get('q') ?? '';
  const cursor = params.get('cursor') ?? '';
  const lifecycle: LiteratureLifecycle = params.get('lifecycle') === 'deleted' ? 'deleted' : 'active';
  const trash = lifecycle === 'deleted';
  const invalidLifecycle =
    params.getAll('lifecycle').length > 1 ||
    (params.has('lifecycle') && !['active', 'deleted'].includes(params.get('lifecycle')!));
  const selectedId = trash ? '' : (params.get('item') ?? '');
  const collectionId = trash ? '' : (params.get('collection') ?? '');
  const inbox = !trash && params.get('inbox') === '1';
  const projectId = trash ? '' : (params.get('project') ?? '');
  const readingStatus: LiteratureReadingStatus | '' =
    literatureReadingStatuses.find((value) => value === params.get('reading_status')) ?? '';
  useEffect(() => {
    if (invalidLifecycle || (params.has('reading_status') && params.get('reading_status') !== readingStatus)) {
      const next = new URLSearchParams(params);
      if (invalidLifecycle) next.delete('lifecycle');
      if (params.has('reading_status') && params.get('reading_status') !== readingStatus) next.delete('reading_status');
      next.delete('cursor');
      next.delete('item');
      setParams(next, { replace: true });
    }
  }, [params, readingStatus, invalidLifecycle, setParams]);
  return {
    query,
    cursor,
    lifecycle,
    trash,
    invalidLifecycle,
    selectedId,
    collectionId,
    inbox,
    projectId,
    readingStatus,
    selectCollection: (id: string) => setParams(librarySectionParams(params, { kind: 'collection', id })),
    selectProject: (id: string) => setParams(librarySectionParams(params, { kind: 'project', id })),
    selectTrash: () => setParams(librarySectionParams(params, { kind: 'trash' })),
    selectInbox: () => setParams(librarySectionParams(params, { kind: 'inbox' })),
  };
}
