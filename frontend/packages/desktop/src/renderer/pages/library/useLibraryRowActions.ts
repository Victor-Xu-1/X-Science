import { useRef } from 'react';
import type { LiteratureCommand, LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import type { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

/** Admission projection only. Page and its original durable controller own writes; its existing detail route owns opens. */
export function useLibraryRowActions(input: {
  account: string;
  readKey: string;
  ready: boolean;
  blocked: boolean;
  items: readonly LiteratureItem[];
  controller: LiteratureMutationController;
  openItem: (itemId: string) => void;
  submit: (command: LiteratureCommand) => Promise<LiteratureReceipt>;
}) {
  const owner = useRef({ account: input.account, key: input.readKey });
  if (owner.current.account !== input.account || owner.current.key !== input.readKey)
    owner.current = { account: input.account, key: input.readKey };
  const visit = owner.current;
  const state = useRef(input);
  state.current = input;
  const admit = (item: LiteratureItem) => {
    const current = state.current;
    const snapshot = current.controller.getSnapshot();
    const fresh = current.items.find((value) => value.id === item.id);
    if (
      owner.current !== visit ||
      current.account !== getRendererAccountScopeToken() ||
      !current.ready ||
      current.blocked ||
      snapshot.mutating ||
      snapshot.pending ||
      snapshot.error === 'storage' ||
      !fresh ||
      fresh.metadata_revision !== item.metadata_revision ||
      fresh.deleted !== item.deleted
    )
      return;
    return { current, fresh };
  };
  const change = (item: LiteratureItem, deleted: boolean): void => {
    const admitted = admit(item);
    if (!admitted || admitted.fresh.deleted === deleted) return;
    const { current, fresh } = admitted;
    // This exact intent is captured by the original outbox before network I/O.
    // Page records the rejection and its existing Retry owns uncertain results.
    void current
      .submit({
        kind: 'edit',
        operation_id: crypto.randomUUID(),
        item_id: fresh.id,
        expected_revision: fresh.metadata_revision,
        deleted,
      })
      .catch((): undefined => undefined);
  };
  const open = (item: LiteratureItem): void => {
    const admitted = admit(item);
    if (admitted && !admitted.fresh.deleted) admitted.current.openItem(admitted.fresh.id);
  };
  return { change, open };
}
