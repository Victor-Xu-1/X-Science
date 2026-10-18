import { loadXScienceKernels } from '../xScienceNotebook';
import { loadXScienceExecutionLogPage } from '../xScienceRuntimeOperations';
import { isXScienceHttpError } from '../xScienceHttp';
import { sameNotebookIdentity, sameNotebookTarget } from './manualNotebookContract';
import { discoverManualNotebook, lookupManualNotebook } from './manualNotebookService';
import { notebookTargetKey, type NotebookSessionStore } from './NotebookSessionStore';

async function loadNotebookInventory(store: NotebookSessionStore): Promise<boolean> {
  const opening = store.opening;
  try {
    const inventory = await store.read((signal) => loadXScienceKernels(store.frameId, { signal, timeoutMs: 15000 }));
    if (!inventory) return false;
    const scope = inventory.manualExecutionScope;
    if (!scope || scope.frameId !== store.frameId || scope.rootFrameId !== store.rootFrameId)
      throw new Error('notebook_scope_unavailable');
    if (store.snapshot.scope && !sameNotebookIdentity(store.snapshot.scope, scope)) {
      store.publish({ status: 'scopeChanged', targets: [], selectedKey: null, draft: '' });
      return false;
    }
    const targets = inventory.kernels.flatMap((kernel) =>
      kernel.manualExecutionTarget && sameNotebookIdentity(scope, kernel.manualExecutionTarget)
        ? [kernel.manualExecutionTarget]
        : []
    );
    const previous = store.snapshot.selectedKey;
    const preferred = store.preferredKernelId
      ? targets.find((target) => target.kernelId === store.preferredKernelId)
      : targets[0];
    const selectedKey = targets.some((target) => notebookTargetKey(target) === previous)
      ? previous
      : previous
        ? null
        : preferred
          ? notebookTargetKey(preferred)
          : null;
    store.publish({
      scope,
      targets,
      status: 'ready',
      error: store.snapshot.error === 'pendingFailed' ? 'pendingFailed' : null,
      busyKernelIds: inventory.kernels
        .filter((kernel) => kernel.busy || kernel.starting)
        .map((kernel) => kernel.kernelId),
      selectedKey,
      ...(previous && !targets.some((target) => notebookTargetKey(target) === previous) ? { draft: '' } : {}),
    });
    return true;
  } catch {
    if (store.visible() && opening === store.opening)
      store.publish({ status: 'error', error: 'loadFailed', targets: [] });
    return false;
  }
}
export function readNotebookInventory(store: NotebookSessionStore): Promise<boolean> {
  if (store.inventoryPromise) return store.inventoryPromise;
  const read = loadNotebookInventory(store);
  store.inventoryPromise = read;
  void read.finally(() => {
    if (store.inventoryPromise === read) store.inventoryPromise = null;
  });
  return read;
}
export async function readNotebookHistory(store: NotebookSessionStore, older = false) {
  if (store.historyBusy || !store.snapshot.scope || (older && !store.snapshot.historyBefore)) return;
  store.historyBusy = true;
  const opening = store.opening,
    before = older ? store.snapshot.historyBefore : null;
  store.publish({ historyLoading: true, historyError: false });
  try {
    const page = await store.read((signal) =>
      loadXScienceExecutionLogPage(store.frameId, before, { signal, timeoutMs: 15000 })
    );
    if (!page) return;
    if (page.nextBefore && page.nextBefore === before) throw new Error('notebook_history_cursor_did_not_advance');
    const seen = new Set(store.snapshot.history.map((record) => record.id));
    const newest = new Set(page.records.map((record) => record.id));
    store.publish({
      history: older
        ? [...page.records.filter((record) => !seen.has(record.id)), ...store.snapshot.history]
        : [...store.snapshot.history.filter((record) => !newest.has(record.id)), ...page.records],
      historyBefore: older || store.snapshot.history.length === 0 ? page.nextBefore : store.snapshot.historyBefore,
    });
  } catch {
    if (store.visible() && opening === store.opening) store.publish({ historyError: true });
  } finally {
    store.historyBusy = false;
    if (store.visible() && opening === store.opening) store.publish({ historyLoading: false });
  }
}
export async function readNotebookReceipt(store: NotebookSessionStore, id: string) {
  const entry = store.entries.get(id);
  if (!entry || store.readRequests.has(id)) return;
  store.readRequests.add(id);
  const opening = store.opening;
  try {
    const receipt = await store.read((signal) =>
      lookupManualNotebook(entry.target, id, entry.request ?? undefined, { signal })
    );
    if (receipt) store.receive(id, receipt);
  } catch (error) {
    if (store.visible() && opening === store.opening)
      store.update(id, {
        error:
          isXScienceHttpError(error) && error.status === 404 && error.code === 'MANUAL_EXECUTION_NOT_ACCEPTED'
            ? 'notAccepted'
            : 'readFailed',
      });
  } finally {
    store.readRequests.delete(id);
  }
}
export async function readNotebookPending(store: NotebookSessionStore, more = false) {
  if (store.scanBusy || !store.snapshot.scope || (more && !store.snapshot.nextCursor)) return;
  store.scanBusy = true;
  const opening = store.opening,
    cursor = more ? store.snapshot.nextCursor : null;
  const scope = store.snapshot.scope;
  store.publish({ scanning: true, error: null });
  try {
    const page = await store.read((signal) => discoverManualNotebook(scope, cursor, { signal }));
    if (!page) return;
    if ((cursor && page.nextCursor === cursor) || (more && store.snapshot.throughSequence !== page.throughSequence))
      throw new Error('notebook_pending_cursor_changed');
    const additions = page.pending.filter((item) => !store.entries.has(item.requestId));
    if (store.entries.size + additions.length > 128) throw new Error('notebook_pending_capacity_reached');
    for (const summary of page.pending) {
      const existing = store.entries.get(summary.requestId);
      if (existing && !sameNotebookTarget(existing.target, summary.target))
        throw new Error('notebook_pending_target_changed');
      if (!existing)
        store.entries.set(summary.requestId, {
          id: summary.requestId,
          target: summary.target,
          request: null,
          summary,
          receipt: null,
          phase: 'accepted',
          error: null,
          cancelIntent: false,
          cancelling: false,
          cancelIssued: false,
        });
    }
    store.publish({
      scanComplete: page.scanComplete,
      nextCursor: page.nextCursor,
      throughSequence: page.throughSequence,
    });
    // One bounded metadata page; read at most four receipts concurrently. No automatic cursor scan.
    for (let index = 0; index < page.pending.length && store.visible(); index += 4)
      await Promise.all(
        page.pending.slice(index, index + 4).map((summary) => readNotebookReceipt(store, summary.requestId))
      );
  } catch {
    if (store.visible() && opening === store.opening) store.publish({ error: 'pendingFailed' });
  } finally {
    store.scanBusy = false;
    if (store.visible() && opening === store.opening) store.publish({ scanning: false });
  }
}
export async function refreshNotebookSession(store: NotebookSessionStore, discover = false): Promise<void> {
  if (store.refreshPromise) return store.refreshPromise;
  const load = (async () => {
    if (!(await readNotebookInventory(store))) return;
    await Promise.all([
      readNotebookHistory(store),
      discover || (store.snapshot.throughSequence === null && store.snapshot.error !== 'pendingFailed')
        ? readNotebookPending(store)
        : Promise.resolve(),
      ...[...store.entries.values()]
        .filter((entry) => entry.receipt?.state !== 'terminal')
        .map((entry) => readNotebookReceipt(store, entry.id)),
    ]);
  })();
  store.refreshPromise = load;
  try {
    await load;
  } finally {
    if (store.refreshPromise === load) store.refreshPromise = null;
  }
}
