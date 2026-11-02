import { vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  decodeResourceTagCommand,
  normalizeResourceTagName,
  type ResourceTag,
  type ResourceTagCommand,
  type ResourceTagSnapshot,
} from '@/common/adapter/resourceTags';
import { getRendererAccountOwnerId } from '@/renderer/services/rendererAccountScope';
const favorite = { id: 'tag-favorite', system_key: 'favorite', created_at: 0, updated_at: 0 } as const;
const initial = [
  favorite,
  { id: 'tag-a', name: 'Alpha', icon_key: 'tag', color_key: 'blue', created_at: 1, updated_at: 2 },
  { id: 'tag-b', name: 'Beta', icon_key: 'star', color_key: 'green', created_at: 1, updated_at: 2 },
] as ResourceTag[];
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
export const syntheticPaper: LiteratureItem = {
  id: 'paper-a',
  title: 'Synthetic research paper',
  metadata_revision: 1,
  metadata: { authors: ['Synthetic Author'], year: 2026 },
  deleted: false,
  project_ids: [],
  attachments: [],
};
/** Only HTTP is synthetic: the mounted view, account owner, durable queue, codecs and readback are real. */
export function installTagManagementHTTP() {
  const originalOwner = getRendererAccountOwnerId();
  let tags = structuredClone(initial);
  let assignments: ResourceTagSnapshot['assignments'] = [
    { tag_id: 'tag-a', resource_type: 'literature.item', resource_id: syntheticPaper.id, created_at: 2 },
  ];
  let revision = 2,
    creates = 0;
  let loseAction: ResourceTagCommand['kind'] | null = null;
  let failRead = false;
  let holdPaper: Promise<Response> | null = null;
  const papers = new Map([[syntheticPaper.id, syntheticPaper]]);
  const requests: { method: string; path: string; body?: Record<string, unknown> }[] = [];
  const ledger = new Map<string, { fingerprint: string; body: string }>();
  const snapshot = (): ResourceTagSnapshot => ({
    schema: 'x-science.resource-tags.v1',
    revision,
    resource_types: ['literature.item'],
    tags: structuredClone(tags),
    assignments: structuredClone(assignments),
  });
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input),
      method = init?.method ?? 'GET';
    const raw = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
    requests.push({ method, path, ...(raw ? { body: raw } : {}) });
    if (path === '/api/tags' && method === 'GET') {
      if (failRead) {
        failRead = false;
        return json({ code: 'RESOURCE_TAG_INTERNAL' }, 503);
      }
      if (getRendererAccountOwnerId() !== originalOwner)
        return json({ ...snapshot(), tags: [favorite], assignments: [] });
      return json(snapshot());
    }
    if (path.startsWith('/api/literature/items/') && method === 'GET') {
      const id = decodeURIComponent(path.split('/').at(-1)!);
      const paper = papers.get(id);
      if (id === syntheticPaper.id && holdPaper) return holdPaper;
      return paper ? json(paper) : json({ code: 'LITERATURE_NOT_FOUND' }, 404);
    }
    if (!raw || !path.startsWith('/api/tags')) throw new Error(`Unexpected Tag fixture request: ${method} ${path}`);
    const kind =
      method === 'POST' && path === '/api/tags'
        ? 'create'
        : method === 'PATCH'
          ? 'update'
          : method === 'DELETE'
            ? 'delete'
            : path === '/api/tags/order'
              ? 'reorder'
              : path === '/api/tags/assignments'
                ? 'assignment'
                : null;
    if (!kind) throw new Error('Unexpected Tag mutation route');
    const command = decodeResourceTagCommand({
      kind,
      ...raw,
      ...(kind === 'update' || kind === 'delete' ? { tag_id: decodeURIComponent(path.split('/').at(-1)!) } : {}),
    });
    const fingerprint = JSON.stringify({ method, path, raw });
    const existing = ledger.get(command.operation_id);
    if (existing) {
      if (existing.fingerprint !== fingerprint) return json({ code: 'RESOURCE_TAG_OPERATION_CONFLICT' }, 409);
      return new Response(existing.body, { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    let id: string | undefined;
    if (command.kind === 'create') {
      const name = normalizeResourceTagName(command.name);
      if (tags.some((tag) => 'name' in tag && tag.name.toLowerCase() === name.toLowerCase()))
        return json({ code: 'RESOURCE_TAG_NAME_CONFLICT' }, 409);
      id = `tag-created-${++creates}`;
      tags.push({
        id,
        name,
        icon_key: command.icon_key,
        color_key: command.color_key,
        created_at: ++revision,
        updated_at: revision,
      });
    } else if (command.kind === 'update') {
      const tag = tags.find((item) => item.id === command.tag_id);
      if (!tag || 'system_key' in tag || tag.updated_at !== command.expected_updated_at)
        return json({ code: 'RESOURCE_TAG_CONFLICT' }, 409);
      id = tag.id;
      tags = tags.map((item) =>
        item.id === id
          ? {
              ...tag,
              name: normalizeResourceTagName(command.name),
              icon_key: command.icon_key,
              color_key: command.color_key,
              updated_at: ++revision,
            }
          : item
      );
    } else if (command.kind === 'delete') {
      if (command.tag_id === favorite.id) return json({ code: 'RESOURCE_TAG_PROTECTED' }, 409);
      id = command.tag_id;
      tags = tags.filter((tag) => tag.id !== id);
      assignments = assignments.filter((entry) => entry.tag_id !== id);
      revision++;
    } else if (command.kind === 'reorder') {
      const byId = new Map(tags.filter((tag) => !('system_key' in tag)).map((tag) => [tag.id, tag]));
      if (command.tag_ids.length !== byId.size || command.tag_ids.some((tagId) => !byId.has(tagId)))
        return json({ code: 'RESOURCE_TAG_CONFLICT' }, 409);
      tags = [favorite, ...command.tag_ids.map((tagId) => byId.get(tagId)!)];
      revision++;
    } else {
      id = command.tag_id;
      assignments = assignments.filter((entry) => !(entry.tag_id === id && entry.resource_id === command.resource_id));
      if (command.assigned)
        assignments = [
          ...assignments,
          {
            tag_id: id,
            resource_type: command.resource_type,
            resource_id: command.resource_id,
            created_at: ++revision,
          },
        ];
      else revision++;
    }
    const body = JSON.stringify({
      schema: 'x-science.resource-tag-receipt.v1',
      operation_id: command.operation_id,
      action: command.kind,
      ...(id ? { tag_id: id } : {}),
      snapshot: snapshot(),
    });
    ledger.set(command.operation_id, { fingerprint, body });
    if (loseAction === command.kind) {
      loseAction = null;
      throw new Error('Synthetic lost response after commit');
    }
    return new Response(body, { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  return {
    requests,
    snapshot,
    addPaper: (id: string, title: string) => {
      papers.set(id, { ...syntheticPaper, id, title });
      assignments = [
        ...assignments,
        { tag_id: 'tag-a', resource_type: 'literature.item', resource_id: id, created_at: ++revision },
      ];
    },
    creates: () => creates,
    loseNext: (kind: ResourceTagCommand['kind']) => {
      loseAction = kind;
    },
    failNextRead: () => {
      failRead = true;
    },
    holdPaper: (promise: Promise<Response>) => {
      holdPaper = promise;
    },
    externalEdit: (id: string, name: string) => {
      tags = tags.map((tag) =>
        tag.id === id && 'name' in tag ? Object.assign({}, tag, { name, updated_at: ++revision }) : tag
      );
    },
  };
}
