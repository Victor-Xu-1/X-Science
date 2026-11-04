import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCandidate, LiteratureTaskOrigin } from '@/common/adapter/literatureCandidates';
import { LibraryCandidateOrigin } from '@/renderer/pages/library/LibraryCandidateOrigin';
import { LibraryInbox } from '@/renderer/pages/library/LibraryInbox';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

const origin: LiteratureTaskOrigin = {
  kind: 'agent',
  frame_id: '7c36532b-57b8-4788-8b12-2d33d8e02c75',
  frame_incarnation_id: '8506ba8b-312e-4ce7-9b67-19c927525575',
  root_frame_id: '514d02bc-f302-454e-89d5-1d29ebd139b7',
  root_frame_incarnation_id: '4345549d-05ad-4a1f-90dd-60e2ac2383b0',
  project_id: 'alpha',
  project_available: true,
  recorded_at: '2026-11-04T02:03:04.123456789Z',
};
const candidate: LiteratureCandidate = {
  id: 'candidate-one',
  title: 'Task discovery',
  provider: 'crossref',
  external_id: '10.1000/task-discovery',
  metadata: { task_origins: [{ frame_id: 'forged-task', project_id: 'beta' }] },
  state: 'pending',
  task_origins: [origin],
};
const directoryProject = (projectId: string, name: string): XScienceProject => ({
  projectId,
  name,
  latestConversationId: null,
  description: null,
  context: null,
  conversationCount: 0,
  artifactCount: 0,
  createdAt: null,
  updatedAt: null,
  lastActiveAt: null,
});
const release: Array<() => void> = [];

function expectNoInternalOriginText(element: HTMLElement) {
  for (const value of [
    origin.frame_id,
    origin.frame_incarnation_id,
    origin.root_frame_id,
    origin.root_frame_incarnation_id,
    origin.recorded_at,
    origin.project_id!,
  ])
    expect(element).not.toHaveTextContent(value);
  expect(element.querySelector('time')).toBeNull();
}

describe('Agent discoveries through the existing Inbox and Library owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('task-inbox-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const done of release.splice(0)) done();
      await Promise.resolve();
    });
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('refreshes externally staged discoveries on visible focus without writes and fences the former account read', async () => {
    let visibility: DocumentVisibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
    let candidates: LiteratureCandidate[] = [];
    let held: ReturnType<typeof deferred<Response>> | undefined;
    const response = () => json({ schema: 'x-science.literature-candidates.v1', candidates });
    const http = installCollectionHTTP({
      collections: () => [],
      intercept: (url) => {
        if (url.pathname !== '/api/literature/candidates') return undefined;
        const pending = held;
        held = undefined;
        return pending?.promise ?? response();
      },
    });
    const actions = { submit: vi.fn(), close: vi.fn(), openItem: vi.fn() };
    let account = getRendererAccountScopeToken();
    const rendered = await renderWithI18n(<LibraryInbox account={account} busy={false} {...actions} />, 'en-US');
    await screen.findByText('No candidates in this view.');
    const reads = () => http.requests.filter((request) => request.path.startsWith('/api/literature/candidates?'));
    const initialReads = reads().length;
    candidates = [candidate]; // Another task stages a discovery outside this mounted view.
    visibility = 'hidden';
    fireEvent.focus(window);
    fireEvent(document, new Event('visibilitychange'));
    expect(reads()).toHaveLength(initialReads);
    visibility = 'visible';
    rendered.rerender(<LibraryInbox account={account} busy {...actions} />);
    fireEvent.focus(window);
    expect(reads()).toHaveLength(initialReads);
    rendered.rerender(<LibraryInbox account={account} busy={false} {...actions} />);
    const refreshed = deferred<Response>();
    held = refreshed;
    release.push(() => refreshed.resolve(response()));
    fireEvent.focus(window);
    fireEvent(document, new Event('visibilitychange'));
    fireEvent.focus(window);
    await waitFor(() => expect(reads()).toHaveLength(initialReads + 1));
    await act(async () => {
      refreshed.resolve(response());
      await refreshed.promise;
    });
    expect(await screen.findByRole('heading', { name: candidate.title })).toBeInTheDocument();
    expectNoInternalOriginText(screen.getByRole('region', { name: 'From research task' }));
    expect(screen.getByRole('button', { name: 'Add to Library' })).toBeEnabled();
    expect(reads()).toHaveLength(initialReads + 1);
    const formerAccount = deferred<Response>();
    held = formerAccount;
    release.push(() => formerAccount.resolve(response()));
    fireEvent.focus(window);
    await waitFor(() => expect(reads()).toHaveLength(initialReads + 2));
    await act(async () => {
      setRendererAccountOwner('next-task-inbox-owner');
      account = getRendererAccountScopeToken();
    });
    fireEvent.focus(window);
    expect(reads()).toHaveLength(initialReads + 2);
    candidates = [{ ...candidate, id: 'next-candidate', title: 'Next owner discovery' }];
    rendered.rerender(<LibraryInbox account={account} busy={false} {...actions} />);
    await screen.findByRole('heading', { name: 'Next owner discovery' });
    await act(async () => {
      formerAccount.resolve(
        json({
          schema: 'x-science.literature-candidates.v1',
          candidates: [{ ...candidate, title: 'Late former owner discovery' }],
        })
      );
      await formerAccount.promise;
    });
    expect(screen.queryByRole('heading', { name: 'Late former owner discovery' })).toBeNull();
    expect(screen.getByRole('heading', { name: 'Next owner discovery' })).toBeInTheDocument();
    expect(http.requests.every((request) => request.method === 'GET')).toBe(true);
    expect(actions.submit).not.toHaveBeenCalled();
  });

  it('keeps a task discovery pending until user acceptance and reads its project membership from the accepted item', async () => {
    let accepted = false;
    let holdDetail = true;
    const acknowledgement = deferred<Response>();
    const detailReadback = deferred<Response>();
    const acceptedItem = () => ({ ...paper('accepted-item', candidate.title), project_ids: accepted ? ['alpha'] : [] });
    release.push(
      () => acknowledgement.resolve(json({})),
      () => detailReadback.resolve(json(acceptedItem()))
    );
    const http = installCollectionHTTP({
      collections: () => [],
      items: () => (accepted ? [acceptedItem()] : []),
      detail: acceptedItem,
      intercept: (url, init) => {
        if (url.pathname === '/api/projects')
          return json({
            projects: [
              { project_id: 'alpha', name: 'Trusted destination' },
              { project_id: 'beta', name: 'Other project' },
            ],
          });
        if (url.pathname === '/api/literature/candidates')
          return json({ schema: 'x-science.literature-candidates.v1', candidates: accepted ? [] : [candidate] });
        if (url.pathname === '/api/literature/candidates/candidate-one/accept' && init.method === 'POST')
          return acknowledgement.promise;
        if (url.pathname === '/api/literature/items/accepted-item' && holdDetail) return detailReadback.promise;
        return undefined;
      },
    });
    const rendered = await mountCollections('/library?inbox=1&project=beta');
    await screen.findByRole('heading', { name: candidate.title });
    const originBlock = screen.getByRole('region', { name: 'From research task' });
    const source = within(originBlock);
    expectNoInternalOriginText(originBlock);
    expect(await source.findByText('Trusted destination')).toBeInTheDocument();
    expect(source.queryByText('Other project')).toBeNull();
    expect(screen.queryByText('forged-task')).toBeNull();
    expect(source.getByText('Waiting for you to add this reference to Library.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Open in Library' })).toBeNull();
    expect(http.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
    const accept = screen.getByRole('button', { name: 'Add to Library' });
    await waitFor(() => expect(accept).toBeEnabled());
    fireEvent.click(accept);
    await waitFor(() => expect(http.requests.filter((request) => request.method === 'POST')).toHaveLength(1));
    const write = http.requests.find((request) => request.method === 'POST')!;
    expect(write.path).toBe('/api/literature/candidates/candidate-one/accept');
    const body = JSON.parse(write.body!) as { operation_id: string };
    expect(Object.keys(body)).toEqual(['operation_id']);
    expect(accept).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Open in Library' })).toBeNull();
    await act(async () => {
      accepted = true;
      acknowledgement.resolve(
        json({
          schema: 'x-science.literature-candidate-receipt.v1',
          operation_id: body.operation_id,
          action: 'accept',
          candidate: { ...candidate, state: 'accepted', item_id: 'accepted-item' },
        })
      );
      await acknowledgement.promise;
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Open in Library' }));
    await waitFor(() =>
      expect(http.requests.some((request) => request.path === '/api/literature/items/accepted-item')).toBe(true)
    );
    expect(screen.queryByRole('checkbox', { name: 'Trusted destination' })).toBeNull();
    await act(async () => {
      holdDetail = false;
      detailReadback.resolve(json(acceptedItem()));
      await detailReadback.promise;
    });
    const membership = await screen.findByRole('checkbox', { name: 'Trusted destination' });
    expect(membership).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Other project' })).not.toBeChecked();
    expect(http.requests.filter((request) => request.method !== 'GET')).toEqual([write]);
    rendered.unmount();
    await mountCollections('/library?item=accepted-item');
    expect(await screen.findByRole('checkbox', { name: 'Trusted destination' })).toBeChecked();
    expect(http.requests.filter((request) => request.method !== 'GET')).toEqual([write]);
  });

  it('shows unavailable original destinations, deduplicates projects and translates pending/dismissed source text', async () => {
    const origins = [origin, { ...origin, frame_id: 'second-task', frame_incarnation_id: 'second-run' }];
    const view = await renderWithI18n(
      <LibraryCandidateOrigin origins={origins} state='pending' projects={{ key: 'projects', status: 'loading' }} />,
      'en-US'
    );
    expect(screen.getByText('Loading project destination…')).toBeInTheDocument();
    expectNoInternalOriginText(screen.getByRole('region', { name: 'From research task' }));
    expect(
      within(screen.getByRole('list', { name: 'Project destinations after approval' })).getAllByRole('listitem')
    ).toHaveLength(1);
    const other = directoryProject('beta', 'Current project');
    view.rerender(
      <LibraryCandidateOrigin
        origins={origins}
        state='pending'
        projects={{ key: 'projects', status: 'ready', value: [other] }}
      />
    );
    expect(screen.getByText('Project destination unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Current project')).toBeNull();
    const target = directoryProject('alpha', 'Trusted destination');
    view.rerender(
      <LibraryCandidateOrigin
        origins={origins}
        state='pending'
        projects={{ key: 'projects', status: 'ready', value: [target], refreshError: true }}
      />
    );
    expect(screen.getByText('Project destination unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Trusted destination')).toBeNull();
    // A recreated project with the same public ID must not rename or authorize the former destination.
    view.rerender(
      <LibraryCandidateOrigin
        origins={[{ ...origin, project_available: false }]}
        state='pending'
        projects={{ key: 'projects', status: 'ready', value: [target] }}
      />
    );
    expect(screen.getByText('Project destination unavailable')).toBeInTheDocument();
    expectNoInternalOriginText(screen.getByRole('region', { name: 'From research task' }));
    expect(screen.queryByText('Trusted destination')).toBeNull();
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expectNoInternalOriginText(screen.getByRole('region', { name: '来自研究任务' }));
    expect(screen.getByText('等待你将这篇文献加入文献库。')).toBeInTheDocument();
    expect(screen.getByText('目标项目不可用')).toBeInTheDocument();
    view.rerender(
      <LibraryCandidateOrigin
        origins={origins}
        state='dismissed'
        projects={{ key: 'projects', status: 'ready', value: [target] }}
      />
    );
    expect(screen.getByText('恢复这篇文献后，可重新审阅并加入文献库。')).toBeInTheDocument();
    expect(screen.getByText('Trusted destination')).toBeInTheDocument();
    expectNoInternalOriginText(screen.getByRole('region', { name: '来自研究任务' }));
    const { project_id: _project, project_available: _available, ...noProject } = origin;
    view.rerender(<LibraryCandidateOrigin origins={[noProject]} state='pending' />);
    expect(screen.getByText('确认收录后加入文献库，不关联项目。')).toBeInTheDocument();
    expectNoInternalOriginText(screen.getByRole('region', { name: '来自研究任务' }));
    expect(screen.queryByRole('list', { name: '确认收录后的目标项目' })).toBeNull();
  });
});
