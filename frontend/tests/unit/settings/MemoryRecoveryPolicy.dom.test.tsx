import React from 'react';
import { Message } from '@arco-design/web-react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { XScienceMemoryManager } from '@/renderer/pages/settings/components/XScienceMemoryManager';
import { getRendererAccountOwnerId, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';

function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function installPreferences() {
  const policies = new Map<string, string>();
  const writes: Array<{
    owner: string;
    body: { enabled: boolean; forked_view_recovery?: string };
    signal?: AbortSignal | null;
    reply: ReturnType<typeof deferred>;
  }> = [];
  const reads: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(
        typeof input === 'string' ? input : input instanceof URL ? input.href : input.url,
        'http://localhost'
      );
      const owner = getRendererAccountOwnerId();
      if (url.pathname === '/api/memory/auto-enabled' && init?.method === 'PUT') {
        const reply = deferred();
        writes.push({ owner, body: JSON.parse(String(init.body)), signal: init.signal, reply });
        return reply.promise;
      }
      reads.push(owner + ':' + url.pathname);
      if (url.pathname === '/api/projects') return Promise.resolve(Response.json({ projects: [] }));
      if (url.pathname === '/api/memory/enabled' || url.pathname === '/api/memory/auto-enabled') {
        return Promise.resolve(
          Response.json({
            enabled: true,
            ...(policies.has(owner) ? { forked_view_recovery: policies.get(owner) } : {}),
          })
        );
      }
      if (url.pathname === '/api/memory/context')
        return Promise.resolve(
          Response.json({
            entities: [],
            categories: [],
            sessions: [],
            total_user_rows: 0,
            memory_enabled: true,
            ...(policies.has(owner) ? { forked_view_recovery: policies.get(owner) } : {}),
          })
        );
      throw new Error('Unexpected synthetic Memory request');
    })
  );
  const acknowledge = (index: number) => {
    const write = writes[index];
    const policy = write.body.forked_view_recovery ?? policies.get(write.owner) ?? 'strict';
    policies.set(write.owner, policy);
    write.reply.resolve(Response.json({ enabled: write.body.enabled, forked_view_recovery: policy }));
  };
  return { policies, writes, reads, acknowledge };
}

const control = () => {
  const element = screen.getByTestId('memory-recovery-policy');
  return element.closest('.arco-select') ?? element;
};

async function choosePolicy(label: string) {
  fireEvent.click(control());
  const option = await waitFor(() => {
    const found = Array.from(document.querySelectorAll<HTMLElement>('.arco-select-option, [role="option"]')).find(
      (node) => node.textContent?.trim() === label
    );
    if (!found) throw new Error('Recovery policy option is unavailable');
    return found;
  });
  fireEvent.click(option);
}

describe('Memory forked view recovery settings', () => {
  beforeEach(() => {
    setRendererAccountOwner('recovery-owner-a');
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    for (const kind of ['success', 'warning', 'error'] as const)
      vi.spyOn(Message, kind).mockReturnValue((): void => undefined);
  });
  afterEach(() => {
    cleanup();
    setRendererAccountOwner(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('keeps legacy policy strict and changes EN/ZH controls only after explicit saved readback', async () => {
    for (const locale of ['en-US', 'zh-CN'] as const) {
      const state = installPreferences();
      await renderWithI18n(<XScienceMemoryManager />, locale);
      await screen.findByTestId('memory-recovery-policy');
      const strict = locale === 'en-US' ? 'Require full captured request' : '必须保留完整捕获请求';
      const compact = locale === 'en-US' ? 'Use compact context if too large' : '过大时使用紧凑上下文';
      expect(control()).toHaveTextContent(strict);
      expect(state.writes).toHaveLength(0);
      await choosePolicy(compact);
      await waitFor(() => expect(state.writes).toHaveLength(1));
      expect(state.writes[0].body).toEqual({ enabled: true, forked_view_recovery: 'compact-on-oversize' });
      expect(control()).toHaveTextContent(strict);
      expect(control()).toHaveClass('arco-select-disabled');
      await act(async () => {
        state.acknowledge(0);
        await state.writes[0].reply.promise;
      });
      await waitFor(() => expect(control()).toHaveTextContent(compact));
      expect(state.writes).toHaveLength(1);
      cleanup();
    }
  });

  it('preserves acknowledged strict policy after rejection and retries only on the next user choice', async () => {
    const state = installPreferences();
    await renderWithI18n(<XScienceMemoryManager />, 'en-US');
    await screen.findByTestId('memory-recovery-policy');
    await choosePolicy('Use compact context if too large');
    await act(async () => {
      state.writes[0].reply.resolve(Response.json({ error: 'synthetic rejection' }, { status: 503 }));
      await state.writes[0].reply.promise;
    });
    await waitFor(() => expect(Message.error).toHaveBeenCalledOnce());
    expect(control()).toHaveTextContent('Require full captured request');
    expect(state.writes).toHaveLength(1);
    await choosePolicy('Use compact context if too large');
    await waitFor(() => expect(state.writes).toHaveLength(2));
    await act(async () => {
      state.acknowledge(1);
      await state.writes[1].reply.promise;
    });
    await waitFor(() => expect(control()).toHaveTextContent('Use compact context if too large'));
  });

  it('fences old-account replies and route cleanup from a new preference flight', async () => {
    const state = installPreferences();
    const onChanged = vi.fn();
    const rendered = await renderWithI18n(<XScienceMemoryManager onChanged={onChanged} />, 'en-US');
    await screen.findByTestId('memory-recovery-policy');
    await choosePolicy('Use compact context if too large');
    expect(state.writes).toHaveLength(1);
    await act(async () => {
      setRendererAccountOwner('recovery-owner-b');
    });
    await waitFor(() => expect(control()).not.toHaveClass('arco-select-disabled'));
    expect(state.writes[0].signal?.aborted).toBe(true);
    await choosePolicy('Use compact context if too large');
    await waitFor(() => expect(state.writes).toHaveLength(2));
    const bReads = state.reads.filter((value) => value.startsWith('recovery-owner-b:')).length;
    await act(async () => {
      state.acknowledge(0);
      await state.writes[0].reply.promise;
    });
    expect(control()).toHaveClass('arco-select-disabled');
    expect(control()).toHaveTextContent('Require full captured request');
    expect(state.reads.filter((value) => value.startsWith('recovery-owner-b:'))).toHaveLength(bReads);
    expect(onChanged).not.toHaveBeenCalled();
    rendered.unmount();
    expect(state.writes[1].signal?.aborted).toBe(true);
    await act(async () => {
      state.acknowledge(1);
      await state.writes[1].reply.promise;
    });
    expect(onChanged).not.toHaveBeenCalled();
    expect(Message.success).not.toHaveBeenCalled();
    expect(Message.error).not.toHaveBeenCalled();
  });
});
