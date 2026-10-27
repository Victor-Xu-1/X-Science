import { useCallback, useEffect, useRef, useState } from 'react';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  decodeLiteraturePublicationReceipt,
  type LiteraturePublicationCheck,
  type LiteraturePublicationCommand,
  type LiteraturePublicationIssue,
  type LiteraturePublicationSnapshot,
} from '@/common/adapter/literaturePublicationCheck';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { readLiteraturePublicationCheck } from '@/renderer/services/runtime/literaturePublicationCheck';

type Read = { abort: AbortController; version: number };
type Owner = {
  key: string;
  account: string;
  itemId: string;
  revision: number;
  controller: ReturnType<typeof useLiteratureMutation>['controller'];
  active: boolean;
  version: number;
  read?: Read;
  call?: object;
  operationId?: string;
  ignoredOperationId?: string;
  receipt?: unknown;
};
type View = {
  owner: Owner;
  loading: boolean;
  writing: boolean;
  loaded: boolean;
  snapshot?: LiteraturePublicationSnapshot;
  issue?: LiteraturePublicationIssue;
  failure: 'read' | 'write' | null;
  retry: 'get' | 'check';
};
const empty = (owner: Owner): View => ({
  owner,
  loading: false,
  writing: false,
  loaded: false,
  failure: null,
  retry: 'get',
});
const matches = (value: unknown, item: LiteratureItem): value is LiteraturePublicationCommand => {
  if (!value || typeof value !== 'object') return false;
  const command = value as Partial<LiteraturePublicationCommand>;
  return (
    command.kind === 'check-publication' &&
    command.item_id === item.id &&
    command.expected_metadata_revision === item.metadata_revision &&
    typeof command.operation_id === 'string'
  );
};

/** Detail-owned reads and presentation only. Every write remains in the existing Library slot. */
export function useLibraryPublicationCheck(account: string, item: LiteratureItem) {
  const { controller, view } = useLiteratureMutation();
  const rawDoi = item.metadata.doi;
  const key = JSON.stringify([account, item.id, item.metadata_revision, rawDoi, item.deleted]);
  const ownerRef = useRef<Owner>({
    key,
    account,
    itemId: item.id,
    revision: item.metadata_revision,
    controller,
    active: true,
    version: 0,
  });
  if (ownerRef.current.key !== key || ownerRef.current.controller !== controller) {
    const previous = ownerRef.current;
    previous.active = false;
    ownerRef.current = {
      key,
      account,
      itemId: item.id,
      revision: item.metadata_revision,
      controller,
      active: true,
      version: 0,
      // A same-revision DOI replacement must not adopt an operation observed for the old raw value.
      ignoredOperationId:
        previous.key !== key &&
        previous.account === account &&
        previous.itemId === item.id &&
        previous.revision === item.metadata_revision
          ? (previous.operationId ?? (matches(view.action, item) ? view.action.operation_id : undefined))
          : undefined,
    };
  }
  const owner = ownerRef.current;
  const [state, setState] = useState(() => empty(owner));
  const local = state.owner === owner ? state : empty(owner);
  const current = useCallback(
    () => ownerRef.current === owner && owner.active && account === getRendererAccountScopeToken(),
    [owner, account]
  );
  const update = useCallback(
    (change: (previous: View) => View) => {
      if (!current()) return;
      setState((previous) => (current() ? change(previous.owner === owner ? previous : empty(owner)) : previous));
    },
    [current, owner]
  );
  const hasDoi = typeof rawDoi === 'string' && Boolean(rawDoi.trim()); // Presence only; the backend normalizes DOI.
  const available = hasDoi && !item.deleted;
  const pending =
    matches(view.pending, item) && view.pending.operation_id !== owner.ignoredOperationId ? view.pending : undefined;
  const action =
    matches(view.action, item) && view.action.operation_id !== owner.ignoredOperationId ? view.action : undefined;
  const settledAction =
    action &&
    view.receipt?.schema === 'x-science.literature-publication-receipt.v1' &&
    view.receipt.operation_id === action.operation_id &&
    view.receipt.result.item_id === item.id &&
    view.receipt.result.metadata_revision === item.metadata_revision;
  if (pending) owner.operationId = pending.operation_id;
  else if (action && (view.mutating || settledAction)) owner.operationId = action.operation_id;

  const publish = useCallback(
    (result: LiteraturePublicationCheck, mode: 'get' | 'check') => {
      if (
        result.item_id !== owner.itemId ||
        result.metadata_revision !== owner.revision ||
        (result.snapshot && result.snapshot.doi !== result.doi)
      )
        throw new Error('Publication result identity changed');
      update((previous) => {
        if (result.issue === 'doi-required')
          return { ...previous, snapshot: undefined, loaded: false, issue: result.issue, failure: null, retry: 'get' };
        const ownsIssue = mode === 'check' || previous.retry !== 'check';
        const incoming = result.snapshot;
        const snapshot = incoming
          ? previous.snapshot && Date.parse(previous.snapshot.checked_at) > Date.parse(incoming.checked_at)
            ? previous.snapshot
            : incoming
          : !result.issue && ownsIssue
            ? undefined
            : previous.snapshot;
        return {
          ...previous,
          snapshot,
          loaded: previous.loaded || !result.issue,
          ...(ownsIssue ? { issue: result.issue, failure: null, retry: result.issue ? mode : 'get' } : {}),
        };
      });
    },
    [owner, update]
  );
  const invalidateRead = useCallback(() => {
    ++owner.version;
    owner.read?.abort.abort();
    owner.read = undefined;
    update((previous) => ({ ...previous, loading: false }));
  }, [owner, update]);
  const read = useCallback(() => {
    if (!current() || !available) return;
    owner.read?.abort.abort();
    const request: Read = { abort: new AbortController(), version: owner.version };
    owner.read = request;
    update((previous) => ({
      ...previous,
      loading: true,
      ...(previous.retry === 'get' ? { issue: undefined, failure: null } : {}),
    }));
    const active = () =>
      current() && owner.read === request && !request.abort.signal.aborted && request.version === owner.version;
    void readLiteraturePublicationCheck(owner.itemId, owner.revision, request.abort.signal)
      .then(
        (result) => {
          if (!active()) return;
          try {
            publish(result, 'get');
          } catch {
            update((previous) => ({ ...previous, issue: 'response-invalid', failure: 'read', retry: 'get' }));
          }
        },
        () => {
          if (active())
            update((previous) =>
              previous.retry === 'check' ? previous : { ...previous, failure: 'read', issue: undefined, retry: 'get' }
            );
        }
      )
      .finally(() => {
        if (owner.read === request) {
          owner.read = undefined;
          update((previous) => ({ ...previous, loading: false }));
        }
      });
  }, [current, available, owner, update, publish]);
  useEffect(() => {
    owner.active = true;
    read();
    return () => {
      owner.active = false;
      owner.read?.abort.abort();
      owner.read = undefined;
      // Closing the detail never cancels or clears the account's durable operation.
    };
  }, [owner, read]);

  const acceptReceipt = useCallback(
    (value: unknown, operationId: string) => {
      if (!current() || owner.receipt === value) return;
      const latest = controller.getSnapshot();
      const original = latest.pending ?? latest.action;
      if (!matches(original, item) || original.operation_id !== operationId) return;
      const receipt = decodeLiteraturePublicationReceipt(value, operationId);
      // A recovered provider failure may need the saved read to restore its older evidence.
      // publish keeps that read from clearing the manual failure or its retry ownership.
      if (!receipt.result.issue || receipt.result.issue === 'doi-required') invalidateRead();
      publish(receipt.result, 'check');
      owner.receipt = value;
    },
    [current, controller, item, invalidateRead, publish, owner]
  );
  useEffect(() => {
    const receipt = view.receipt;
    if (
      !receipt ||
      !action ||
      receipt.operation_id !== action.operation_id ||
      receipt.operation_id !== owner.operationId ||
      owner.receipt === receipt ||
      !current()
    )
      return;
    try {
      acceptReceipt(receipt, action.operation_id);
    } catch {
      update((previous) => ({ ...previous, issue: 'response-invalid', failure: 'write', retry: 'check' }));
    }
  }, [view.receipt, action, owner, current, acceptReceipt, update]);

  const otherPending = Boolean(view.pending && !pending);
  const busy = local.writing || Boolean(view.mutating && (pending || action));
  const blocked = otherPending || (view.mutating && !busy) || view.error === 'storage';
  const rejected = Boolean(action && !pending && view.error === 'rejected');
  const launch = (resume: boolean) => {
    if (!current() || !available || owner.call || busy || blocked || local.issue === 'doi-required') return;
    const latest = controller.getSnapshot();
    if (
      latest.mutating ||
      latest.error === 'storage' ||
      (resume ? !pending || latest.pending?.operation_id !== pending.operation_id : Boolean(latest.pending))
    )
      return;
    let operationId: string;
    try {
      operationId = resume ? pending!.operation_id : crypto.randomUUID();
    } catch {
      update((previous) => ({ ...previous, failure: 'write', retry: 'check' }));
      return;
    }
    const token = {};
    owner.call = token;
    invalidateRead();
    owner.operationId = operationId;
    update((previous) => ({ ...previous, writing: true, issue: undefined, failure: null, retry: 'check' }));
    const fail = () => {
      if (current() && owner.call === token) update((previous) => ({ ...previous, failure: 'write', retry: 'check' }));
    };
    const finish = () => {
      if (owner.call !== token) return;
      owner.call = undefined;
      update((previous) => ({ ...previous, writing: false }));
    };
    try {
      const promise = resume
        ? controller.retry()
        : controller.submit({
            kind: 'check-publication',
            operation_id: operationId,
            item_id: item.id,
            expected_metadata_revision: item.metadata_revision,
          });
      void promise
        .then((receipt) => {
          if (current() && owner.call === token) acceptReceipt(receipt, operationId);
        }, fail)
        .then(finish, () => {
          fail();
          finish();
        });
    } catch {
      fail();
      finish();
    }
  };
  return {
    current: current(),
    available,
    hasDoi,
    deleted: item.deleted,
    snapshot: local.snapshot,
    loaded: local.loaded,
    issue: local.issue,
    loading: local.loading,
    busy,
    pending,
    otherPending,
    rejected,
    storageFailed: view.error === 'storage',
    failure: local.failure,
    canCheck:
      current() && available && !busy && !blocked && !local.loading && !pending && local.issue !== 'doi-required',
    canResume: current() && available && !busy && !blocked && Boolean(pending),
    retryMode: local.retry,
    read,
    check: () => launch(false),
    resume: () => launch(true),
    restore: () => {
      if (current() && !owner.call && !view.mutating) controller.restore();
    },
  };
}
