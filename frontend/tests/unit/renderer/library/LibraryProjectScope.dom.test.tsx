import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import {
  collection,
  deferred,
  installCollectionHTTP,
  json,
  mountCollections,
  page,
  paper,
} from './libraryCollectionsFixtures';

const projects = () =>
  json({
    projects: [
      { project_id: 'alpha', name: 'Alpha project' },
      { project_id: 'beta', name: 'Beta project' },
    ],
  });
const location = () => new URLSearchParams(screen.getByLabelText('Library location').textContent ?? '');
const sidebar = () => within(screen.getByRole('navigation', { name: 'Collections' }));
const scope = () => screen.getByRole('combobox', { name: 'Project scope' });

describe('Library project scope through the existing Web query owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('project-scope-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('binds project navigation, paging, reload and collection/all transitions to the real HTTP scope', async () => {
    let linked = true;
    const http = installCollectionHTTP({
      collections: () => [collection('methods', 'Methods')],
      detail: (id) => ({ ...paper(id, 'Scoped detail'), project_ids: linked ? ['alpha'] : [] }),
      intercept: (url, init) => {
        if (url.pathname === '/api/literature/items/entry/projects/alpha' && init.method === 'PUT') {
          const command = JSON.parse(String(init.body));
          expect(command.included).toBe(false);
          linked = false;
          return json({
            schema: 'x-science.literature-receipt.v1',
            operation_id: command.operation_id,
            item: { ...paper('entry', 'Scoped detail'), project_ids: [] },
          });
        }
        if (url.pathname === '/api/projects') return projects();
        if (url.pathname !== '/api/literature/items') return undefined;
        const project = url.searchParams.get('project_id');
        if (project === 'alpha' && !linked) return json(page([]));
        const title = project
          ? `${project} ${url.searchParams.has('cursor') ? 'second' : 'first'} page`
          : 'Unscoped paper';
        return json(
          page([paper('entry', title)], project && !url.searchParams.has('cursor') ? `${project}-next` : undefined)
        );
      },
    });
    let rendered = await mountCollections('/library?collection=methods&cursor=old&item=old&q=study');
    await screen.findByRole('option', { name: 'Alpha project' });
    fireEvent.change(scope(), { target: { value: 'alpha' } });
    expect(await screen.findByRole('button', { name: /alpha first page/ })).toBeInTheDocument();
    expect(location().get('project')).toBe('alpha');
    expect(location().get('q')).toBe('study');
    for (const name of ['collection', 'cursor', 'item']) expect(location().get(name)).toBeNull();
    expect(sidebar().getByRole('button', { name: 'All references' })).not.toHaveAttribute('aria-current');
    expect(screen.getByRole('heading', { name: 'Alpha project', level: 2 })).toBeInTheDocument();
    const first = http.requests.find((request) => request.path.includes('project_id=alpha'));
    expect(new URL(first!.path, 'http://localhost').searchParams.get('query')).toBe('study');
    expect(new URL(first!.path, 'http://localhost').searchParams.has('cursor')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    expect(await screen.findByRole('button', { name: /alpha second page/ })).toBeInTheDocument();
    expect(location().get('cursor')).toBe('alpha-next');
    const saved = '/library' + screen.getByLabelText('Library location').textContent;
    rendered.unmount();
    rendered = await mountCollections(saved);
    expect(await screen.findByRole('button', { name: /alpha second page/ })).toBeInTheDocument();
    expect(scope()).toHaveValue('alpha');
    fireEvent.click(screen.getByRole('button', { name: 'Back to first page' }));
    expect(await screen.findByRole('button', { name: /alpha first page/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    fireEvent.click(await screen.findByRole('button', { name: /alpha second page/ }));
    await screen.findByRole('heading', { name: 'Scoped detail' });
    fireEvent.click(screen.getByRole('button', { name: rendered.i18n.t('conversation.libraryWorkbench.edit') }));
    fireEvent.change(
      screen.getByRole('combobox', { name: rendered.i18n.t('conversation.libraryWorkbench.projectAction') }),
      { target: { value: 'alpha' } }
    );
    fireEvent.click(screen.getByRole('button', { name: rendered.i18n.t('conversation.reading.libraryUnlinkProject') }));
    await waitFor(() => expect(location().get('item')).toBeNull());
    expect(location().get('cursor')).toBeNull();
    expect(location().get('project')).toBe('alpha');
    expect(await screen.findByRole('heading', { name: 'No matching papers' })).toBeInTheDocument();
    fireEvent.click(sidebar().getByRole('button', { name: 'Methods' }));
    expect(await screen.findByRole('button', { name: /Unscoped paper/ })).toBeInTheDocument();
    expect(location().get('project')).toBeNull();
    expect(location().get('collection')).toBe('methods');
    fireEvent.change(scope(), { target: { value: 'beta' } });
    expect(await screen.findByRole('button', { name: /beta first page/ })).toBeInTheDocument();
    fireEvent.click(sidebar().getByRole('button', { name: 'All references' }));
    expect(await screen.findByRole('button', { name: /Unscoped paper/ })).toBeInTheDocument();
    expect(location().get('project')).toBeNull();
    expect(location().get('collection')).toBeNull();
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('combobox', { name: '项目范围' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '全部文献' })).toHaveAttribute('aria-current', 'page');
  });

  it('fences old project list/detail and former-account responses after a scope switch', async () => {
    const alpha = deferred<Response>();
    const beta = deferred<Response>();
    const detail = deferred<Response>();
    let alphaStarted = false;
    let betaStarted = false;
    let detailStarted = false;
    let nextAccount = false;
    installCollectionHTTP({
      collections: () => [],
      intercept: (url) => {
        if (url.pathname === '/api/projects') return projects();
        if (url.pathname === '/api/literature/items/old') {
          detailStarted = true;
          return detail.promise;
        }
        if (url.pathname !== '/api/literature/items') return undefined;
        if (nextAccount) return json(page([paper('next', 'Next owner beta paper')]));
        if (url.searchParams.get('project_id') === 'alpha') {
          alphaStarted = true;
          return alpha.promise;
        }
        betaStarted = true;
        return beta.promise;
      },
    });
    await mountCollections('/library?project=alpha&item=old');
    await waitFor(() => expect(alphaStarted && detailStarted).toBe(true));
    await screen.findByRole('option', { name: 'Beta project' });
    fireEvent.change(scope(), { target: { value: 'beta' } });
    await waitFor(() => expect(betaStarted).toBe(true));
    expect(location().get('item')).toBeNull();
    await act(async () => {
      nextAccount = true;
      setRendererAccountOwner('next-project-scope-owner');
    });
    expect(await screen.findByRole('button', { name: /Next owner beta paper/ })).toBeInTheDocument();
    await act(async () => {
      alpha.resolve(json(page([paper('alpha-old', 'Late alpha paper')])));
      beta.resolve(json(page([paper('beta-old', 'Former owner beta paper')])));
      detail.resolve(json(paper('old', 'Late alpha detail')));
      await Promise.all([alpha.promise, beta.promise, detail.promise]);
    });
    for (const title of ['Late alpha paper', 'Former owner beta paper', 'Late alpha detail'])
      expect(screen.queryByText(title)).toBeNull();
    expect(scope()).toHaveValue('beta');
    expect(screen.getByRole('button', { name: /Next owner beta paper/ })).toBeInTheDocument();
  });

  it('retries project/catalogue errors and keeps an unavailable deep-linked scope without an all-library fallback', async () => {
    let fail = true;
    const http = installCollectionHTTP({
      collections: () => [],
      intercept: (url) => {
        if (url.pathname === '/api/projects') return fail ? json({ code: 'UNAVAILABLE' }, 503) : projects();
        if (url.pathname === '/api/literature/items') return fail ? json({ code: 'UNAVAILABLE' }, 503) : json(page([]));
        return undefined;
      },
    });
    const rendered = await mountCollections('/library?project=alpha');
    expect(await screen.findByRole('heading', { name: 'Could not load Library' })).toBeInTheDocument();
    expect(scope()).toHaveValue('alpha');
    expect(screen.getByRole('option', { name: 'Project unavailable' })).toBeInTheDocument();
    fail = false;
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0]);
    await screen.findByRole('option', { name: 'Alpha project' });
    expect(await screen.findByRole('heading', { name: 'No references in this project' })).toBeInTheDocument();
    expect(
      http.requests
        .filter((request) => request.path.startsWith('/api/literature/items?'))
        .every((request) => new URL(request.path, 'http://localhost').searchParams.get('project_id') === 'alpha')
    ).toBe(true);
    rendered.unmount();
    await mountCollections('/library?project=removed-project');
    await screen.findByRole('option', { name: 'Alpha project' });
    expect(scope()).toHaveValue('removed-project');
    expect(screen.getByRole('option', { name: 'Project unavailable' })).toBeInTheDocument();
    expect(location().get('project')).toBe('removed-project');
    fireEvent.click(sidebar().getByRole('button', { name: 'All references' }));
    await waitFor(() => expect(location().get('project')).toBeNull());
  });
});
