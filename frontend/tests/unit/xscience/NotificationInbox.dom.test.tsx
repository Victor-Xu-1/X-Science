import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
const events = vi.hoisted(() => ({
  completed: new Set<() => void>(),
  confirmation: new Set<(event: { type: string }) => void>(),
  runtime: new Set<() => void>(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      turnCompleted: {
        on: (callback: () => void) => {
          events.completed.add(callback);
          return () => events.completed.delete(callback);
        },
      },
      responseStream: {
        on: (callback: (event: { type: string }) => void) => {
          events.confirmation.add(callback);
          return () => events.confirmation.delete(callback);
        },
      },
    },
    runtime: {
      statusChanged: {
        on: (callback: () => void) => {
          events.runtime.add(callback);
          return () => events.runtime.delete(callback);
        },
      },
    },
    realtime: { reconnected: { on: () => () => {} } },
  },
}));
import NotificationBell from '@/renderer/components/notifications/NotificationBell';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { inboxDeferred, inboxItem, inboxResponse, inboxSnapshot } from './notificationInboxFixtures';
import { inboxWrites, notificationInboxFixture } from './notificationInboxDomFixture';

beforeEach(() => {
  setRendererAccountOwner('inbox-owner');
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
});
async function openBell() {
  const bell = await screen.findByRole('button', { name: 'Messages, 1 unread' });
  fireEvent.click(bell);
  await screen.findByRole('dialog', { name: 'Message center' });
  return bell;
}

describe('notification inbox rendered interaction', () => {
  it('shares badge, one canonical observation and one open panel across two bells without manufacturing events', async () => {
    const fixture = notificationInboxFixture();
    await renderWithI18n(
      <>
        <NotificationBell />
        <NotificationBell side='top' />
      </>,
      'en-US'
    );
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Messages, 1 unread' })).toHaveLength(2));
    expect(fixture.fetch).toHaveBeenCalledTimes(1);
    expect(events.completed.size).toBe(1);
    const bells = screen.getAllByRole('button', { name: 'Messages, 1 unread' });
    fireEvent.click(bells[0]);
    fireEvent.click(bells[1]);
    expect(screen.getAllByRole('dialog', { name: 'Message center' })).toHaveLength(1);
    expect(bells[0]).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(fixture.fetch.mock.calls.length).toBeGreaterThanOrEqual(2));
    const count = fixture.fetch.mock.calls.length;
    act(() => {
      events.completed.forEach((callback) => callback());
      events.confirmation.forEach((callback) => callback({ type: 'acp_permission' }));
    });
    await waitFor(() => expect(fixture.fetch).toHaveBeenCalledTimes(count + 1));
    expect(screen.getAllByRole('button', { name: 'Messages, 1 unread' })).toHaveLength(2);
    expect(inboxWrites(fixture)).toHaveLength(0);
  });

  it('marks only the rendered read-all receipt and reserves duplicate clicks while new notifications arrive', async () => {
    const fixture = notificationInboxFixture(),
      pending = inboxDeferred<Response>();
    fixture.write = () => pending.promise;
    await renderWithI18n(<NotificationBell />, 'en-US');
    await openBell();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Mark all read' })).toBeEnabled());
    const original = fixture.snapshot().read_receipt;
    fixture.items.push(inboxItem('new'));
    fixture.revision++;
    const marking = screen.getByRole('button', { name: 'Mark all read' });
    fireEvent.click(marking);
    fireEvent.click(marking);
    expect(inboxWrites(fixture)).toHaveLength(1);
    const body = JSON.parse(String(inboxWrites(fixture)[0][1]?.body));
    expect(body).toEqual({ read_receipt: original });
    await act(async () => {
      pending.resolve(fixture.apply('/read-all', body));
      await pending.promise;
    });
    expect(await screen.findByRole('button', { name: 'Messages, 1 unread' })).toBeVisible();
    expect(fixture.items.find((item) => item.id === 'new')?.read_at).toBeUndefined();
  });

  it('retains rows and retries the same marking body after a lost response and newer authoritative refresh', async () => {
    const fixture = notificationInboxFixture();
    fixture.write = async () => {
      throw new TypeError('lost marking response');
    };
    await renderWithI18n(<NotificationBell />, 'en-US');
    await openBell();
    fireEvent.click(screen.getByRole('button', { name: 'Mark all read' }));
    await screen.findByRole('button', { name: 'Retry the same marking request' });
    const body = inboxWrites(fixture)[0][1]?.body;
    fixture.items.push(inboxItem('new'));
    fixture.revision++;
    act(() => {
      events.completed.forEach((callback) => callback());
    });
    await screen.findByRole('button', { name: 'Messages, 2 unread' });
    expect(screen.getByText('Original task notice-1')).toBeVisible();
    fixture.write = null;
    fireEvent.click(screen.getByRole('button', { name: 'Retry the same marking request' }));
    await waitFor(() => expect(inboxWrites(fixture)).toHaveLength(2));
    expect(inboxWrites(fixture)[1][1]?.body).toBe(body);
    await screen.findByRole('button', { name: 'Messages, 1 unread' });
  });

  it('only navigates and marks read; a pending confirmation is not approved, answered or executed', async () => {
    const fixture = notificationInboxFixture(),
      navigate = vi.fn();
    fixture.items = [inboxItem('notice-1', { kind: 'authorization.required', action_state: 'pending' })];
    await renderWithI18n(<NotificationBell onNavigate={navigate} />, 'en-US');
    await openBell();
    fireEvent.click(screen.getByRole('button', { name: /Original task notice-1/ }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/conversation/frame-notice-1'));
    await waitFor(() => expect(inboxWrites(fixture)).toHaveLength(1));
    expect(inboxWrites(fixture)[0][0]).toBe('/api/notifications/inbox/read');
    expect(inboxWrites(fixture)[0][1]?.body).toBe('{"ids":["notice-1"]}');
    expect(fixture.items[0].action_state).toBe('pending');
  });

  it('disables invalidated targets and refuses an incarnation replacement during click preflight', async () => {
    const fixture = notificationInboxFixture(),
      navigate = vi.fn();
    fixture.items = [inboxItem('gone', { target_invalidated_at: '2026-10-18T02:00:00Z' }), inboxItem()];
    fixture.target = async (id) => inboxResponse({ id, extra: { frame_incarnation_id: 'replacement' } });
    await renderWithI18n(<NotificationBell onNavigate={navigate} />, 'en-US');
    fireEvent.click(await screen.findByRole('button', { name: 'Messages, 2 unread' }));
    expect(screen.getByRole('button', { name: /Original task gone/ })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: /Original task notice-1/ }));
    expect(await screen.findByRole('alert')).toHaveTextContent('could not be verified');
    expect(navigate).not.toHaveBeenCalled();
    expect(inboxWrites(fixture)).toHaveLength(0);
  });

  it('keeps loaded content on read failure and recovers through the visible Retry action', async () => {
    const fixture = notificationInboxFixture();
    await renderWithI18n(<NotificationBell />, 'en-US');
    await screen.findByRole('button', { name: 'Messages, 1 unread' });
    fixture.read = async () => inboxResponse({ code: 'INBOX_UNAVAILABLE' }, 503);
    await openBell();
    expect(await screen.findByRole('alert')).toHaveTextContent('Showing previously loaded messages');
    expect(screen.getByText('Original task notice-1')).toBeVisible();
    fixture.read = null;
    fireEvent.click(screen.getByRole('button', { name: 'Retry', exact: true }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });

  it('shows incomplete empty recovery and advances only the explicit opaque cursor', async () => {
    const fixture = notificationInboxFixture();
    fixture.read = async (cursor) =>
      cursor
        ? inboxResponse(inboxSnapshot([inboxItem()], 2))
        : inboxResponse(
            inboxSnapshot([], 1, {
              reconciliation_incomplete: true,
              next_cursor: 'scan-next',
              incomplete_reason: 'more_sources',
            })
          );
    await renderWithI18n(<NotificationBell />, 'en-US');
    fireEvent.click(await screen.findByRole('button', { name: 'Messages, 0 recovered unread' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Recovery is incomplete');
    expect(fixture.fetch.mock.calls.some(([url]) => String(url).includes('cursor='))).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Continue recovery' }));
    await screen.findByRole('button', { name: 'Messages, 1 unread' });
    expect(fixture.fetch.mock.calls.filter(([url]) => String(url).includes('cursor='))).toEqual([
      expect.arrayContaining(['/api/notifications/inbox?cursor=scan-next']),
    ]);
  });

  it('removes the old account portal and ignores its late marking response', async () => {
    const fixture = notificationInboxFixture(),
      pending = inboxDeferred<Response>();
    fixture.write = () => pending.promise;
    await renderWithI18n(<NotificationBell />, 'en-US');
    await openBell();
    const oldPanel = screen.getByRole('dialog', { name: 'Message center' });
    fireEvent.click(screen.getByRole('button', { name: 'Mark all read' }));
    fixture.items = [inboxItem('second-owner')];
    fixture.revision++;
    act(() => {
      setRendererAccountOwner('second-owner');
    });
    expect(oldPanel).not.toBeInTheDocument();
    await act(async () => {
      pending.resolve(inboxResponse(inboxSnapshot([], 3)));
      await pending.promise;
    });
    await openBell();
    expect(await screen.findByText('Original task second-owner')).toBeVisible();
    expect(screen.queryByText('Original task notice-1')).not.toBeInTheDocument();
  });

  it('allows the same outside click to activate Send instead of requiring a second click', async () => {
    notificationInboxFixture();
    const send = vi.fn();
    await renderWithI18n(
      <>
        <NotificationBell />
        <button onClick={send}>Send</button>
      </>,
      'en-US'
    );
    await openBell();
    const target = screen.getByRole('button', { name: 'Send' });
    fireEvent.pointerDown(target);
    fireEvent.click(target);
    expect(send).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog', { name: 'Message center' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Dismiss messages' })).not.toBeInTheDocument();
  });

  it('uses the mobile sheet keyboard boundary and shared Chinese resources, restoring focus after Escape', async () => {
    notificationInboxFixture();
    const view = await renderWithI18n(<NotificationBell mobile />, 'en-US');
    const bell = await openBell(),
      panel = screen.getByRole('dialog', { name: 'Message center' });
    expect(panel).toHaveAttribute('aria-modal', 'true');
    const buttons = within(panel)
      .getAllByRole('button')
      .filter((button) => !button.hasAttribute('disabled'));
    buttons[buttons.length - 1].focus();
    fireEvent.keyDown(document, { key: 'Tab' });
    expect(buttons[0]).toHaveFocus();
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('dialog', { name: '消息中心' })).toBeVisible();
    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(bell).toHaveFocus());
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
