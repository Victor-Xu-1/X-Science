import React from 'react';
import { act, cleanup, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ResourceTag } from '@/common/adapter/resourceTags';
import { ResourceTagMenu, type ResourceTagMenuProps } from '@/renderer/pages/library/ResourceTagMenu';
import { ResourceTagBadges } from '@/renderer/pages/library/ResourceTagBadges';
import { renderWithI18n } from '../../i18nTestUtils';

const tags: readonly ResourceTag[] = [
  { id: 'system-favorite', system_key: 'favorite', created_at: 0, updated_at: 0 },
  { id: 'a', name: 'Alpha', icon_key: 'tag', color_key: 'blue', created_at: 1, updated_at: 1 },
  { id: 'b', name: 'Beta', icon_key: 'flask-conical', color_key: 'green', created_at: 1, updated_at: 1 },
];
function props(overrides: Partial<ResourceTagMenuProps> = {}): ResourceTagMenuProps {
  return {
    targetKey: 'account:paper-a',
    tags,
    assignedIds: [],
    pendingTagIds: [],
    creating: false,
    disabled: false,
    onAssign: vi.fn().mockResolvedValue(undefined),
    onCreate: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}
function deferred() {
  let resolve!: () => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<void>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
async function open() {
  fireEvent.click(screen.getByRole('button', { name: 'Manage tags' }));
  const input = await screen.findByRole('combobox', { name: 'Search tags' });
  await waitFor(() => expect(input).toHaveFocus());
  return input;
}
afterEach(cleanup);

describe('controlled Library resource Tag controls', () => {
  it('opens the same menu from a custom read-only badge trigger', async () => {
    await renderWithI18n(
      <ResourceTagMenu
        {...props()}
        trigger={<ResourceTagBadges targetKey='account:paper-a' tags={tags} assignedIds={['a', 'b']} />}
      />,
      'en-US'
    );
    const button = screen.getByRole('button', { name: 'Manage tags' });
    expect(button).toContainElement(screen.getByText('Alpha'));
    expect(within(button).queryByRole('button')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Alpha'));
    expect(await screen.findByRole('combobox', { name: 'Search tags' })).toBeInTheDocument();
    expect(screen.getAllByRole('dialog', { name: 'Manage tags' })).toHaveLength(1);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(button.getAttribute('aria-controls')).toBe(screen.getByRole('dialog', { name: 'Manage tags' }).id);
  });

  it('shows two read-only badges, overflow and system-key presentation without changing the catalog', async () => {
    await renderWithI18n(
      <ResourceTagBadges targetKey='a' tags={tags} assignedIds={tags.map((tag) => tag.id)} />,
      'en-US'
    );
    expect(screen.getByText('Favorites').closest('[data-color]')).toHaveAttribute('data-color', 'amber');
    expect(screen.getByText('Alpha')).toBeInTheDocument();
    expect(screen.queryByText('Beta')).not.toBeInTheDocument();
    expect(screen.getByText('+1')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('keeps checked and pending state controlled, accepts another tag while one is pending and stays open', async () => {
    const hold = deferred();
    const initial = props({ onAssign: vi.fn(() => hold.promise) });
    const view = await renderWithI18n(<ResourceTagMenu {...initial} />, 'en-US');
    await open();
    fireEvent.click(screen.getByRole('option', { name: 'Alpha' }));
    expect(initial.onAssign).toHaveBeenCalledWith('a', true);
    expect(screen.getByRole('option', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'false');
    view.rerender(<ResourceTagMenu {...initial} assignedIds={['a']} pendingTagIds={['a']} />);
    expect(screen.getByRole('option', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('option', { name: 'Alpha' })).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(screen.getByRole('option', { name: 'Alpha' }));
    fireEvent.click(screen.getByRole('option', { name: 'Beta' }));
    expect(initial.onAssign).toHaveBeenCalledTimes(2);
    expect(initial.onAssign).toHaveBeenLastCalledWith('b', true);
    expect(screen.getByRole('combobox')).toBeInTheDocument();
    await act(async () => hold.resolve());
  });

  it('matches canonical names without duplicate creation, treats commas literally and limits only create while pending', async () => {
    const initial = props();
    const view = await renderWithI18n(<ResourceTagMenu {...initial} />, 'en-US');
    const input = await open();
    expect(input).toHaveAttribute('maxlength', '64');
    fireEvent.change(input, { target: { value: ' Ａlpha\t' } });
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(screen.getByRole('option', { name: 'Alpha' })).toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'Alpha, Beta' } });
    fireEvent.click(screen.getByRole('option', { name: 'Create “Alpha, Beta”' }));
    await waitFor(() => expect(initial.onCreate).toHaveBeenCalledWith('Alpha, Beta'));
    await waitFor(() => expect(input).toHaveValue(''));
    view.rerender(<ResourceTagMenu {...initial} creating error='A prior create is awaiting confirmation' />);
    fireEvent.click(screen.getByRole('option', { name: 'Beta' }));
    expect(initial.onAssign).toHaveBeenCalledWith('b', true);
    fireEvent.change(input, { target: { value: 'Novel' } });
    const create = screen.getByRole('option', { name: 'Create “Novel”' });
    expect(create).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(create);
    expect(initial.onCreate).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('supports arrow navigation and Enter, ignores IME confirmation, and Escape closes only its own dialog', async () => {
    const initial = props();
    const parentKey = vi.fn();
    await renderWithI18n(
      <section role='dialog' aria-label='Paper details' onKeyDown={parentKey}>
        <ResourceTagMenu {...initial} />
        <button type='button'>Next detail action</button>
      </section>,
      'en-US'
    );
    const input = await open();
    expect(screen.getByRole('dialog', { name: 'Paper details' })).toContainElement(
      screen.getByRole('dialog', { name: 'Manage tags' })
    );
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(initial.onAssign).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(initial.onAssign).toHaveBeenCalledWith('a', true);
    parentKey.mockClear();
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Manage tags' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Manage tags' })).toHaveFocus();
    expect(parentKey).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Paper details' })).toBeInTheDocument();
  });

  it('dismisses on Tab without preventing normal focus navigation or submitting a search', async () => {
    const initial = props();
    await renderWithI18n(
      <>
        <ResourceTagMenu {...initial} />
        <button type='button'>Next</button>
      </>,
      'en-US'
    );
    const input = await open();
    fireEvent.change(input, { target: { value: 'Unsubmitted query' } });
    const tab = createEvent.keyDown(input, { key: 'Tab', bubbles: true, cancelable: true });
    fireEvent(input, tab);
    expect(tab.defaultPrevented).toBe(false);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(initial.onCreate).not.toHaveBeenCalled();
    expect(initial.onAssign).not.toHaveBeenCalled();
    await open();
    expect(screen.getByRole('combobox')).toHaveValue('');
  });

  it('does not clear later edits, cancel accepted creates or project a late completion into a new target', async () => {
    const first = deferred();
    const second = deferred();
    const initial = props({ onCreate: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise) });
    const view = await renderWithI18n(<ResourceTagMenu {...initial} />, 'en-US');
    let input = await open();
    fireEvent.change(input, { target: { value: 'Original' } });
    fireEvent.click(screen.getByRole('option', { name: 'Create “Original”' }));
    fireEvent.change(input, { target: { value: 'Later edit' } });
    await act(async () => first.resolve());
    expect(input).toHaveValue('Later edit');
    fireEvent.click(screen.getByRole('option', { name: 'Create “Later edit”' }));
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(initial.onCreate).toHaveBeenCalledTimes(2);
    view.rerender(<ResourceTagMenu {...initial} targetKey='account:paper-b' />);
    input = await open();
    fireEvent.change(input, { target: { value: 'New target search' } });
    await act(async () => second.reject(new Error('Old target rejection')));
    expect(input).toHaveValue('New target search');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('retains search and reports real rejection, disables revoked controls, and exposes no modal mask', async () => {
    const initial = props({ onAssign: vi.fn().mockRejectedValue(new Error('denied')) });
    const view = await renderWithI18n(<ResourceTagMenu {...initial} />, 'en-US');
    const input = await open();
    fireEvent.change(input, { target: { value: 'Alpha' } });
    fireEvent.click(screen.getByRole('option', { name: 'Alpha' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not update tags');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveValue('Alpha');
    expect(screen.getByRole('dialog', { name: 'Manage tags' })).toHaveAttribute('aria-modal', 'false');
    view.rerender(<ResourceTagMenu {...initial} disabled />);
    await waitFor(() => expect(screen.queryByRole('combobox')).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Manage tags' })).toBeDisabled();
  });

  it('renders all detail badges and only requests removal of this resource assignment', async () => {
    const remove = vi.fn().mockResolvedValue(undefined);
    await renderWithI18n(
      <ResourceTagBadges
        targetKey='account:paper-a'
        tags={tags}
        assignedIds={tags.map((tag) => tag.id)}
        limit={Infinity}
        pendingTagIds={['b']}
        onRemove={remove}
      />,
      'en-US'
    );
    expect(screen.getByText('Beta')).toBeInTheDocument();
    const button = screen.getByRole('button', { name: 'Remove tag Alpha' });
    fireEvent.click(button);
    expect(remove).toHaveBeenCalledExactlyOnceWith('a');
    expect(screen.getByText('Alpha')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Remove tag Beta' })).toBeDisabled();
    expect(within(button.parentElement!).getByText('Alpha')).toBeInTheDocument();
  });
});
