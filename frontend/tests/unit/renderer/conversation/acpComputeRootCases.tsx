import React from 'react';
import { act, waitFor } from '@testing-library/react';
import { expect, it, type Mock } from 'vitest';
import type { TChatConversation } from '@/common/config/storage';
import AcpSendBox from '@/renderer/pages/conversation/platforms/acp/AcpSendBox';
import type { UseAcpMessageReturn } from '@/renderer/pages/conversation/platforms/acp/useAcpMessage';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

type Entry = {
  key: string;
  meta?: string;
  submenu?: { options?: Array<{ key: string; active?: boolean }>; onSelect?: (key: string) => void };
};
const projection = (id = 'compute-child', incarnation = 'compute-inc', root: string | undefined = 'compute-root') =>
  ({
    id,
    type: 'acp',
    name: 'Synthetic compute child',
    extra: { backend: 'xscience', frame_incarnation_id: incarnation, root_frame_id: root },
  }) as TChatConversation;
export { projection as computeRootProjection };
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
};

/** Uses the existing real composer fixture; no task, compute host or API is started. */
export function registerComputeRootCases(args: {
  render: (element: React.ReactElement) => Promise<{ rerender: (element: React.ReactElement) => void }>;
  messageState: () => UseAcpMessageReturn;
  mobile: { current: boolean };
  menu: { current: Record<string, unknown> | null };
  entries: { current: Entry[] };
  providers: Mock;
  selection: Mock;
  write: Mock;
}) {
  const prepare = (mobile = false) => {
    args.mobile.current = mobile;
    setRendererAccountOwner('compute-owner');
    args.providers.mockResolvedValue([{ name: 'ssh:fixture', displayName: 'Compute fixture', checked: true }]);
    args.selection.mockResolvedValue([]);
  };
  const node = (
    conversation: TChatConversation | undefined = projection(),
    id = 'compute-child',
    owner = 'compute-owner'
  ) => (
    <AcpSendBox
      conversation_id={id}
      ownerId={owner}
      backend='xscience'
      initialConversation={conversation}
      messageState={args.messageState()}
    />
  );
  const entry = () => args.entries.current.find((item) => item.key === 'session-compute');

  it('C394 root bridge supplies the current child projection root to the desktop menu', async () => {
    prepare();
    await args.render(node());
    expect(args.menu.current?.rootFrameId).toBe('compute-root');
  });

  it('C394 root bridge reads and writes the projected root on mobile, never the child route', async () => {
    prepare(true);
    await args.render(node());
    await waitFor(() => expect(entry()).toBeDefined());
    expect(args.selection).toHaveBeenCalledWith('compute-root');
    await act(async () => {
      entry()?.submenu?.onSelect?.('ssh:fixture');
    });
    expect(args.write).toHaveBeenCalledWith('compute-root', 'ssh:fixture', true);
    expect(args.selection).not.toHaveBeenCalledWith('compute-child');
  });

  it.each(['missing-root', 'missing-incarnation', 'other-frame', 'missing-owner'])(
    'C394 root bridge keeps %s unknown without a guessed mobile request',
    async (kind) => {
      prepare();
      const current = projection(
        kind === 'other-frame' ? 'different-frame' : 'compute-child',
        kind === 'missing-incarnation' ? '' : 'compute-inc'
      );
      if (kind === 'missing-root' && current.type === 'acp') delete current.extra.root_frame_id;
      const view = await args.render(node(current, 'compute-child', kind === 'missing-owner' ? '' : 'compute-owner'));
      expect(args.menu.current?.rootFrameId).toBeNull();
      args.mobile.current = true;
      view.rerender(node(current, 'compute-child', kind === 'missing-owner' ? '' : 'compute-owner'));
      expect(args.selection).not.toHaveBeenCalled();
      expect(entry()).toBeUndefined();
      expect(args.write).not.toHaveBeenCalled();
    }
  );

  it.each(['incarnation', 'account'])(
    'C394 root bridge isolates a late mobile read after %s replacement',
    async (kind) => {
      prepare(true);
      const old = deferred<string[]>();
      args.selection.mockReturnValueOnce(old.promise);
      const view = await args.render(node());
      await waitFor(() => expect(args.selection).toHaveBeenCalledTimes(1));
      if (kind === 'account') setRendererAccountOwner('next-owner');
      view.rerender(
        node(
          projection('compute-child', kind === 'incarnation' ? 'new-inc' : 'compute-inc'),
          'compute-child',
          kind === 'account' ? 'next-owner' : 'compute-owner'
        )
      );
      await waitFor(() => expect(args.selection).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(entry()?.meta).toBe('0'));
      await act(async () => {
        old.resolve(['ssh:fixture']);
        await old.promise;
      });
      expect(entry()?.meta).toBe('0');
    }
  );

  it('C394 root bridge fences a late mobile write and its retained handler across incarnation changes', async () => {
    prepare(true);
    const old = deferred<void>();
    args.write.mockReturnValueOnce(old.promise);
    const view = await args.render(node());
    await waitFor(() => expect(entry()).toBeDefined());
    const oldSelect = entry()?.submenu?.onSelect;
    await act(async () => {
      oldSelect?.('ssh:fixture');
      await Promise.resolve();
    });
    view.rerender(node(projection('compute-child', 'new-inc')));
    await waitFor(() => expect(args.selection).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(entry()?.meta).toBe('0'));
    await act(async () => {
      old.resolve();
      await old.promise;
    });
    expect(entry()?.meta).toBe('0');
    await act(async () => {
      oldSelect?.('ssh:fixture');
    });
    expect(args.write).toHaveBeenCalledTimes(1);
  });
}
