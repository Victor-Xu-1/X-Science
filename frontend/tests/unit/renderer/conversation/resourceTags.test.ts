import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeResourceTagCommand,
  decodeResourceTagReceipt,
  decodeResourceTagRef,
  decodeResourceTagSnapshot,
  normalizeResourceTagName,
  prepareResourceTagCommand,
  RESOURCE_TAG_MUTATION_BODY_BYTES,
  RESOURCE_TAG_TIMESTAMP_MAX,
  type ResourceTag,
  type ResourceTagCommand,
  type ResourceTagSnapshot,
} from '@/common/adapter/resourceTags';
import { loadResourceTags, runResourceTagCommand } from '@/renderer/services/runtime/resourceTags';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';

const op = (n = 1) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const favorite = (): ResourceTag => ({ id: 'tag-favorite', system_key: 'favorite', created_at: 0, updated_at: 0 });
const tag = (id = 'tag-a', name = 'Research'): ResourceTag => ({
  id,
  name,
  icon_key: 'tag',
  color_key: 'blue',
  created_at: 1,
  updated_at: 2,
});
const snapshot = (patch: Partial<ResourceTagSnapshot> = {}): ResourceTagSnapshot => ({
  schema: 'x-science.resource-tags.v1',
  revision: 1,
  resource_types: ['literature.item'],
  tags: [favorite(), tag()],
  assignments: [],
  ...patch,
});
const create = () =>
  ({ kind: 'create', operation_id: op(), name: 'Research', icon_key: 'tag', color_key: 'blue' }) as const;
const assignment = (assigned = true) =>
  ({
    kind: 'assignment',
    operation_id: op(5),
    tag_id: 'tag-a',
    resource_type: 'literature.item',
    resource_id: '  item-1\u00a0',
    assigned,
  }) as const;
const receipt = (command: ResourceTagCommand) => {
  let view = snapshot();
  if (command.kind === 'create' || command.kind === 'update')
    view = snapshot({
      tags: [
        favorite(),
        {
          id: 'tag-a',
          name: normalizeResourceTagName(command.name),
          icon_key: command.icon_key,
          color_key: command.color_key,
          created_at: 1,
          updated_at: 3,
        },
      ],
    });
  if (command.kind === 'delete') view = snapshot({ revision: 2, tags: [favorite()] });
  if (command.kind === 'assignment')
    view = snapshot({
      assignments: command.assigned
        ? [
            {
              tag_id: command.tag_id,
              resource_type: command.resource_type,
              resource_id: command.resource_id.trim(),
              created_at: 2,
            },
          ]
        : [],
    });
  return {
    schema: 'x-science.resource-tag-receipt.v1',
    operation_id: command.operation_id,
    action: command.kind,
    ...(command.kind === 'reorder' ? {} : { tag_id: command.kind === 'create' ? 'tag-a' : command.tag_id }),
    snapshot: view,
  };
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const fetchMock = vi.fn<typeof fetch>();
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('owner-a');
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => {
  vi.useRealTimers();
  setRendererAccountOwner('');
  vi.unstubAllGlobals();
});

describe('resource tag wire codec', () => {
  it('keeps virtual Favorite0, ordered catalog and frozen independent projections valid', () => {
    const virtual = decodeResourceTagSnapshot(snapshot({ revision: 0, tags: [favorite()] }));
    expect(virtual).toMatchObject({ revision: 0, tags: [{ system_key: 'favorite', created_at: 0, updated_at: 0 }] });
    const decoded = decodeResourceTagSnapshot(snapshot());
    expect(Object.isFrozen(decoded)).toBe(true);
    expect(Object.isFrozen(decoded.tags)).toBe(true);
    expect(Object.isFrozen(decoded.tags[1])).toBe(true);
    expect(decoded.resource_types).toEqual(['literature.item']);
  });

  it('rejects ambiguous capability/catalog/assignment shapes rather than defaulting to empty state', () => {
    const related = { tag_id: 'tag-a', resource_type: 'literature.item', resource_id: 'item-1', created_at: 2 };
    // An actual hole is intentional; a dense undefined entry is a different malformed shape.
    const sparseTags: unknown[] = [];
    sparseTags.length = 1;
    const malformed: unknown[] = [
      { ...snapshot(), revision: Number.MAX_SAFE_INTEGER + 1 },
      { ...snapshot(), unexpected: true },
      { ...snapshot(), resource_types: ['literature.item', 'literature.item'] },
      { ...snapshot(), resource_types: ['unknown.resource'] },
      { ...snapshot(), tags: [tag()] },
      { ...snapshot(), tags: [favorite(), tag(), tag()] },
      { ...snapshot(), tags: [favorite(), tag(), tag('tag-b', 'RESEARCH')] },
      { ...snapshot(), tags: [{ ...favorite(), name: 'Fake' }, tag()] },
      { ...snapshot(), tags: [favorite(), { ...tag(), icon_key: 'unknown' }] },
      { ...snapshot(), tags: [favorite(), { ...tag(), color_key: 'unknown' }] },
      { ...snapshot(), tags: [favorite(), { ...tag(), updated_at: RESOURCE_TAG_TIMESTAMP_MAX + 1 }] },
      { ...snapshot(), tags: [favorite(), { ...tag(), updated_at: 0 }] },
      { ...snapshot(), tags: [favorite(), { ...tag(), name: ' Research ' }] },
      { ...snapshot(), tags: [favorite(), { ...tag(), name: '\uD800' }] },
      { ...snapshot(), tags: [favorite(), { ...tag(), id: '文'.repeat(171) }] },
      { ...snapshot(), tags: sparseTags },
      { ...snapshot(), assignments: [{ ...related, tag_id: 'orphan' }] },
      { ...snapshot(), assignments: [related, related] },
      { ...snapshot(), assignments: [{ ...related, resource_type: 'catalog.skill' }] },
      { ...snapshot(), assignments: [{ ...related, resource_id: ' item-1 ' }] },
      { ...snapshot(), revision: 0 },
    ];
    for (const value of malformed) expect(() => decodeResourceTagSnapshot(value)).toThrow('resource tag contract');
  });

  it('matches raw and normalized UTF16 grammar without rewriting the original operation payload', () => {
    expect(normalizeResourceTagName('  Ｒesearch\tNotes ')).toBe('Research Notes');
    expect(normalizeResourceTagName('😀'.repeat(32))).toHaveLength(64);
    for (const name of [' '.repeat(64) + 'A', '😀'.repeat(33), 'İ'.repeat(64), '\uD800', '   '])
      expect(() => normalizeResourceTagName(name)).toThrow();
    const raw = { ...create(), name: ' Ｒesearch ' };
    expect(decodeResourceTagCommand(raw)).toMatchObject({ name: raw.name, operation_id: raw.operation_id });
    expect(decodeResourceTagCommand(assignment())).toMatchObject({ resource_id: assignment().resource_id });
    expect(() => decodeResourceTagCommand({ ...assignment(), resource_id: '😀'.repeat(129) })).toThrow();
    expect(decodeResourceTagCommand({ ...assignment(), resource_id: '😀'.repeat(128) })).toMatchObject({
      resource_id: '😀'.repeat(128),
    });
  });

  it('shares strict canonical resource coordinates without assuming every known type is implemented', () => {
    const reference = decodeResourceTagRef({ resource_type: 'literature.item', resource_id: ' \uFEFFitem-1\u00a0 ' });
    expect(reference).toEqual({ resource_type: 'literature.item', resource_id: 'item-1' });
    expect(Object.isFrozen(reference)).toBe(true);
    expect(() => decodeResourceTagRef({ ...reference, owner_user_id: 'other' })).toThrow();
    expect(() => decodeResourceTagRef({ ...reference, resource_type: 'unknown' })).toThrow();
    expect(() => decodeResourceTagRef({ ...reference, resource_id: '   ' })).toThrow();
    expect(decodeResourceTagRef({ resource_type: 'pdf.annotation', resource_id: 'note-1' })).toMatchObject({
      resource_type: 'pdf.annotation',
    });
    expect(decodeResourceTagSnapshot(snapshot()).resource_types).toEqual(['literature.item']);
  });

  it('requires closed full commands, explicit desired state and a caller-owned canonical operation', () => {
    const bad: unknown[] = [
      { ...create(), owner_user_id: 'other' },
      { ...create(), operation_id: 'BAD' },
      {
        kind: 'assignment',
        operation_id: op(),
        tag_id: 'tag-a',
        resource_type: 'literature.item',
        resource_id: 'item-1',
      },
      { ...assignment(), assigned: null },
      { ...assignment(), assigned: 'false' },
      { ...create(), kind: 'update', tag_id: 'tag-a', expected_updated_at: 0 },
      { ...create(), kind: 'update', tag_id: 'tag-a', expected_updated_at: RESOURCE_TAG_TIMESTAMP_MAX },
      { kind: 'reorder', operation_id: op(), tag_ids: ['tag-a', 'tag-a'] },
      { kind: 'reorder', operation_id: op(), tag_ids: ['tag-favorite'] },
    ];
    for (const value of bad) expect(() => decodeResourceTagCommand(value)).toThrow();
    const input = { kind: 'reorder' as const, operation_id: op(), tag_ids: ['tag-a'] };
    const frozen = decodeResourceTagCommand(input);
    input.tag_ids.push('tag-b');
    expect(frozen).toMatchObject({ tag_ids: ['tag-a'] });
    expect(Object.isFrozen(frozen)).toBe(true);
  });

  it('binds an original receipt to operation/action/tag and its original result, not today’s snapshot', () => {
    const command = create(),
      original = receipt(command);
    expect(decodeResourceTagReceipt(original, command)).toMatchObject({
      operation_id: command.operation_id,
      tag_id: 'tag-a',
    });
    const bad: unknown[] = [
      { ...original, operation_id: op(99) },
      { ...original, action: 'delete' },
      { ...original, tag_id: 'tag-b' },
      { ...original, unexpected: true },
      { ...original, snapshot: snapshot({ tags: [favorite(), tag('tag-a', 'Different')] }) },
    ];
    for (const value of bad) expect(() => decodeResourceTagReceipt(value, command)).toThrow();
    expect(() => decodeResourceTagReceipt(receipt(assignment(false)), assignment(true))).toThrow();
    const deletion = { kind: 'delete', operation_id: op(3), tag_id: 'tag-a' } as const;
    expect(() => decodeResourceTagReceipt({ ...receipt(deletion), snapshot: snapshot() }, deletion)).toThrow();
    const order = { kind: 'reorder', operation_id: op(4), tag_ids: ['tag-a'] } as const;
    expect(() => decodeResourceTagReceipt({ ...receipt(order), tag_id: 'tag-a' }, order)).toThrow();
  });
});

describe('resource tag transport uses the original HTTP authority', () => {
  const commands: ResourceTagCommand[] = [
    create(),
    { ...create(), kind: 'update', operation_id: op(2), tag_id: 'tag-a', expected_updated_at: 2 },
    { kind: 'delete', operation_id: op(3), tag_id: 'tag-a' },
    { kind: 'reorder', operation_id: op(4), tag_ids: ['tag-a'] },
    assignment(true),
    { ...assignment(false), operation_id: op(6) },
  ];
  const routes = [
    ['POST', '/api/tags'],
    ['PATCH', '/api/tags/tag-a'],
    ['DELETE', '/api/tags/tag-a'],
    ['PUT', '/api/tags/order'],
    ['PUT', '/api/tags/assignments'],
    ['PUT', '/api/tags/assignments'],
  ];
  it.each(
    commands.map((command, index) => ({
      command,
      route: routes[index],
      label: command.kind + (command.kind === 'assignment' ? `:${command.assigned}` : ''),
    }))
  )('sends exactly one caller-owned $label command', async ({ command, route }) => {
    fetchMock.mockResolvedValueOnce(json(receipt(command)));
    const result = await runResourceTagCommand(command);
    expect(result.operation_id).toBe(command.operation_id);
    expect(fetchMock).toHaveBeenCalledOnce();
    const [url, init] = fetchMock.mock.calls[0];
    expect(new URL(String(url)).pathname).toBe(route[1]);
    expect(init?.method).toBe(route[0]);
    expect(init?.credentials).toBe('include');
    expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json');
    const body = JSON.parse(String(init?.body));
    const { kind, ...expected } = command;
    if (kind === 'update' || kind === 'delete') delete (expected as { tag_id?: string }).tag_id;
    expect(body).toEqual(expected);
    expect(body.operation_id).toBe(command.operation_id);
    expect(body).not.toHaveProperty('kind');
  });

  it('matches the existing64KiB wire-body boundary before fetch without replacing the caller key', async () => {
    expect(RESOURCE_TAG_MUTATION_BODY_BYTES).toBe(64 * 1024);
    const base = { ...assignment(false), resource_id: 'item-1' };
    const size = new TextEncoder().encode(JSON.stringify(prepareResourceTagCommand(base).body)).length;
    const exact = { ...base, resource_id: ' '.repeat(64 * 1024 - size) + base.resource_id };
    const oversized = { ...exact, resource_id: ' ' + exact.resource_id };
    await expect(runResourceTagCommand(oversized)).rejects.toThrow('resource tag contract');
    expect(fetchMock).not.toHaveBeenCalled();
    expect(oversized.operation_id).toBe(base.operation_id);
    fetchMock.mockResolvedValueOnce(json(receipt(exact)));
    await runResourceTagCommand(exact);
    expect(fetchMock).toHaveBeenCalledOnce();
    const sent = String(fetchMock.mock.calls[0][1]?.body);
    expect(new TextEncoder().encode(sent).length).toBe(64 * 1024);
    expect(JSON.parse(sent).operation_id).toBe(base.operation_id);
  });

  it('keeps historical ACK and canonical GET separate without implicit retry or state replacement', async () => {
    const command = create(),
      original = receipt(command);
    fetchMock
      .mockResolvedValueOnce(json(original))
      .mockResolvedValueOnce(json(snapshot({ revision: 4, tags: [favorite()] })));
    const acknowledged = await runResourceTagCommand(command);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(acknowledged.snapshot.tags).toHaveLength(2);
    const current = await loadResourceTags();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(current).toMatchObject({ revision: 4, tags: [{ system_key: 'favorite' }] });
    expect(acknowledged.snapshot.tags).toHaveLength(2);
    expect(fetchMock.mock.calls[1][1]?.body).toBeUndefined();
  });

  it.each([
    [404, 'RESOURCE_TAG_UNAVAILABLE'],
    [409, 'RESOURCE_TAG_OPERATION_CONFLICT'],
    [500, 'RESOURCE_TAG_INTERNAL'],
    [503, 'RESOURCE_TAG_UNAVAILABLE'],
  ] as const)(
    'preserves typed status%s/code%s without retrying or replacing operation identity',
    async (status, code) => {
      fetchMock.mockResolvedValueOnce(json({ code }, status));
      const command = create();
      const error = await runResourceTagCommand(command).catch((reason: unknown) => reason);
      expect(error).toBeInstanceOf(BackendHttpError);
      expect(error).toMatchObject({ status, code });
      expect(fetchMock).toHaveBeenCalledOnce();
      expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body)).operation_id).toBe(command.operation_id);
    }
  );

  it('rejects invalid commands before sending and rejects malformed200 acknowledgement without a second request', async () => {
    const invalid = { ...create(), owner_user_id: 'foreign' };
    await expect(runResourceTagCommand(invalid)).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
    fetchMock.mockResolvedValueOnce(json({ ...receipt(create()), operation_id: op(99) }));
    await expect(runResourceTagCommand(create())).rejects.toThrow('resource tag contract');
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('captures the original command before the caller edits another draft', async () => {
    let resolve!: (response: Response) => void;
    fetchMock.mockImplementationOnce(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        })
    );
    const command = {
      kind: 'create' as const,
      operation_id: op(),
      name: 'Research',
      icon_key: 'tag' as const,
      color_key: 'blue' as const,
    };
    const pending = runResourceTagCommand(command);
    command.operation_id = op(99);
    command.name = 'New draft';
    resolve(json(receipt(create())));
    expect(await pending).toMatchObject({ operation_id: op() });
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({
      operation_id: op(),
      name: 'Research',
    });
  });

  it('fences a queued old account before HTTP and a late old response/error afterwards', async () => {
    const old = getRendererAccountScopeToken();
    setRendererAccountOwner('owner-b');
    await expect(loadResourceTags({ accountToken: old })).rejects.toThrow('account changed');
    expect(fetchMock).not.toHaveBeenCalled();
    for (const response of [json(snapshot()), json({ code: 'RESOURCE_TAG_UNAVAILABLE' }, 404)]) {
      let resolve!: (response: Response) => void;
      fetchMock.mockImplementationOnce(
        () =>
          new Promise<Response>((done) => {
            resolve = done;
          })
      );
      const pending = loadResourceTags();
      setRendererAccountOwner(getRendererAccountScopeToken() + '-next');
      resolve(response);
      await expect(pending).rejects.toThrow('account changed');
    }
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('cancels the original fetch via AbortSignal and sends nothing for an already-aborted request', async () => {
    const already = new AbortController();
    already.abort(new Error('cancelled before send'));
    await expect(loadResourceTags({ signal: already.signal })).rejects.toThrow('cancelled before send');
    expect(fetchMock).not.toHaveBeenCalled();
    let active: AbortSignal | undefined;
    fetchMock.mockImplementationOnce(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          active = init?.signal ?? undefined;
          active?.addEventListener('abort', () => reject(active?.reason), { once: true });
        })
    );
    const abort = new AbortController();
    const pending = runResourceTagCommand(create(), { signal: abort.signal });
    expect(active?.aborted).toBe(false);
    abort.abort(new Error('cancel original send'));
    await expect(pending).rejects.toThrow('cancel original send');
    expect(active?.aborted).toBe(true);
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it.each([
    ['read', 15_000],
    ['write', 30_000],
  ] as const)('uses the existing bounded %s timeout without a retry', async (kind, timeout) => {
    vi.useFakeTimers();
    fetchMock.mockImplementationOnce(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {
            once: true,
          });
        })
    );
    const pending = (kind === 'read' ? loadResourceTags() : runResourceTagCommand(create())).catch(
      (error: unknown) => error
    );
    await vi.advanceTimersByTimeAsync(timeout + 1);
    expect(await pending).toMatchObject({ message: expect.stringContaining('timed out') });
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
