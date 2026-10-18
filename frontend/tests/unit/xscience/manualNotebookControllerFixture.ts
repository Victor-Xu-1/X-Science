import { vi } from 'vitest';
import { inventory, owner, pendingPage, rawTarget, receipt, response, summary } from './manualNotebookFixtures';
import type { ManualNotebookRequest } from '@/renderer/services/notebook/manualNotebookTypes';

export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
export function notebookFixture() {
  const requests = new Map<string, { body: ManualNotebookRequest; state: string; kind: 'typed-v1' | 'legacy' }>();
  const fixture = {
    targets: [rawTarget],
    owner,
    requests,
    history: [] as unknown[],
    pending: null as null | ((cursor: string | null) => Response),
    submit: null as null | ((body: ManualNotebookRequest) => Promise<Response>),
    cancel: null as null | ((body: ManualNotebookRequest) => Promise<Response>),
    lookup: null as null | ((id: string) => Promise<Response>),
    kernels: null as null | (() => Promise<Response>),
    accept(body: ManualNotebookRequest, state = 'queued', kind: 'typed-v1' | 'legacy' = 'typed-v1') {
      requests.set(body.request_id, { body, state, kind });
    },
    fetch: vi.fn<typeof fetch>(),
  };
  fixture.fetch.mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'http://localhost'),
      method = init?.method ?? 'GET';
    if (url.pathname.endsWith('/kernels'))
      return fixture.kernels ? fixture.kernels() : response(inventory(fixture.targets));
    if (url.pathname.endsWith('/execution-log'))
      return response({ records: fixture.history, total: fixture.history.length, next_before: '' });
    if (url.pathname.endsWith('/kernel-executions') && method === 'GET')
      return fixture.pending
        ? fixture.pending(url.searchParams.get('cursor'))
        : response(
            pendingPage(
              [...requests.values()]
                .filter((entry) => entry.state !== 'terminal')
                .map((entry) => summary(entry.body, entry.kind))
            )
          );
    if (url.pathname.endsWith('/kernel-executions') && method === 'POST') {
      const body = JSON.parse(String(init?.body)) as ManualNotebookRequest;
      if (fixture.submit) return fixture.submit(body);
      fixture.accept(body);
      return response(receipt(body));
    }
    const match = url.pathname.match(/\/by-request\/([^/]+)(\/cancel)?$/);
    if (match) {
      if (!match[2] && fixture.lookup) return fixture.lookup(match[1]);
      const entry = requests.get(match[1]);
      if (!entry) return response({ code: 'MANUAL_EXECUTION_NOT_ACCEPTED', detail: 'not visible' }, 404);
      if (match[2]) {
        if (JSON.stringify(JSON.parse(String(init?.body))) !== JSON.stringify(entry.body))
          throw new Error('changed cancellation body');
        if (fixture.cancel) return fixture.cancel(entry.body);
        entry.state = 'cancel_requested';
      }
      const result = receipt(entry.body, entry.state, entry.kind);
      result.durable.owner_id = fixture.owner;
      return response(result);
    }
    throw new Error(`unexpected Notebook route: ${method} ${url.pathname}`);
  });
  vi.stubGlobal('fetch', fixture.fetch);
  return fixture;
}
export function submissions(fixture: ReturnType<typeof notebookFixture>) {
  return fixture.fetch.mock.calls.filter(
    ([input, init]) => String(input).endsWith('/kernel-executions') && init?.method === 'POST'
  );
}
