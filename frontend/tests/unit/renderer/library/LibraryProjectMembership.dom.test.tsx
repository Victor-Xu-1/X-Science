import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

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
    fireEvent.change(within(screen.getByRole('region', { name: '项目' })).getByRole('searchbox', { name: '搜索项目' }), {
      target: { value: 'missing' },
    });
    expect(screen.getByText('没有匹配的项目')).toBeInTheDocument();
  });
});
