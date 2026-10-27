import { useEffect, useMemo, useRef, useState } from 'react';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { literatureSelectionScopeKey, type LiteratureScopeInput } from '@/common/adapter/literatureSelection';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type {
  LiteratureMutationController,
  LiteratureMutationView,
} from '@/renderer/services/runtime/LiteratureMutationController';
import { loadCompleteLiteratureSelection } from '@/renderer/services/runtime/literatureSelection';
import {
  isLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import { BulkPausedError } from '@/renderer/services/runtime/literatureReadingBulkFlow';
import { OriginalPendingConflict } from '@/renderer/services/runtime/literatureUploadIntent';
import {
  EMPTY_LIBRARY_SELECTION,
  allMatchingLibrarySelection,
  explicitLibrarySelection,
  libraryKnownRowRange,
  resolveLibrarySelection,
  setLibrarySelectionItems,
  toggleLibraryPageSelection,
  toggleLibrarySelection,
  type LibrarySelectionState,
} from './librarySelectionState';

type Visit = { key: string };
type Work = { visit: Visit; abort: AbortController; operationId?: string };
const remaining = (intent: LiteratureReadingBulkIntent) =>
  intent.batches.slice(intent.confirmed_batches).flatMap((batch) => batch.item_ids);

/** Selection is view-owned; accepted batches and their unsent suffix live in the original durable slot. */
export function useLibraryBulkReadingStatus({
  account,
  scope,
  cursor,
  rows,
  readReady,
  blocked,
  controller,
  view,
}: {
  account: string;
  scope: LiteratureScopeInput;
  cursor: string;
  rows: readonly LiteratureItem[];
  readReady: boolean;
  blocked: boolean;
  controller: LiteratureMutationController;
  view: LiteratureMutationView;
}) {
  const scopeKey = literatureSelectionScopeKey(scope);
  const visitKey = JSON.stringify([account, scopeKey, cursor]);
  const owner = useRef<Visit>({ key: visitKey });
  if (owner.current.key !== visitKey) owner.current = { key: visitKey };
  const visit = owner.current;
  const [selected, setSelected] = useState({ visit, value: EMPTY_LIBRARY_SELECTION });
  const [activity, setActivity] = useState<{
    visit: Visit;
    resolving: boolean;
    message?: 'resolveFailed' | 'emptyResolved' | 'operationFailed';
  }>({ visit, resolving: false });
  const work = useRef<Work | undefined>(undefined);
  const anchor = useRef<{ visit: Visit; item: string } | undefined>(undefined);
  const selection = selected.visit === visit ? selected.value : EMPTY_LIBRARY_SELECTION;
  const resolving = activity.visit === visit && activity.resolving;
  const message = activity.visit === visit ? activity.message : undefined;
  const rowIds = useMemo(() => rows.map((item) => item.id), [rows]);
  const chosen = useMemo(
    () => new Set(selection.allMatchingSelected ? selection.excludedMatchingIds : selection.selectedIds),
    [selection]
  );
  const isSelected = (id: string) => (selection.allMatchingSelected ? !chosen.has(id) : chosen.has(id));
  const pageCount = rowIds.filter(isSelected).length;
  const current = (token = visit) =>
    owner.current === token && account === getRendererAccountScopeToken() && controller.isCurrentAccount();
  const editable = () => {
    const actual = controller.getSnapshot();
    return (
      current() &&
      readReady &&
      !blocked &&
      !work.current &&
      !actual.mutating &&
      !actual.pending &&
      actual.error !== 'storage'
    );
  };
  const update = (change: (value: LibrarySelectionState) => LibrarySelectionState) => {
    if (!editable()) return;
    setSelected((previous) => ({
      visit,
      value: change(previous.visit === visit ? previous.value : EMPTY_LIBRARY_SELECTION),
    }));
  };

  useEffect(() => {
    const token = visit;
    return () => {
      const active = work.current;
      if (!active || active.visit !== token) return;
      active.abort.abort();
      if (active.operationId)
        void controller.pauseReadingStatusBulk(active.operationId).catch((): undefined => undefined);
      if (work.current === active) work.current = undefined;
    };
  }, [visit, controller]);
  useEffect(() => {
    // A refresh may restore the original accepted plan, including the unsent suffix.
    const pending = controller.getSnapshot().pending;
    if (current() && isLiteratureReadingBulk(pending) && pending.scope_key === scopeKey)
      setSelected({ visit, value: explicitLibrarySelection(remaining(pending)) });
    // Ordinary page/filter visits clear ephemeral selection; only account/owner restoration runs here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [account, controller]);

  const settleSelection = (active: Work) => {
    if (!current(active.visit)) return;
    const actual = controller.getSnapshot();
    const intent = isLiteratureReadingBulk(actual.pending)
      ? actual.pending
      : isLiteratureReadingBulk(actual.action)
        ? actual.action
        : undefined;
    if (!intent || intent.operation_id !== active.operationId) {
      if (active.operationId) setSelected({ visit: active.visit, value: EMPTY_LIBRARY_SELECTION });
      return;
    }
    setSelected({ visit: active.visit, value: explicitLibrarySelection(remaining(intent)) });
  };
  const execute = async (active: Work, operation: () => Promise<unknown>) => {
    work.current = active;
    try {
      const before = controller.getSnapshot().action;
      const promise = operation();
      const action = controller.getSnapshot().action;
      if (isLiteratureReadingBulk(action) && (action !== before || active.operationId === action.operation_id))
        active.operationId = action.operation_id;
      await promise;
    } catch (error) {
      const stored = controller.getSnapshot();
      const accepted =
        isLiteratureReadingBulk(stored.pending) ||
        (isLiteratureReadingBulk(stored.action) && stored.action.operation_id === active.operationId);
      if (
        current(active.visit) &&
        !accepted &&
        !(error instanceof BulkPausedError) &&
        !(error instanceof OriginalPendingConflict) &&
        !(error instanceof DOMException && error.name === 'AbortError')
      )
        setActivity({ visit: active.visit, resolving: false, message: 'operationFailed' });
    } finally {
      settleSelection(active);
      if (work.current === active) work.current = undefined;
      if (current(active.visit)) setActivity((previous) => ({ ...previous, visit: active.visit, resolving: false }));
    }
  };
  const start = async (status: LiteratureReadingStatus) => {
    if (!editable() || (!selection.allMatchingSelected && !selection.selectedIds.length)) return;
    const active: Work = { visit, abort: new AbortController() };
    work.current = active;
    setActivity({ visit, resolving: selection.allMatchingSelected });
    let targets: readonly string[];
    try {
      const complete = selection.allMatchingSelected
        ? await loadCompleteLiteratureSelection(scope, active.abort.signal)
        : undefined;
      if (!current(active.visit) || active.abort.signal.aborted || work.current !== active) {
        if (work.current === active) work.current = undefined;
        return;
      }
      targets = resolveLibrarySelection(selection, complete?.item_ids);
    } catch (error) {
      if (current(active.visit) && !active.abort.signal.aborted)
        setActivity({ visit, resolving: false, message: 'resolveFailed' });
      if (work.current === active) work.current = undefined;
      return;
    }
    if (!targets.length) {
      if (current()) {
        setSelected({ visit, value: EMPTY_LIBRARY_SELECTION });
        setActivity({ visit, resolving: false, message: 'emptyResolved' });
      }
      if (work.current === active) work.current = undefined;
      return;
    }
    setActivity({ visit, resolving: false });
    await execute(active, () =>
      controller.startReadingStatusBulk(
        targets,
        status,
        scopeKey,
        () => current(active.visit) && !active.abort.signal.aborted && work.current === active
      )
    );
  };
  const resume = async (stopAfterCurrent = false) => {
    const pending = controller.getSnapshot().pending;
    if (!current() || work.current || controller.getSnapshot().mutating) return;
    if (!isLiteratureReadingBulk(pending)) {
      controller.restore();
      return;
    }
    const active: Work = { visit, abort: new AbortController(), operationId: pending.operation_id };
    setActivity({ visit, resolving: false });
    await execute(active, () =>
      controller.resumeReadingStatusBulk(
        stopAfterCurrent,
        () => current(active.visit) && !active.abort.signal.aborted && work.current === active
      )
    );
  };
  const pause = async () => {
    const pending = controller.getSnapshot().pending;
    if (!current() || !isLiteratureReadingBulk(pending)) return;
    try {
      await controller.pauseReadingStatusBulk(pending.operation_id);
    } catch (error) {
      if (current() && !(error instanceof OriginalPendingConflict))
        setActivity({ visit, resolving: false, message: 'operationFailed' });
    }
  };
  const discard = async () => {
    const pending = controller.getSnapshot().pending;
    if (!current() || !isLiteratureReadingBulk(pending) || controller.getSnapshot().mutating) return;
    try {
      await controller.discardReadingStatusBulk(pending.operation_id);
      if (current()) setSelected({ visit, value: EMPTY_LIBRARY_SELECTION });
    } catch (error) {
      if (current()) {
        if (error instanceof OriginalPendingConflict) setSelected({ visit, value: EMPTY_LIBRARY_SELECTION });
        else setActivity({ visit, resolving: false, message: 'operationFailed' });
      }
    }
  };
  return {
    selection,
    scopeKey,
    resolving,
    message,
    ownedNotice: isLiteratureReadingBulk(view.pending) || isLiteratureReadingBulk(view.action),
    clear: () => update(() => EMPTY_LIBRARY_SELECTION),
    selectAllMatching: () => update(() => allMatchingLibrarySelection()),
    togglePage: () => update((value) => toggleLibraryPageSelection(value, rowIds)),
    toggle: (id: string, shift: boolean) => {
      if (!rowIds.includes(id)) return;
      const range =
        shift && anchor.current?.visit === visit ? libraryKnownRowRange(rowIds, anchor.current.item, id) : [];
      update((value) =>
        range.length ? setLibrarySelectionItems(value, range, !isSelected(id)) : toggleLibrarySelection(value, id)
      );
      if (editable()) anchor.current = { visit, item: id };
    },
    isSelected,
    pageChecked: rowIds.length > 0 && pageCount === rowIds.length,
    pageMixed: pageCount > 0 && pageCount < rowIds.length,
    start,
    resume,
    pause,
    discard,
  };
}
