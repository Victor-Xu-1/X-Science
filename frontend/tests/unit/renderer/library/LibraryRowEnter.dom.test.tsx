import React, { useState } from 'react';
import { cleanup, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { LibraryRowActions } from '@/renderer/pages/library/LibraryRowActions';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import { useLibraryDetailDialog } from '@/renderer/pages/library/useLibraryDetailDialog';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { renderWithI18n } from '../../i18nTestUtils';

const paper: LiteratureItem = {
  id: 'a',
  title: 'Paper A',
  metadata_revision: 1,
  metadata: {},
  deleted: false,
  project_ids: [],
  attachments: [],
};
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('row-enter-owner');
});
afterEach(cleanup);

async function openMenu() {
  const trigger = screen.getByRole('button', { name: 'Actions for Paper A' });
  fireEvent.click(trigger);
  const edit = await screen.findByRole('menuitem', { name: 'Edit' });
  edit.focus();
  return { trigger, edit };
}

// jsdom has no browser default activation. Reproduce the independently observed
// Chrome sequence: uncancelled Enter clicks the newly focused active element.
function enterCycle(target: HTMLElement) {
  const down = createEvent.keyDown(target, {
    key: 'Enter',
    code: 'Enter',
    keyCode: 13,
    which: 13,
    bubbles: true,
    cancelable: true,
  });
  const performDefault = fireEvent(target, down);
  if (performDefault && document.activeElement instanceof HTMLElement) fireEvent.click(document.activeElement);
  if (document.activeElement)
    fireEvent.keyUp(document.activeElement, { key: 'Enter', code: 'Enter', keyCode: 13, which: 13 });
}

it('keeps a completed keyboard selection closed after the full native-default cycle', async () => {
  const openItem = vi.fn();
  const change = vi.fn();
  await renderWithI18n(
    <LibraryRowActions item={paper} disabled={false} change={change} openItem={openItem} />,
    'en-US'
  );
  const { trigger, edit } = await openMenu();
  enterCycle(edit);
  expect(openItem).toHaveBeenCalledExactlyOnceWith(paper);
  expect(change).not.toHaveBeenCalled();
  await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull());
});

it('does not default-activate a newly focused real detail-dialog control', async () => {
  const openItem = vi.fn();
  const detailClick = vi.fn();
  function Fixture() {
    const [visible, setVisible] = useState(false);
    const dialog = useLibraryDetailDialog(getRendererAccountScopeToken(), 'ready', () => setVisible(false), visible);
    return (
      <>
        <LibraryRowActions
          item={paper}
          disabled={false}
          change={() => undefined}
          openItem={(item) => {
            openItem(item);
            setVisible(true);
          }}
        />
        <WorkbenchModal
          visible={visible}
          footer={null}
          title={null}
          closable={false}
          focusLock
          autoFocus
          escToExit={false}
          unmountOnExit={false}
          afterOpen={dialog.focus}
          onCancel={dialog.close}
          className='library-workbench__detail-dialog'
          maskStyle={{ backgroundColor: 'transparent' }}
          mask={visible}
          wrapStyle={visible ? undefined : { visibility: 'hidden', pointerEvents: 'none' }}
          modalRender={(node) =>
            React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
              ? React.cloneElement(node, { 'aria-label': 'Paper A', ...dialog.events })
              : node
          }
        >
          <div ref={dialog.contentRef}>
            <button onClick={detailClick}>Edit details</button>
            <button onClick={dialog.close}>Close</button>
          </div>
        </WorkbenchModal>
      </>
    );
  }
  await renderWithI18n(<Fixture />, 'en-US');
  const { trigger, edit } = await openMenu();
  enterCycle(edit);
  const dialog = await screen.findByRole('dialog', { name: 'Paper A' });
  expect(within(dialog).getByRole('button', { name: 'Edit details' })).toBeInTheDocument();
  expect(openItem).toHaveBeenCalledExactlyOnceWith(paper);
  expect(detailClick).not.toHaveBeenCalled();
  await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull());
});

it('preserves pointer activation and keyboard lifecycle dispatch without another action', async () => {
  const openItem = vi.fn();
  const change = vi.fn();
  await renderWithI18n(
    <LibraryRowActions item={paper} disabled={false} change={change} openItem={openItem} />,
    'en-US'
  );
  let menu = await openMenu();
  const click = createEvent.click(menu.edit, { bubbles: true, cancelable: true });
  fireEvent(menu.edit, click);
  expect(click.defaultPrevented).toBe(false);
  expect(openItem).toHaveBeenCalledExactlyOnceWith(paper);
  await waitFor(() => expect(menu.trigger).toHaveAttribute('aria-expanded', 'false'));
  menu = await openMenu();
  const lifecycle = await screen.findByRole('menuitem', { name: 'Move to Trash' });
  lifecycle.focus();
  enterCycle(lifecycle);
  expect(change).toHaveBeenCalledExactlyOnceWith(paper, true);
  expect(openItem).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(menu.trigger).toHaveAttribute('aria-expanded', 'false'));
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Move to Trash' })).toBeNull());
});
