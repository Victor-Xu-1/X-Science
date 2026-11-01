import type { ResourceTagSnapshot } from '@/common/adapter/resourceTags';

/** A read-only empty account catalog. Fault interceptors remain the caller's first decision. */
export function readEmptyResourceTagCatalog(url: URL, init?: Pick<RequestInit, 'method'>): Response | undefined {
  if ((init?.method ?? 'GET') !== 'GET' || url.pathname !== '/api/tags' || url.search !== '') return undefined;
  const snapshot: ResourceTagSnapshot = {
    schema: 'x-science.resource-tags.v1',
    revision: 0,
    resource_types: ['literature.item'],
    tags: [{ id: 'tag-favorite', system_key: 'favorite', created_at: 0, updated_at: 0 }],
    assignments: [],
  };
  return new Response(JSON.stringify(snapshot), { status: 200, headers: { 'Content-Type': 'application/json' } });
}
