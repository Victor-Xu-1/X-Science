import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { LibraryProjectMembership } from '@/renderer/pages/library/LibraryProjectMembership';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

const projects = [
  { project_id: 'alpha', name: 'Alpha project' },
  { project_id: 'beta', name: 'Beta project' },
  { project_id: 'gamma', name: 'Gamma project' },
  { project_id: 'delta', name: 'Delta project' },
  { project_id: 'epsilon', name: 'Epsilon project' },
  { project_id: 'zeta', name: 'Zeta project' },
];
const links = () => within(screen.getByRole('region', { name: 'Projects' }));

describe('Overview project membership through the existing Web owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('overview-project-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('searches project names locally and links/unlinks the unchanged target through Page receipt and authoritative readback', async () => {
    let included = false;
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    const detail = () => ({ ...paper(), project_ids: included ? ['beta'] : [] });
    const http = installCollectionHTTP({
      collections: () => [],
      items: () => [detail()],
      detail,
      intercept: (url, init) => {
        if (url.pathname === '/api/projects') return json({ projects });
        if (init.method !== 'PUT') return undefined;
        const body = JSON.parse(String(init.body));
        writes.push({ path: url.pathname, body });
        included = body.included;
        return json({ schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item: detail() });
      },
    });
    let rendered = await mountCollections('/library?item=a');
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).toBeEnabled());
    expect(screen.getAllByRole('heading', { name: 'Projects' })).toHaveLength(1);
    expect(screen.queryByText('Projects', { selector: 'dt' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Edit reference' })).toBeNull();
    const search = links().getByRole('searchbox', { name: 'Search projects' });
    const beforeTyping = http.requests.length;
    fireEvent.change(search, { target: { value: '  bETa  ' } });
    expect(links().getAllByRole('checkbox')).toHaveLength(1);
    fireEvent.change(search, { target: { value: 'no match' } });
    expect(links().getByText('No matching projects')).toBeInTheDocument();
    expect(links().queryByRole('checkbox')).toBeNull();
    fireEvent.change(search, { target: { value: 'beta' } });
    expect(http.requests).toHaveLength(beforeTyping);
    fireEvent.click(links().getByRole('checkbox', { name: 'Beta project' }));
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).toBeChecked());
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({ path: '/api/literature/items/a/projects/beta', body: { included: true } });
    expect(typeof writes[0].body.operation_id).toBe('string');
    expect(links().getByRole('searchbox')).toHaveValue('beta');
    rendered.unmount();
    rendered = await mountCollections('/library?item=a');
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).toBeChecked());
    expect(links().getByRole('searchbox')).toHaveValue('');
    fireEvent.click(links().getByRole('checkbox', { name: 'Beta project' }));
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).not.toBeChecked());
    expect(writes).toHaveLength(2);
    expect(writes[1]).toMatchObject({ path: '/api/literature/items/a/projects/beta', body: { included: false } });
    expect(writes[1].body.operation_id).not.toBe(writes[0].body.operation_id);
    expect(http.requests.filter((request) => !['GET', 'PUT'].includes(request.method))).toHaveLength(0);
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    fireEvent.change(
      within(screen.getByRole('region', { name: '项目' })).getByRole('searchbox', { name: '搜索项目' }),
      {
        target: { value: 'missing' },
      }
    );
    expect(screen.getByText('没有匹配的项目')).toBeInTheDocument();
  });

  it('retains an uncertain membership and retries the same operation through the existing Page controller', async () => {
    let included = false;
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    const detail = () => ({ ...paper(), project_ids: included ? ['beta'] : [] });
    installCollectionHTTP({
      collections: () => [],
      items: () => [detail()],
      detail,
      intercept: (url, init) => {
        if (url.pathname === '/api/projects') return json({ projects });
        if (init.method !== 'PUT') return undefined;
        const body = JSON.parse(String(init.body));
        writes.push({ path: url.pathname, body });
        if (writes.length === 1) return json({ code: 'UNAVAILABLE' }, 503);
        included = body.included;
        return json({ schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item: detail() });
      },
    });
    await mountCollections('/library?item=a');
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).toBeEnabled());
    fireEvent.click(links().getByRole('checkbox', { name: 'Beta project' }));
    const retry = await screen.findByRole('button', { name: 'Retry' });
    expect(links().getByRole('checkbox', { name: 'Beta project' })).not.toBeChecked();
    expect(links().getByRole('checkbox', { name: 'Alpha project' })).toBeDisabled();
    fireEvent.click(links().getByRole('checkbox', { name: 'Alpha project' }));
    expect(writes).toHaveLength(1);
    fireEvent.click(retry);
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).toBeChecked());
    await waitFor(() => expect(links().getByRole('checkbox', { name: 'Beta project' })).toBeEnabled());
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
    expect(writes[1]).toMatchObject({ path: '/api/literature/items/a/projects/beta', body: { included: true } });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('keeps readiness and empty states honest and fences query and late completion by account/item/revision', async () => {
    type Props = React.ComponentProps<typeof LibraryProjectMembership>;
    const old = deferred<unknown>();
    const submit = vi.fn<Props['submit']>(() => old.promise);
    const retry = vi.fn();
    let value: Props = {
      item: paper(),
      account: getRendererAccountScopeToken(),
      disabled: false,
      submit,
      retry,
      view: { status: 'ready' },
      projects: projects.map((project): Props['projects'][number] => ({
        projectId: project.project_id,
        name: project.name,
        latestConversationId: null,
        description: null,
        context: null,
        conversationCount: 0,
        artifactCount: 0,
        createdAt: null,
        updatedAt: null,
        lastActiveAt: null,
      })),
    };
    const rendered = await renderWithI18n(<LibraryProjectMembership {...value} />, 'en-US');
    fireEvent.change(links().getByRole('searchbox'), { target: { value: 'beta' } });
    fireEvent.click(links().getByRole('checkbox', { name: 'Beta project' }));
    expect(submit).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'membership', item_id: 'a', project_id: 'beta', included: true })
    );
    value = { ...value, item: paper('b') };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().getByRole('searchbox')).toHaveValue('');
    fireEvent.change(links().getByRole('searchbox'), { target: { value: 'alpha' } });
    await act(async () => {
      old.resolve({});
      await old.promise;
    });
    expect(links().getByRole('searchbox')).toHaveValue('alpha');
    expect(links().getByRole('checkbox', { name: 'Alpha project' })).not.toBeChecked();
    value = {
      ...value,
      item: { ...value.item, metadata_revision: 2 },
      view: { status: 'ready', refreshing: true, refreshError: true },
    };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().getByRole('searchbox')).toHaveValue('alpha');
    expect(links().getByRole('checkbox', { name: 'Alpha project' })).toBeDisabled();
    fireEvent.click(links().getByRole('checkbox', { name: 'Alpha project' }));
    expect(submit).toHaveBeenCalledTimes(1);
    fireEvent.click(links().getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledTimes(1);
    value = { ...value, view: { status: 'ready' }, projects: value.projects.slice(0, 5) };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().queryByRole('searchbox')).toBeNull();
    expect(links().getAllByRole('checkbox')).toHaveLength(5);
    value = { ...value, projects: [] };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().getByText('No active projects')).toBeInTheDocument();
    value = { ...value, view: { status: 'loading' } };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().getByRole('status')).toBeInTheDocument();
    expect(links().queryByText('No active projects')).toBeNull();
    value = { ...value, view: { status: 'error' } };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().getByRole('alert')).toBeInTheDocument();
    act(() => {
      setRendererAccountOwner('overview-project-next-owner');
    });
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(screen.queryByRole('region', { name: 'Projects' })).toBeNull();
    value = {
      ...value,
      account: getRendererAccountScopeToken(),
      view: { status: 'ready' },
      projects: projects.map((project, index): Props['projects'][number] => ({
        ...projects[index],
        projectId: project.project_id,
        name: project.name,
        latestConversationId: null,
        description: null,
        context: null,
        conversationCount: 0,
        artifactCount: 0,
        createdAt: null,
        updatedAt: null,
        lastActiveAt: null,
      })),
    };
    rendered.rerender(<LibraryProjectMembership {...value} />);
    expect(links().getByRole('searchbox')).toHaveValue('');
    expect(links().getAllByRole('checkbox')).toHaveLength(6);
    expect(submit).toHaveBeenCalledTimes(1);
  });
});
