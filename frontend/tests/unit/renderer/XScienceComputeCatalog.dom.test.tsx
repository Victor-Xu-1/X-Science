import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { createXScienceSessionDefaults } from '@/renderer/services/xScienceSessionOptions';
import { renderWithI18n, type TestLanguage } from '../i18nTestUtils';

const api = vi.hoisted(() => ({ fetch: vi.fn(), error: vi.fn(), changeSelection: vi.fn() }));
vi.mock('@arco-design/web-react', async (original) => {
  const actual = await original<typeof import('@arco-design/web-react')>();
  return { ...actual, Message: { ...actual.Message, error: api.error } };
});

const provider = (name = 'fixture') => ({
  name: `ssh:${name}`,
  displayName: `Compute ${name}`,
  family: 'ssh',
  checked: true,
});
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const noMode = async () => undefined;
function Harness({
  root = 'frame-fixture',
  incarnation = 'first',
  owner = 'owner-fixture',
  draft = false,
  selection,
}: {
  root?: string | null;
  incarnation?: string;
  owner?: string;
  draft?: boolean;
  selection?: string[];
} = {}) {
  return (
    <MemoryRouter>
      <XScienceSessionOptionsMenu
        rootFrameId={draft ? undefined : root}
        computeSelection={selection}
        onComputeSelectionChange={draft ? api.changeSelection : undefined}
        value={createXScienceSessionDefaults()}
        onChange={() => {}}
        permissionControl={
          draft
            ? undefined
            : {
                option: null,
                setStatus: { state: 'idle' },
                setConfigOption: noMode,
                contextKey: JSON.stringify([owner, root, incarnation]),
              }
        }
      />
    </MemoryRouter>
  );
}
async function openCompute() {
  fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
  fireEvent.mouseEnter(await screen.findByTestId('session-config-row-compute'));
}
const toggle = (name = 'fixture') => screen.getByRole('menuitemcheckbox', { name: `Compute Compute ${name}` });

describe('C394 Compute catalog read state', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setRendererAccountOwner('owner-fixture');
    vi.stubGlobal('fetch', api.fetch);
  });
  afterEach(() => {
    cleanup();
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
  });

  it('C394 root bridge does not treat an unresolved conversation root as a known Local draft', async () => {
    api.fetch.mockImplementation(async () => response([provider()]));
    await renderWithI18n(<Harness root={null} />, 'en-US');
    await openCompute();
    expect(screen.getByRole('menuitemradio', { name: 'Local' })).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('menuitemradio', { name: 'Local' })).toBeDisabled();
    expect(api.fetch.mock.calls.every(([url]) => !String(url).includes('/api/compute/session/'))).toBe(true);
    expect(screen.queryByText('No additional compute is configured.')).not.toBeInTheDocument();
  });

  it.each([
    [
      'en-US',
      'Compute resources could not be loaded. The current selection is not confirmed.',
      'Retry compute',
      'Local',
    ],
    ['zh-CN', '无法加载计算资源，当前选择尚未确认。', '重试计算资源', '本机'],
  ] as const)(
    'C394 failed first read is unknown, not empty or Local, and retries in %s',
    async (language, error, retry, local) => {
      let failed = true;
      api.fetch.mockImplementation(async (url: string) =>
        url === '/api/compute/providers'
          ? response(failed ? { error: 'synthetic catalog failure' } : [provider()], failed ? 503 : 200)
          : response(['ssh:fixture'])
      );
      await renderWithI18n(<Harness />, language as TestLanguage);
      await openCompute();
      await waitFor(() => expect(api.error).toHaveBeenCalledTimes(1));
      expect(screen.getByRole('alert')).toHaveTextContent(error);
      expect(screen.getByRole('menuitemradio', { name: local })).toHaveAttribute('aria-checked', 'false');
      expect(screen.getByRole('menuitemradio', { name: local })).toBeDisabled();
      expect(screen.queryByText(/No additional compute is configured|尚未配置额外计算资源/)).not.toBeInTheDocument();
      failed = false;
      fireEvent.click(screen.getByRole('menuitem', { name: retry }));
      const submenu = within(screen.getByRole('menu', { name: language === 'zh-CN' ? '计算' : 'Compute' }));
      await waitFor(() => expect(submenu.getByRole('menuitemcheckbox')).toBeEnabled());
      expect(submenu.getByRole('menuitemcheckbox')).toHaveAttribute('aria-checked', 'true');
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
      expect(api.fetch.mock.calls.filter(([url]) => url === '/api/compute/session/frame-fixture/enabled')).toHaveLength(
        2
      );
      expect(api.fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
    }
  );

  it('C394 failed refresh keeps the observed selection but freezes its old provider controls', async () => {
    let failed = false;
    api.fetch.mockImplementation(async (url: string) =>
      url === '/api/compute/providers'
        ? response([provider()])
        : response(failed ? { error: 'synthetic selection failure' } : ['ssh:fixture'], failed ? 503 : 200)
    );
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
    await waitFor(() =>
      expect(screen.getByTestId('x-science-session-options-trigger')).toHaveAttribute('aria-expanded', 'false')
    );
    failed = true;
    await openCompute();
    await waitFor(() => expect(api.error).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('alert')).toHaveTextContent('The current selection is not confirmed.');
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(toggle()).toBeDisabled();
    expect(screen.getByRole('menuitemradio', { name: 'Local' })).toBeDisabled();
    fireEvent.click(toggle());
    expect(api.fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('C394 mutation lost ACK freezes the last snapshot until a read-only retry without replaying the write', async () => {
    let stored = ['ssh:fixture'];
    api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        stored = [];
        throw new TypeError('synthetic ACK lost after persistence');
      }
      return response(url === '/api/compute/providers' ? [provider()] : stored);
    });
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    await waitFor(() => expect(api.error).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('alert')).toHaveTextContent('The current selection is not confirmed.');
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(toggle()).toBeDisabled();
    fireEvent.click(toggle());
    fireEvent.click(screen.getByRole('menuitem', { name: 'Retry compute' }));
    await waitFor(() => expect(screen.getByRole('menuitemradio', { name: 'Local' })).toBeEnabled());
    expect(screen.getByRole('menuitemradio', { name: 'Local' })).toHaveAttribute('aria-checked', 'true');
    expect(api.fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(
      api.fetch.mock.calls.filter(([url, init]) => String(url).endsWith('/enabled') && !init?.method)
    ).toHaveLength(2);
  });

  it('C394 mutation success uses authoritative readback instead of appending or filtering the old selection', async () => {
    let written = false;
    api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        written = true;
        return new Response(null, { status: 204 });
      }
      return response(
        url === '/api/compute/providers' ? [provider(), provider('other')] : [written ? 'ssh:other' : 'ssh:fixture']
      );
    });
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    await waitFor(() => expect(toggle('other')).toHaveAttribute('aria-checked', 'true'));
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(api.fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(
      api.fetch.mock.calls.filter(([url, init]) => String(url).endsWith('/enabled') && !init?.method)
    ).toHaveLength(2);
  });

  it('C394 mutation successful PUT with failed readback freezes its observed snapshot and retries only GET', async () => {
    let written = false;
    let readFailed = true;
    api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        written = true;
        return new Response(null, { status: 204 });
      }
      if (url === '/api/compute/providers') return response([provider()]);
      return written && readFailed
        ? response({ error: 'synthetic readback failure' }, 503)
        : response(written ? [] : ['ssh:fixture']);
    });
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    await screen.findByRole('alert');
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(toggle()).toBeDisabled();
    readFailed = false;
    fireEvent.click(screen.getByRole('menuitem', { name: 'Retry compute' }));
    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(api.fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
  });

  it('C394 mutation Local partial failure waits for all issued writes and never presents an atomic clear', async () => {
    let settleSecond!: (value: Response) => void;
    const second = new Promise<Response>((resolve) => {
      settleSecond = resolve;
    });
    api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        if (url.endsWith('ssh%3Afixture')) throw new TypeError('synthetic first write failed');
        return second;
      }
      return response(
        url === '/api/compute/providers' ? [provider(), provider('other')] : ['ssh:fixture', 'ssh:other']
      );
    });
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Local' }));
    await waitFor(() => expect(api.fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(2));
    expect(toggle()).toBeDisabled();
    expect(api.error).not.toHaveBeenCalled();
    await act(async () => {
      settleSecond(new Response(null, { status: 204 }));
    });
    await screen.findByRole('alert');
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(toggle('other')).toHaveAttribute('aria-checked', 'true');
    expect(toggle()).toBeDisabled();
    expect(screen.getByRole('menuitemradio', { name: 'Local' })).toHaveAttribute('aria-checked', 'false');
  });

  it('C394 mutation closed and reopened menu waits for its pending write then reads without late ACK projection', async () => {
    let settle!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      settle = resolve;
    });
    let written = false;
    api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        await pending;
        written = true;
        return new Response(null, { status: 204 });
      }
      return response(url === '/api/compute/providers' ? [provider()] : written ? [] : ['ssh:fixture']);
    });
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
    await waitFor(() =>
      expect(screen.getByTestId('x-science-session-options-trigger')).toHaveAttribute('aria-expanded', 'false')
    );
    await openCompute();
    expect(toggle()).toBeDisabled();
    expect(
      api.fetch.mock.calls.filter(([url, init]) => String(url).endsWith('/enabled') && !init?.method)
    ).toHaveLength(1);
    await act(async () => {
      settle(new Response(null, { status: 204 }));
    });
    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(api.fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(api.error).not.toHaveBeenCalled();
  });

  it.each(['conversation', 'incarnation', 'account'] as const)(
    'C394 mutation %s replacement ignores old ACK while the new owner reads its authority',
    async (kind) => {
      let rejectOld!: (reason: unknown) => void;
      const pending = new Promise<Response>((_, reject) => {
        rejectOld = reject;
      });
      let next = false;
      api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') return pending;
        return response(
          url === '/api/compute/providers'
            ? [provider(next ? 'current' : 'fixture')]
            : [next ? 'ssh:current' : 'ssh:fixture']
        );
      });
      const rendered = await renderWithI18n(<Harness />, 'en-US');
      await openCompute();
      await waitFor(() => expect(toggle()).toBeEnabled());
      fireEvent.click(toggle());
      next = true;
      const replacement = {
        root: kind === 'conversation' ? 'frame-next' : 'frame-fixture',
        incarnation: 'second',
        owner: kind === 'account' ? 'owner-next' : 'owner-fixture',
      };
      if (kind === 'account') setRendererAccountOwner(replacement.owner);
      rendered.rerender(<Harness {...replacement} />);
      await waitFor(() =>
        expect(screen.getByTestId('x-science-session-options-trigger')).toHaveAttribute('aria-expanded', 'false')
      );
      await openCompute();
      await waitFor(() => expect(toggle('current')).toBeEnabled());
      await act(async () => {
        rejectOld(new TypeError('old owner ACK unknown'));
      });
      expect(toggle('current')).toBeEnabled();
      expect(toggle('current')).toHaveAttribute('aria-checked', 'true');
      expect(api.error).not.toHaveBeenCalled();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    }
  );

  it('C394 mutation unmount suppresses its late unknown outcome without replaying the write', async () => {
    let rejectOld!: (reason: unknown) => void;
    const pending = new Promise<Response>((_, reject) => {
      rejectOld = reject;
    });
    api.fetch.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') return pending;
      return response(url === '/api/compute/providers' ? [provider()] : ['ssh:fixture']);
    });
    const rendered = await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    rendered.unmount();
    await act(async () => {
      rejectOld(new TypeError('unmounted ACK unknown'));
    });
    expect(api.error).not.toHaveBeenCalled();
    expect(api.fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(
      api.fetch.mock.calls.filter(([url, init]) => String(url).endsWith('/enabled') && !init?.method)
    ).toHaveLength(1);
  });

  it('C394 closed read cannot overwrite a reopened successful snapshot or its next retry', async () => {
    let settleOld!: (value: Response) => void;
    const old = new Promise<Response>((resolve) => {
      settleOld = resolve;
    });
    let batch = 0;
    api.fetch.mockImplementation((url: string) => {
      if (url === '/api/compute/providers') {
        batch += 1;
        return Promise.resolve(response([provider(batch === 1 ? 'old' : 'current')]));
      }
      return batch === 1 ? old : Promise.resolve(response(['ssh:current']));
    });
    await renderWithI18n(<Harness />, 'en-US');
    await openCompute();
    fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
    await waitFor(() =>
      expect(screen.getByTestId('x-science-session-options-trigger')).toHaveAttribute('aria-expanded', 'false')
    );
    await openCompute();
    await waitFor(() => expect(toggle('current')).toBeEnabled());
    await act(async () => {
      settleOld(response({ error: 'late closed failure' }, 503));
    });
    expect(toggle('current')).toHaveAttribute('aria-checked', 'true');
    expect(screen.queryByText('Compute old')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(api.error).not.toHaveBeenCalled();
  });

  it.each(['conversation', 'incarnation', 'account'] as const)(
    'C394 %s replacement fences the old read and retries only the current owner',
    async (kind) => {
      let settleOld!: (value: Response) => void;
      const old = new Promise<Response>((resolve) => {
        settleOld = resolve;
      });
      let batch = 0;
      let failCurrent = false;
      api.fetch.mockImplementation((url: string) => {
        if (url === '/api/compute/providers') {
          batch += 1;
          return Promise.resolve(response([provider(batch === 1 ? 'old' : 'current')]));
        }
        return batch === 1
          ? old
          : Promise.resolve(
              response(failCurrent ? { error: 'current read failure' } : ['ssh:current'], failCurrent ? 503 : 200)
            );
      });
      const rendered = await renderWithI18n(<Harness />, 'en-US');
      await openCompute();
      const next = {
        root: kind === 'conversation' ? 'frame-next' : 'frame-fixture',
        incarnation: 'second',
        owner: kind === 'account' ? 'owner-next' : 'owner-fixture',
      };
      if (kind === 'account') setRendererAccountOwner(next.owner);
      rendered.rerender(<Harness {...next} />);
      await waitFor(() =>
        expect(screen.getByTestId('x-science-session-options-trigger')).toHaveAttribute('aria-expanded', 'false')
      );
      await openCompute();
      await waitFor(() => expect(toggle('current')).toBeEnabled());
      await act(async () => {
        settleOld(response(['ssh:old']));
      });
      expect(toggle('current')).toHaveAttribute('aria-checked', 'true');
      expect(screen.queryByText('Compute old')).not.toBeInTheDocument();
      expect(api.error).not.toHaveBeenCalled();
      fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
      await waitFor(() =>
        expect(screen.getByTestId('x-science-session-options-trigger')).toHaveAttribute('aria-expanded', 'false')
      );
      failCurrent = true;
      await openCompute();
      await screen.findByRole('alert');
      const beforeRetry = api.fetch.mock.calls.length;
      failCurrent = false;
      fireEvent.click(screen.getByRole('menuitem', { name: 'Retry compute' }));
      await waitFor(() => expect(toggle('current')).toBeEnabled());
      expect(api.fetch.mock.calls.slice(beforeRetry).map(([url]) => url)).toEqual([
        '/api/compute/providers',
        `/api/compute/session/${next.root}/enabled`,
      ]);
    }
  );

  it.each([
    { label: 'explicit nondefault', selection: ['ssh:controlled'], summary: 'ssh:controlled' },
    { label: 'explicit Local', selection: [], summary: 'Local' },
  ])(
    'C394 controlled draft $label keeps its known display without treating props as a successful read',
    async ({ selection, summary }) => {
      api.fetch.mockResolvedValue(response({ error: 'synthetic provider read failure' }, 503));
      await renderWithI18n(<Harness draft selection={selection} />, 'en-US');
      const dot = screen.getByTestId('x-science-session-options-trigger').querySelector('span.bg-primary');
      if (selection.length) expect(dot).toBeInTheDocument();
      else expect(dot).not.toBeInTheDocument();
      expect(api.fetch).not.toHaveBeenCalled();
      await openCompute();
      await waitFor(() => expect(api.error).toHaveBeenCalledTimes(1));
      expect(screen.getByTestId('session-config-row-compute')).toHaveTextContent(summary);
      expect(screen.getByRole('alert')).toBeInTheDocument();
      const local = screen.getByRole('menuitemradio', { name: 'Local' });
      expect(local).toHaveAttribute('aria-checked', String(selection.length === 0));
      expect(local).toBeDisabled();
      fireEvent.click(local);
      expect(api.changeSelection).not.toHaveBeenCalled();
      expect(api.fetch.mock.calls.map(([url]) => url)).toEqual(['/api/compute/providers']);
    }
  );
});
