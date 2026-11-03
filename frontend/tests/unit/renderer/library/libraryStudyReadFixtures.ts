import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  literatureStudySourceKey,
  literatureStudyVersion,
  type LiteratureStudyView,
} from '@/common/adapter/literatureStudySnapshot';

/** Only the real read route is supplied; mutations and fault interceptors stay owned by the caller. */
export function readNotGeneratedLiteratureStudy(
  url: URL,
  init: Pick<RequestInit, 'method'> | undefined,
  readItem: (id: string) => LiteratureItem | undefined
): Response | undefined {
  const match = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/study-snapshot$/);
  const language = url.searchParams.get('language');
  if (
    (init?.method ?? 'GET') !== 'GET' ||
    !match ||
    [...url.searchParams].length !== 1 ||
    !language ||
    language.length > 64 ||
    !/^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$/.test(language)
  )
    return undefined;
  const id = decodeURIComponent(match[1]);
  const item = readItem(id);
  if (!item || item.id !== id) throw new Error('Unexpected study fixture item');
  if (item.deleted)
    return new Response(JSON.stringify({ code: 'LITERATURE_UNAVAILABLE' }), {
      status: 404,
      headers: { 'Content-Type': 'application/json' },
    });
  const view: LiteratureStudyView = {
    schema: 'x-science.literature-study.v1',
    source_key: literatureStudySourceKey(item),
    revision: 0,
    available: Boolean(
      (typeof item.metadata.abstract === 'string' && item.metadata.abstract.trim()) ||
      literatureStudyVersion(item)?.available
    ),
  };
  return new Response(JSON.stringify(view), { status: 200, headers: { 'Content-Type': 'application/json' } });
}
