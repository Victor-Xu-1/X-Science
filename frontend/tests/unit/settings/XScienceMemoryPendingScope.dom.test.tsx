import React from 'react';
import { Message } from '@arco-design/web-react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { XScienceMemoryManager } from '@/renderer/pages/settings/components/XScienceMemoryManager';
import { renderWithI18n } from '../i18nTestUtils';

function contextFor(projectId: string | null, count = 1, enabled = true) {
  return {
    memory_enabled: enabled,
    total_user_rows: count,
    categories: [],
    sessions: [],
    entities: count
      ? [
          {
            entity_key: projectId ? 'project:' + projectId : 'profile',
            label: projectId ? 'Project ' + projectId.toUpperCase() : 'Profile',
            project_id: projectId,
            rows: Array.from({ length: count }, (_, index) => ({
              id: (projectId ?? 'profile') + '-' + index,
              body: 'Synthetic ' + (projectId ?? 'profile') + ' rule ' + index,
              categoryId: null,
              origin: 'user',
              evidence: 'stated',
              createdAt: '2026-01-01T00:00:00Z',
              updatedAt: '2026-01-01T00:00:00Z',
            })),
          },
        ]
      : [],
  };
}

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function installReads(
  projectRead: (projectId: string, count: number) => Response | Promise<Response>,
  write?: () => Response | Promise<Response>
) {
  const observed: Array<{ path: string; method: string; body?: unknown }> = [];
  const counts = new Map<string, number>();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(
        typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url,
        'http://localhost'
      );
      const method = init?.method ?? 'GET';
      observed.push({
        path: url.pathname + url.search,
        method,
        ...(typeof init?.body === 'string' ? { body: JSON.parse(init.body) } : {}),
      });
      if (method !== 'GET') {
        if (write) return write();
        throw new Error('This read regression must never write memory or preferences');
      }
      if (url.pathname === '/api/projects')
        return Response.json({
          projects: [
            { project_id: 'a', name: 'Project A' },
            { project_id: 'b', name: 'Project B' },
          ],
        });
      if (url.pathname === '/api/memory/enabled' || url.pathname === '/api/memory/auto-enabled')
        return Response.json({ enabled: true });
      if (url.pathname === '/api/memory/context') {
        const id = url.searchParams.get('project_id');
        if (!id) return Response.json(contextFor(null));
        const count = (counts.get(id) ?? 0) + 1;
        counts.set(id, count);
        return projectRead(id, count);
      }
      throw new Error('Unexpected synthetic read ' + url.pathname);
    })
  );
  return observed;
}

async function chooseProject(name: string) {
  const select = screen.getByTestId('memory-project-select');
  fireEvent.click(select.closest('.arco-select') ?? select);
  const option = await waitFor(() => {
    const found = Array.from(document.querySelectorAll<HTMLElement>('.arco-select-option, [role="option"]')).find(
      (node) => node.textContent?.trim() === name
    );
    if (!found) throw new Error('Project option is not available: ' + name);
    return found;
  });
  fireEvent.click(option);
}

async function openProjectLayer() {
  const rendered = await renderWithI18n(<XScienceMemoryManager />, 'en-US');
  await screen.findByTestId('memory-row-profile-0');
  fireEvent.click(screen.getByTestId('memory-layer-project'));
  return rendered;
}

const emptyProject = 'This project has no memories yet. Add the first project rule, context, or requirement.';

describe('Memory context scope readback', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.spyOn(Message, 'error').mockReturnValue({ close: () => undefined });
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('shows pending rather than empty/off for a different project and keeps known data during a same-project refresh', async () => {
    const firstB = deferredResponse();
    const refreshB = deferredResponse();
    const observed = installReads((id, count) =>
      id === 'b' ? (count === 1 ? firstB.promise : refreshB.promise) : Response.json(contextFor(id))
    );
    await openProjectLayer();
    await screen.findByTestId('memory-row-a-0');
    await chooseProject('Project B');

    expect(screen.getByTestId('memory-context-loading')).toBeInTheDocument();
    expect(screen.getByTestId('memory-enabled-pending')).toBeInTheDocument();
    expect(screen.queryByTestId('memory-enabled-toggle')).toBeNull();
    expect(screen.queryByText(emptyProject)).toBeNull();
    expect(screen.queryByTestId('memory-row-a-0')).toBeNull();
    expect(screen.getByTestId('memory-project-picker')).toBeInTheDocument();
    await act(async () => {
      firstB.resolve(Response.json(contextFor('b', 5)));
      await firstB.promise;
    });
    await screen.findByTestId('memory-row-b-4');
    for (let index = 0; index < 5; index++) expect(screen.getByTestId('memory-row-b-' + index)).toBeInTheDocument();
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'true');

    fireEvent.click(screen.getByTestId('memory-layer-project'));
    expect(screen.getByTestId('memory-row-b-4')).toBeInTheDocument();
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByTestId('memory-enabled-toggle')).toBeDisabled();
    expect(screen.queryByTestId('memory-enabled-pending')).toBeNull();
    expect(screen.queryByText(emptyProject)).toBeNull();
    await act(async () => {
      refreshB.resolve(Response.json(contextFor('b', 0, false)));
      await refreshB.promise;
    });
    expect(await screen.findByText(emptyProject)).toBeInTheDocument();
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'false');
    expect(screen.queryByTestId('memory-row-b-4')).toBeNull();
    expect(observed.every((request) => request.method === 'GET')).toBe(true);
  });

  it('shows a scoped read failure and retries the same project without claiming empty or disabled', async () => {
    const failed = deferredResponse();
    const observed = installReads((id, count) =>
      id === 'b' && count === 1 ? failed.promise : Response.json(contextFor(id, id === 'b' ? 5 : 1))
    );
    await openProjectLayer();
    await screen.findByTestId('memory-row-a-0');
    await chooseProject('Project B');
    await act(async () => {
      failed.resolve(Response.json({ detail: 'controlled read failure' }, { status: 503 }));
      await failed.promise;
    });

    const error = await screen.findByTestId('memory-context-error');
    expect(error).toHaveTextContent('Memory data is temporarily unavailable.');
    expect(screen.queryByText(emptyProject)).toBeNull();
    expect(screen.queryByTestId('memory-enabled-toggle')).toBeNull();
    expect(screen.queryByTestId('memory-row-a-0')).toBeNull();
    expect(screen.getByTestId('memory-project-select')).toHaveTextContent('Project B');
    fireEvent.click(within(error).getByRole('button', { name: 'Retry' }));
    await screen.findByTestId('memory-row-b-4');
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'true');
    expect(screen.queryByTestId('memory-context-error')).toBeNull();
    expect(observed.filter((request) => request.path === '/api/memory/context?project_id=b')).toHaveLength(2);
    expect(observed.every((request) => request.method === 'GET')).toBe(true);
  });

  it('rejects a late previous-project response and retains the selected read scope when language changes', async () => {
    const lateA = deferredResponse();
    const observed = installReads((id) => (id === 'a' ? lateA.promise : Response.json(contextFor('b', 5))));
    const rendered = await openProjectLayer();
    expect(screen.getByTestId('memory-context-loading')).toBeInTheDocument();
    await chooseProject('Project B');
    await screen.findByTestId('memory-row-b-4');
    // The controlled old transport ignores abort; the existing generation fence must still reject it.
    await act(async () => {
      lateA.resolve(Response.json(contextFor('a', 3, false)));
      await lateA.promise;
    });
    expect(screen.getByTestId('memory-row-b-4')).toBeInTheDocument();
    expect(screen.queryByTestId('memory-row-a-0')).toBeNull();
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'true');

    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    await waitFor(() =>
      expect(observed.filter((request) => request.path === '/api/memory/context?project_id=b')).toHaveLength(2)
    );
    await waitFor(() => expect(screen.getByTestId('memory-enabled-toggle')).not.toBeDisabled());
    expect(screen.getByTestId('memory-row-b-4')).toBeInTheDocument();
    expect(screen.getByTestId('memory-project-select')).toHaveTextContent('Project B');
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'true');
    expect(observed.filter((request) => request.path === '/api/memory/context')).toHaveLength(1);
    expect(observed.every((request) => request.method === 'GET')).toBe(true);
  });

  it.each([true, false])(
    'keeps the current project preference after a previous-project write settles (success=%s)',
    async (succeeds) => {
      const write = deferredResponse();
      const observed = installReads(
        (id) => Response.json(contextFor(id, id === 'b' ? 5 : 1, id === 'a' || succeeds)),
        () => write.promise
      );
      await openProjectLayer();
      await screen.findByTestId('memory-row-a-0');
      fireEvent.click(screen.getByTestId('memory-enabled-toggle'));
      await waitFor(() =>
        expect(observed.filter((request) => request.method === 'PUT')).toEqual([
          { path: '/api/projects/a/memory/enabled', method: 'PUT', body: { enabled: false } },
        ])
      );
      await chooseProject('Project B');
      await screen.findByTestId('memory-row-b-4');
      expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', String(succeeds));

      await act(async () => {
        write.resolve(
          succeeds
            ? Response.json({ enabled: false })
            : Response.json({ detail: 'controlled write failure' }, { status: 503 })
        );
        await write.promise;
      });
      await waitFor(() => expect(screen.getByTestId('memory-enabled-toggle')).not.toBeDisabled());
      expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', String(succeeds));
      expect(screen.getByTestId('memory-row-b-4')).toBeInTheDocument();
      expect(screen.queryByTestId('memory-context-loading')).toBeNull();
      expect(observed.filter((request) => request.path === '/api/memory/context?project_id=a')).toHaveLength(1);
      expect(observed.filter((request) => request.path === '/api/memory/context?project_id=b')).toHaveLength(
        succeeds ? 2 : 1
      );
      expect(observed.filter((request) => request.method !== 'GET')).toHaveLength(1);
    }
  );
});
