import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TagsWorkbench } from '@/renderer/pages/settings/tags/TagsWorkbench';
import { setRendererAccountOwner, resetRendererAccountScopeForTest } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { installTagManagementHTTP, syntheticPaper } from './tagManagementHTTPFixture';
function Location() {
  return <output data-testid='route'>{useLocation().search}</output>;
}
const renderTags = (query = '', language: 'en-US' | 'zh-CN' = 'en-US') =>
  renderWithI18n(
    <MemoryRouter initialEntries={[`/settings/tags${query}`]}>
      <TagsWorkbench />
      <Location />
    </MemoryRouter>,
    language
  );
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('tag-manager-a');
  localStorage.clear();
  sessionStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  localStorage.clear();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
const write = (backend: ReturnType<typeof installTagManagementHTTP>, method: string) =>
  backend.requests.filter((request) => request.method === method);

describe('global Tags complete frontend path with real owner and typed HTTP', () => {
  it('creates an independent Tag with chosen icon/color and navigates only after canonical GET', async () => {
    const backend = installTagManagementHTTP();
    await renderTags();
    fireEvent.click(await screen.findByRole('button', { name: 'New Tag' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'Evidence review' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Icon: Book' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Color: Purple' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('heading', { name: 'Evidence review' })).toBeInTheDocument();
    expect(write(backend, 'POST')).toHaveLength(1);
    expect(write(backend, 'POST')[0].body).toMatchObject({
      name: 'Evidence review',
      icon_key: 'book-open',
      color_key: 'purple',
    });
    expect(screen.getByTestId('route').textContent).toContain('tag=tag-created-1');
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });
  it('preserves an edit rejected by real CAS and allocates a new original operation only after explicit conflict choice', async () => {
    const backend = installTagManagementHTTP();
    await renderTags('?tag=tag-a');
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Tag' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'My draft' } });
    backend.externalEdit('tag-a', 'Other writer');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Tag version updated')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('My draft');
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(write(backend, 'PATCH')[0].body?.expected_updated_at).toBe(2);
    fireEvent.click(screen.getByRole('button', { name: 'Continue editing draft' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('heading', { name: 'My draft' })).toBeInTheDocument();
    const writes = write(backend, 'PATCH');
    expect(writes).toHaveLength(2);
    expect(writes[1].body?.expected_updated_at).toBe(3);
    expect(writes[1].body?.operation_id).not.toBe(writes[0].body?.operation_id);
  });
  it('restores the original uncertain save after closing and new account-owner instance without duplicate creation', async () => {
    const backend = installTagManagementHTTP();
    backend.loseNext('create');
    const page = await renderTags('?view=create');
    await screen.findByRole('textbox', { name: 'Name' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'Recover me' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Resume save' })).toBeEnabled());
    expect(backend.creates()).toBe(1);
    expect(localStorage.length).toBe(1);
    const original = write(backend, 'POST')[0].body;
    page.unmount();
    act(() => {
      setRendererAccountOwner(null);
      setRendererAccountOwner('tag-manager-a');
    });
    await renderTags('?view=create');
    expect(await screen.findByText('Your unfinished changes were restored.')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Resume save' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Resume save' }));
    expect(await screen.findByRole('heading', { name: 'Recover me' })).toBeInTheDocument();
    expect(backend.creates()).toBe(1);
    for (const request of write(backend, 'POST')) expect(request.body).toEqual(original);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });
  it('shows actual assignment impact, permits cancel and deletes only the Tag/relationships', async () => {
    const backend = installTagManagementHTTP();
    await renderTags('?tag=tag-a');
    fireEvent.click(await screen.findByRole('button', { name: 'Delete Tag' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Assignments to remove: 1.')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(write(backend, 'DELETE')).toHaveLength(0);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Delete Tag' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete Tag' }));
    await waitFor(() => expect(backend.snapshot().tags.some((tag) => tag.id === 'tag-a')).toBe(false));
    expect(backend.snapshot().assignments).toHaveLength(0);
    expect(write(backend, 'DELETE')).toHaveLength(1);
    expect(syntheticPaper.title).toBe('Synthetic research paper');
  });
  it('reorders every custom ID with the keyboard while keeping Favorite first', async () => {
    const backend = installTagManagementHTTP();
    await renderTags();
    const grip = await screen.findByRole('button', { name: 'Reorder Beta' });
    fireEvent.keyDown(grip, { key: 'ArrowUp' });
    await waitFor(() =>
      expect(backend.snapshot().tags.map((tag) => tag.id)).toEqual(['tag-favorite', 'tag-b', 'tag-a'])
    );
    expect(write(backend, 'PUT')[0].body?.tag_ids).toEqual(['tag-b', 'tag-a']);
    expect(await screen.findByText('Moved Beta to position 2.')).toBeInTheDocument();
    const rail = screen.getByRole('complementary', { name: 'Tags' });
    expect(
      Array.from(rail.querySelectorAll('[data-reorderable-tag-id]')).map((row) =>
        row.getAttribute('data-reorderable-tag-id')
      )
    ).toEqual(['tag-b', 'tag-a']);
  });
  it('cancels a pointer drag without writing and submits the complete order after a real drop target', async () => {
    const backend = installTagManagementHTTP();
    await renderTags();
    const grip = await screen.findByRole('button', { name: 'Reorder Beta' });
    const alpha = screen.getByRole('button', { name: 'Reorder Alpha' }).closest('li')!;
    vi.spyOn(alpha, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      bottom: 30,
      left: 0,
      right: 200,
      width: 200,
      height: 30,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    const pointer = (name: string, y: number) => {
      const event = new Event(name, { bubbles: true, cancelable: true });
      Object.defineProperties(event, {
        pointerId: { value: 1 },
        isPrimary: { value: true },
        button: { value: 0 },
        clientY: { value: y },
      });
      fireEvent(grip, event);
    };
    pointer('pointerdown', 45);
    pointer('pointermove', 5);
    pointer('pointercancel', 5);
    expect(write(backend, 'PUT')).toHaveLength(0);
    pointer('pointerdown', 45);
    pointer('pointermove', 5);
    pointer('pointerup', 5);
    await waitFor(() => expect(write(backend, 'PUT')).toHaveLength(1));
    expect(write(backend, 'PUT')[0].body?.tag_ids).toEqual(['tag-b', 'tag-a']);
    await waitFor(() =>
      expect(backend.snapshot().tags.map((tag) => tag.id)).toEqual(['tag-favorite', 'tag-b', 'tag-a'])
    );
  });
  it('reuses the original delete operation after a lost response instead of treating unknown as success', async () => {
    const backend = installTagManagementHTTP();
    backend.loseNext('delete');
    await renderTags('?tag=tag-a');
    fireEvent.click(await screen.findByRole('button', { name: 'Delete Tag' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete Tag' }));
    const resume = await screen.findByRole('button', { name: 'Resume deletion' });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(localStorage.length).toBe(1);
    fireEvent.click(resume);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    const deletes = write(backend, 'DELETE');
    expect(deletes).toHaveLength(2);
    expect(deletes[1].body).toEqual(deletes[0].body);
    expect(localStorage.length).toBe(0);
    expect(backend.snapshot().tags.some((tag) => tag.id === 'tag-a')).toBe(false);
  });
  it('loads actual related literature, links its real item and unassigns without deleting either resource', async () => {
    const backend = installTagManagementHTTP();
    await renderTags('?tag=tag-a');
    const resource = await screen.findByRole('link', { name: 'Synthetic research paper' });
    expect(resource).toHaveAttribute('href', '#/library?item=paper-a');
    fireEvent.click(screen.getByRole('button', { name: 'Remove Synthetic research paper from this Tag' }));
    await waitFor(() => expect(backend.snapshot().assignments).toHaveLength(0));
    expect(backend.snapshot().tags).toHaveLength(3);
    expect(write(backend, 'DELETE')).toHaveLength(0);
    expect(write(backend, 'PUT')[0].body).toMatchObject({
      tag_id: 'tag-a',
      resource_type: 'literature.item',
      resource_id: 'paper-a',
      assigned: false,
    });
  });
  it('guards dirty internal navigation and restores the draft after leaving/reopening the editor', async () => {
    installTagManagementHTTP();
    await renderTags('?view=create');
    await screen.findByRole('textbox', { name: 'Name' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'Unfinished' } });
    fireEvent.click(screen.getByRole('button', { name: 'Back to Tags' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Unfinished');
    fireEvent.click(screen.getByRole('button', { name: 'Back to Tags' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Leave editor' }));
    fireEvent.click(await screen.findByRole('button', { name: 'New Tag' }));
    expect(await screen.findByRole('textbox', { name: 'Name' })).toHaveValue('Unfinished');
  });
  it('keeps canonical relationship order when concurrent resource responses arrive in reverse order', async () => {
    const backend = installTagManagementHTTP();
    backend.addPaper('paper-b', 'Second resource');
    let release!: (value: Response) => void;
    backend.holdPaper(
      new Promise((resolve) => {
        release = resolve;
      })
    );
    await renderTags('?tag=tag-a');
    expect(await screen.findByRole('link', { name: 'Second resource' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Synthetic research paper' })).not.toBeInTheDocument();
    await act(async () =>
      release(
        new Response(JSON.stringify(syntheticPaper), { status: 200, headers: { 'Content-Type': 'application/json' } })
      )
    );
    await screen.findByRole('link', { name: 'Synthetic research paper' });
    const links = within(screen.getByRole('region', { name: 'Tagged resources' })).getAllByRole('link');
    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      '#/library?item=paper-a',
      '#/library?item=paper-b',
    ]);
    expect(backend.requests.filter((request) => request.path.startsWith('/api/literature/items/'))).toHaveLength(2);
  });
  it('rejects a late old-account literature response and keeps the new account blank', async () => {
    const backend = installTagManagementHTTP();
    let release!: (value: Response) => void;
    backend.holdPaper(
      new Promise((resolve) => {
        release = resolve;
      })
    );
    await renderTags('?tag=tag-a');
    await screen.findByRole('button', { name: /^Alpha/ });
    await waitFor(() => expect(backend.requests.some((request) => request.path.endsWith('/paper-a'))).toBe(true));
    act(() => setRendererAccountOwner('tag-manager-b'));
    await act(async () =>
      release(
        new Response(JSON.stringify(syntheticPaper), { status: 200, headers: { 'Content-Type': 'application/json' } })
      )
    );
    await waitFor(() => expect(screen.queryByRole('button', { name: /^Alpha/ })).not.toBeInTheDocument());
    expect(screen.queryByRole('link', { name: 'Synthetic research paper' })).not.toBeInTheDocument();
    expect(write(backend, 'POST')).toHaveLength(0);
  });
  it('shows corrupt UI draft recovery without silently clearing its bytes or submitting a write', async () => {
    const backend = installTagManagementHTTP();
    const key = 'x-science.tag-editor-draft.v1:tag-manager-a:new';
    sessionStorage.setItem(key, '{broken');
    await renderTags('?view=create');
    expect(await screen.findByText('Could not preserve draft recovery. Try again before saving.')).toBeInTheDocument();
    expect(sessionStorage.getItem(key)).toBe('{broken');
    expect(write(backend, 'POST')).toHaveLength(0);
    fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), { target: { value: 'Fresh draft' } });
    await waitFor(() => expect(JSON.parse(sessionStorage.getItem(key)!).fields.name).toBe('Fresh draft'));
    expect(write(backend, 'POST')).toHaveLength(0);
  });
  it('keeps the same core controls available with Chinese labels', async () => {
    installTagManagementHTTP();
    await renderTags('', 'zh-CN');
    expect(await screen.findByRole('button', { name: '新建标签' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: '新建标签' }));
    expect(screen.getByRole('textbox', { name: '名称' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: '图标: 书籍' })).toBeInTheDocument();
  });
});
