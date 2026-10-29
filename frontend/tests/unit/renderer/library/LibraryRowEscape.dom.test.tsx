import React, { useState } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { LibraryRowActions } from '@/renderer/pages/library/LibraryRowActions';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { paper } from './libraryCollectionsFixtures';

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('row-escape-owner');
});
afterEach(cleanup);

async function openMenu() {
  const trigger = screen.getByRole('button', { name: 'Actions for Paper A' });
  fireEvent.click(trigger);
  const edit = await screen.findByRole('menuitem', { name: 'Edit' });
  return { trigger, edit };
}

it('closes Escape from the focused menu or trigger and restores only its current enabled trigger', async () => {
  for (const origin of ['menu', 'trigger']) {
    const change = vi.fn();
    const openItem = vi.fn();
    const view = await renderWithI18n(<LibraryRowActions item={paper()} disabled={false} change={change} openItem={openItem} />, 'en-US');
    const { trigger, edit } = await openMenu();
    const target = origin === 'menu' ? edit : trigger;
    target.focus();
    expect(target).toHaveFocus();
    fireEvent.keyDown(target, { key: 'Escape', code: 'Escape', keyCode: 27 });
    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull());
    expect(trigger).toHaveFocus();
    expect(change).not.toHaveBeenCalled();
    expect(openItem).not.toHaveBeenCalled();
    view.unmount();
  }
});

it('does not restore focus for outside dismissal, disabled or replaced scopes, or a retired account', async () => {
  let disable: (value: boolean) => void = () => undefined;
  let replace: () => void = () => undefined;
  function Fixture() {
    const [disabled, setDisabled] = useState(false);
    const [scope, setScope] = useState(0);
    disable = setDisabled;
    replace = () => setScope((value) => value + 1);
    return <><button type='button'>Outside</button><LibraryRowActions key={scope} item={paper()} disabled={disabled} change={() => undefined} openItem={() => undefined} /></>;
  }
  await renderWithI18n(<Fixture />, 'en-US');
  const outside = screen.getByRole('button', { name: 'Outside' });
  let menu = await openMenu();
  outside.focus();
  fireEvent.mouseDown(outside);
  fireEvent.click(outside);
  await waitFor(() => expect(menu.trigger).toHaveAttribute('aria-expanded', 'false'));
  expect(outside).toHaveFocus();
  menu = await openMenu();
  outside.focus();
  await act(async () => disable(true));
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull());
  expect(outside).toHaveFocus();
  await act(async () => disable(false));
  menu = await openMenu();
  const oldTrigger = menu.trigger;
  outside.focus();
  await act(async () => replace());
  expect(oldTrigger.isConnected).toBe(false);
  expect(outside).toHaveFocus();
  menu = await openMenu();
  await act(async () => setRendererAccountOwner('replacement-escape-owner'));
  outside.focus();
  // A queued key from a stale account must not focus its still-mounted old trigger.
  fireEvent.keyDown(menu.edit, { key: 'Escape', code: 'Escape', keyCode: 27 });
  await waitFor(() => expect(menu.trigger).toHaveAttribute('aria-expanded', 'false'));
  expect(outside).toHaveFocus();
});

it('leaves Arrow and original Arco Enter dispatch intact', async () => {
  const item = paper();
  const openItem = vi.fn();
  const change = vi.fn();
  await renderWithI18n(<LibraryRowActions item={item} disabled={false} change={change} openItem={openItem} />, 'en-US');
  const { trigger, edit } = await openMenu();
  edit.focus();
  fireEvent.keyDown(edit, { key: 'ArrowDown', code: 'ArrowDown', keyCode: 40 });
  expect(trigger).toHaveAttribute('aria-expanded', 'true');
  expect(openItem).not.toHaveBeenCalled();
  fireEvent.keyDown(edit, { key: 'Enter', code: 'Enter', keyCode: 13 });
  expect(openItem).toHaveBeenCalledExactlyOnceWith(item);
  expect(change).not.toHaveBeenCalled();
  await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
});
