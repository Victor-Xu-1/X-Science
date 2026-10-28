import React, { useEffect, useState } from 'react';
import '@testing-library/jest-dom/vitest';
import { createRoot } from 'react-dom/client';
import { act, fireEvent, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRendererRealtimeRuntime } from '@/common/adapter/realtimeRenderer';
import type { RealtimeSocket } from '@/common/adapter/realtimeClient';
import { installReactRootLifecycle } from '@/renderer/utils/ui/reactRootLifecycle';
import { installWebChunkRecovery } from '@/renderer/services/webChunkRecovery';

class Socket extends EventTarget implements RealtimeSocket {
  readyState = 1;
  readonly sent: string[] = [];
  send(value: string) {
    this.sent.push(value);
  }
  close() {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.dispatchEvent(new Event('close'));
  }
  receive(value: unknown) {
    this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(value) }));
  }
}

const pageEvent = (type: 'pagehide' | 'pageshow', persisted: boolean): Event => {
  const event = new Event(type);
  Object.defineProperty(event, 'persisted', { value: persisted });
  return event;
};

describe('renderer cached-page ownership', () => {
  afterEach(() => vi.restoreAllMocks());

  it('retains the same React draft and runtime, resumes cursor delivery, and keeps chunk recovery until permanent exit', async () => {
    const target = new EventTarget();
    Object.assign(target, { location: { protocol: 'http:', host: 'localhost', origin: 'http://localhost' } });
    const sockets: Socket[] = [];
    const urls: string[] = [];
    const runtime = createRendererRealtimeRuntime({
      window: target as EventTarget & { location: Location },
      document: new EventTarget(),
      digestOwner: async () => 'a'.repeat(64),
      getStorage: () => undefined,
      isOnline: () => true,
      createSocket: (url) => {
        urls.push(url);
        const socket = new Socket();
        sockets.push(socket);
        return socket;
      },
    });
    const disposeRuntime = vi.spyOn(runtime, 'dispose');
    const reconnected = vi.fn();
    runtime.subscribeReconnected(reconnected);
    let mounts = 0;
    const Workspace = () => {
      const [draft, setDraft] = useState('Initial draft');
      const [received, setReceived] = useState(0);
      useEffect(() => {
        mounts++;
        void runtime.setIdentity('fixture-owner');
        const release = runtime.subscribe('durable', 'text_chunk', () => setReceived((value) => value + 1));
        return () => {
          release();
          runtime.dispose();
        };
      }, []);
      return (
        <>
          <input aria-label='Unsaved draft' value={draft} onChange={(event) => setDraft(event.target.value)} />
          <output aria-label='Delivered events'>{received}</output>
        </>
      );
    };
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    const unmount = vi.fn(() => root.unmount());
    const reload = vi.fn();
    const marker = new Map<string, string>();
    const disposeChunk = vi.fn(
      installWebChunkRecovery({
        target: target as Window,
        storage: {
          getItem: (key) => marker.get(key) ?? null,
          setItem: (key, value) => {
            marker.set(key, value);
          },
          removeItem: (key) => {
            marker.delete(key);
          },
        },
        href: () => 'http://localhost/#/conversation/fixture',
        reload,
        schedule: (callback) => callback(),
      })
    );
    const lifecycle = installReactRootLifecycle({ unmount, disposeResources: disposeChunk, target: target as Window });
    const durable = (sequence: number) => ({
      type: 'text_chunk',
      _event_id: `fixture-${sequence}`,
      _sequence: sequence,
      _kind: 'fanout',
    });
    try {
      await act(async () => root.render(<Workspace />));
      act(() => sockets[0].receive({ type: 'connected', cursor: 0 }));
      act(() => sockets[0].receive(durable(1)));
      const draft = within(host).getByLabelText('Unsaved draft');
      fireEvent.change(draft, { target: { value: 'Retain this unfinished input' } });
      act(() => target.dispatchEvent(pageEvent('pagehide', true)));
      expect(unmount).not.toHaveBeenCalled();
      expect(disposeChunk).not.toHaveBeenCalled();
      expect(disposeRuntime).not.toHaveBeenCalled();

      sockets[0].readyState = 3; // No close event while the document was cached.
      act(() => target.dispatchEvent(pageEvent('pageshow', true)));
      expect(sockets).toHaveLength(2);
      expect(urls[1]).toContain('after_sequence=1');
      act(() => sockets[1].receive({ type: 'connected', cursor: 1 }));
      act(() => sockets[1].receive(durable(2)));
      act(() => sockets[1].receive(durable(2)));
      expect(reconnected).toHaveBeenCalledOnce();
      expect(within(host).getByLabelText('Delivered events')).toHaveTextContent('2');
      expect(within(host).getByLabelText('Unsaved draft')).toBe(draft);
      expect(draft).toHaveValue('Retain this unfinished input');
      expect(mounts).toBe(1);
      target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }));
      expect(reload).toHaveBeenCalledOnce();

      act(() => target.dispatchEvent(pageEvent('pagehide', false)));
      expect(unmount).toHaveBeenCalledOnce();
      expect(disposeChunk).toHaveBeenCalledOnce();
      expect(disposeRuntime).toHaveBeenCalledOnce();
      expect(host).toBeEmptyDOMElement();
      marker.clear();
      target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }));
      target.dispatchEvent(pageEvent('pageshow', true));
      expect(reload).toHaveBeenCalledOnce();
      expect(sockets).toHaveLength(2);
    } finally {
      act(() => lifecycle.dispose());
      runtime.dispose();
      host.remove();
    }
  });
});
