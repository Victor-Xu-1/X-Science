import { useEffect, useRef, useState } from 'react';
import type { LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { literatureValueIdentity } from '@/common/adapter/literatureValueIdentity';
import {
  literatureSelectionScopeKey,
  type LiteratureRestoreTarget,
  type LiteratureScopeInput,
} from '@/common/adapter/literatureSelection';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type {
  LiteratureMutationController,
  LiteratureMutationView,
} from '@/renderer/services/runtime/LiteratureMutationController';
import { loadLiteratureRestorePreview } from '@/renderer/services/runtime/literatureSelection';
import {
  isLiteratureRestoreBulk,
  literatureRestoreBulkCodec,
  type LiteratureRestoreBulkIntent,
} from '@/renderer/services/runtime/literatureRestoreBulkIntent';
import { BulkPausedError } from '@/renderer/services/runtime/literatureBulkFlow';
import { OriginalPendingConflict } from '@/renderer/services/runtime/literatureUploadIntent';
import { resolveLibrarySelection, type LibrarySelectionState } from './librarySelectionState';

type Visit = { key: string; alive: boolean };
type Work = { visit: Visit; abort: AbortController; selection?: object; operationId?: string };
type Preview = { work: Work; items: readonly LiteratureRestoreTarget[] };
type Message = 'previewFailed' | 'selectionChanged' | 'emptyResolved' | 'operationFailed';
type Props = {
  account: string;
  scope: LiteratureScopeInput;
  route: string;
  selection: LibrarySelectionState;
  blocked: boolean;
  controller: LiteratureMutationController;
  view: LiteratureMutationView;
  setRemaining(ids: readonly string[]): void;
};
const remaining = (intent: LiteratureRestoreBulkIntent) =>
  intent.commands.slice(intent.confirmed_items).map((command) => command.item_id);

/** Preview is ephemeral. All accepted intent, requests and ACKs remain with the one durable controller. */
export function useLibraryRestoreSelection({
  account,
  scope,
  route,
  selection,
  blocked,
  controller,
  view,
  setRemaining,
}: Props) {
  const scopeKey = literatureSelectionScopeKey(scope);
  const key = JSON.stringify([account, scopeKey, route]);
  const selectionKey = JSON.stringify(selection);
  const selected = useRef({ key: selectionKey });
  if (selected.current.key !== selectionKey) selected.current = { key: selectionKey };
  const selectedVisit = selected.current;
  const owner = useRef<Visit>({ key, alive: true });
  if (owner.current.key !== key) owner.current = { key, alive: true };
  const visit = owner.current;
  const work = useRef<Work | undefined>(undefined);
  const origin = useRef<{ visit: Visit; intent: LiteratureRestoreBulkIntent } | undefined>(undefined);
  const selectionPlan = useRef<{ visit: Visit; operationId: string } | undefined>(undefined);
  const [state, setState] = useState<{
    visit: Visit;
    selection?: object;
    loading?: boolean;
    preview?: Preview;
    message?: Message;
  }>({ visit });
  const visible =
    state.visit === visit && (state.selection === undefined || state.selection === selectedVisit) ? state : undefined;
  const current = (token = visit) =>
    owner.current === token &&
    token.alive &&
    account === getRendererAccountScopeToken() &&
    controller.isCurrentAccount();
  const available = () => {
    const actual = controller.getSnapshot();
    return (
      current() &&
      scope.lifecycle === 'deleted' &&
      !blocked &&
      !work.current &&
      !actual.mutating &&
      !actual.pending &&
      actual.error !== 'storage'
    );
  };
  const cancel = () => {
    const active = work.current;
    if (!active || active.operationId || !current(active.visit)) return;
    active.abort.abort();
    work.current = undefined;
    setState({ visit });
  };
  useEffect(() => {
    visit.alive = true;
    return () => {
      visit.alive = false;
      const active = work.current;
      if (active?.visit !== visit) return;
      active.abort.abort();
      if (active.operationId) void controller.pauseRestoreBulk(active.operationId).catch((): undefined => undefined);
      if (work.current === active) work.current = undefined;
    };
  }, [visit, controller]);
  useEffect(
    () => () => {
      const active = work.current;
      if (active && !active.operationId && active.selection === selectedVisit) {
        active.abort.abort();
        work.current = undefined;
      }
    },
    [selectedVisit]
  );
  useEffect(() => {
    const pending = controller.getSnapshot().pending;
    if (current() && isLiteratureRestoreBulk(pending) && pending.scope_key === scopeKey) {
      selectionPlan.current = { visit, operationId: pending.operation_id };
      setRemaining(remaining(pending));
    }
    // Rehydrate only for a new account/controller; ordinary visits keep the existing clear-selection contract.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [account, controller]);
  const preview = async () => {
    if (!available() || (!selection.allMatchingSelected && !selection.selectedIds.length)) return;
    const active: Work = { visit, abort: new AbortController(), selection: selectedVisit };
    work.current = active;
    setState({ visit, selection: selectedVisit, loading: true });
    try {
      const complete = await loadLiteratureRestorePreview(scope, active.abort.signal);
      if (
        !current(active.visit) ||
        active.selection !== selected.current ||
        active.abort.signal.aborted ||
        work.current !== active
      )
        return;
      const ids = new Set(
        resolveLibrarySelection(
          selection,
          complete.items.map((item) => item.id)
        )
      );
      const items = complete.items.filter((item) => ids.has(item.id));
      if (
        items.length !== ids.size ||
        selection.excludedMatchingIds.some((id) => !complete.items.some((item) => item.id === id))
      ) {
        work.current = undefined;
        setState({ visit, message: 'selectionChanged' });
      } else if (!items.length) {
        work.current = undefined;
        setState({ visit, message: 'emptyResolved' });
      } else setState({ visit, selection: selectedVisit, preview: { work: active, items: Object.freeze(items) } });
    } catch {
      if (current(active.visit) && !active.abort.signal.aborted && work.current === active) {
        work.current = undefined;
        setState({ visit, message: 'previewFailed' });
      }
    }
  };
  const execute = async (active: Work, operation: () => Promise<unknown>, capture = false) => {
    work.current = active;
    try {
      const promise = operation();
      const intent = controller.getSnapshot().action;
      if (capture && isLiteratureRestoreBulk(intent)) {
        active.operationId = intent.operation_id;
        origin.current = { visit: active.visit, intent };
        selectionPlan.current = { visit: active.visit, operationId: intent.operation_id };
      }
      await promise;
    } catch (error) {
      if (
        current(active.visit) &&
        !(error instanceof BulkPausedError) &&
        !(error instanceof OriginalPendingConflict) &&
        !(error instanceof DOMException && error.name === 'AbortError')
      )
        setState({ visit: active.visit, message: 'operationFailed' });
    } finally {
      if (
        current(active.visit) &&
        selectionPlan.current?.visit === active.visit &&
        selectionPlan.current.operationId === active.operationId
      ) {
        const actual = controller.getSnapshot();
        const intent = isLiteratureRestoreBulk(actual.pending)
          ? actual.pending
          : isLiteratureRestoreBulk(actual.action)
            ? actual.action
            : undefined;
        if (intent?.operation_id === active.operationId) setRemaining(remaining(intent));
      }
      if (work.current === active) work.current = undefined;
    }
  };
  const confirm = async () => {
    const approved = visible?.preview;
    if (
      !approved ||
      !current(approved.work.visit) ||
      approved.work.selection !== selected.current ||
      approved.work.abort.signal.aborted ||
      work.current !== approved.work
    )
      return;
    const actual = controller.getSnapshot();
    if (blocked || actual.pending || actual.mutating || actual.error === 'storage') return;
    setState({ visit });
    await execute(
      approved.work,
      () =>
        controller.startRestoreBulk(
          approved.items,
          scopeKey,
          () => current(approved.work.visit) && !approved.work.abort.signal.aborted
        ),
      true
    );
  };
  const resume = async (stopAfterCurrent = false) => {
    if (!current() || work.current || controller.getSnapshot().mutating) return;
    const pending = controller.getSnapshot().pending;
    if (!isLiteratureRestoreBulk(pending)) {
      controller.restore();
      return;
    }
    const active: Work = { visit, abort: new AbortController(), operationId: pending.operation_id };
    await execute(active, () =>
      controller.resumeRestoreBulk(stopAfterCurrent, () => current(active.visit) && !active.abort.signal.aborted)
    );
  };
  const pause = async () => {
    const pending = controller.getSnapshot().pending;
    if (current() && isLiteratureRestoreBulk(pending))
      await controller.pauseRestoreBulk(pending.operation_id).catch(() => {
        if (current()) setState({ visit, message: 'operationFailed' });
      });
  };
  const discard = async () => {
    const pending = controller.getSnapshot().pending;
    if (!current() || !isLiteratureRestoreBulk(pending) || controller.getSnapshot().mutating) return;
    try {
      await controller.discardRestoreBulk(pending.operation_id);
      if (
        current() &&
        selectionPlan.current?.visit === visit &&
        selectionPlan.current.operationId === pending.operation_id
      )
        setRemaining([]);
    } catch {
      if (current()) setState({ visit, message: 'operationFailed' });
    }
  };
  return {
    identityKey: key,
    disabled: !available(),
    loading: Boolean(visible?.loading),
    count: visible?.preview?.items.length,
    message: visible?.message,
    preview,
    confirm,
    cancel,
    resume,
    pause,
    discard,
    canFocus: () => current(),
    owns: (operationId: string) => origin.current?.intent.operation_id === operationId && current(origin.current.visit),
    isReceipt: (receipt: LiteratureReceipt) =>
      origin.current?.intent.commands.some((command) => command.operation_id === receipt.operation_id) === true,
    ownedNotice: isLiteratureRestoreBulk(view.pending) || isLiteratureRestoreBulk(view.action),
  };
}
export type LibraryRestoreSelectionModel = ReturnType<typeof useLibraryRestoreSelection>;

const restoreSummaryStatus = ({
  pending: _pending,
  action: _action,
  receipt: _receipt,
  ...fields
}: LiteratureMutationView) => fields;

/** Page admission/settlement stays live; the small progress consumer reads every actual ACK. */
export function projectLibraryRestoreSummary(
  next: LiteratureMutationView,
  previous: LiteratureMutationView
): LiteratureMutationView {
  if (
    !next.mutating ||
    !previous.mutating ||
    !isLiteratureRestoreBulk(next.pending) ||
    !isLiteratureRestoreBulk(previous.pending) ||
    !isLiteratureRestoreBulk(next.action) ||
    !isLiteratureRestoreBulk(previous.action)
  )
    return next;
  const codec = literatureRestoreBulkCodec;
  if (
    !codec.sameOrigin(next.pending, previous.pending) ||
    !codec.sameOrigin(next.pending, next.action) ||
    !codec.sameOrigin(previous.pending, previous.action) ||
    next.pending.paused !== previous.pending.paused ||
    next.action.paused !== previous.action.paused
  )
    return next;
  if (literatureValueIdentity(restoreSummaryStatus(next)) !== literatureValueIdentity(restoreSummaryStatus(previous)))
    return next;
  if (next.receipt !== previous.receipt) {
    const command = next.pending.commands.find((candidate) => candidate.operation_id === next.receipt?.operation_id);
    if (!command) return next;
    try {
      codec.receipt(next.receipt, command);
    } catch {
      return next;
    }
  }
  return previous;
}
