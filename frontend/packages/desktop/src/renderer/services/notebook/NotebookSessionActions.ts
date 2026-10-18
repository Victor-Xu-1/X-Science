import { freezeManualNotebookRequest } from './manualNotebookContract';
import { cancelManualNotebook, submitManualNotebook } from './manualNotebookService';
import { readNotebookHistory, readNotebookInventory, readNotebookReceipt } from './NotebookSessionReads';
import { notebookTargetKey, type NotebookSessionStore } from './NotebookSessionStore';

async function submitFrozen(store: NotebookSessionStore, id: string) {
  const entry = store.entries.get(id);
  if (!entry?.request || !store.isLive(entry.target)) return;
  store.update(id, { phase: 'submitting', error: null });
  try {
    const receipt = await submitManualNotebook(entry.target, entry.request);
    if (!store.current()) return;
    store.receive(id, receipt);
    if (store.entries.get(id)?.cancelIntent) await cancelNotebookRequest(store, id, false);
    if (receipt.state === 'terminal') await readNotebookHistory(store);
  } catch {
    if (!store.current()) return;
    // A concurrently confirmed canonical receipt is stronger evidence than a lost POST response.
    if (!store.entries.get(id)?.receipt) store.update(id, { phase: 'uncertain', error: 'sendUncertain' });
    // A read, never an automatic second POST. A 404 cannot retract an in-flight admission.
    await readNotebookReceipt(store, id);
  }
}
export async function runNotebookCode(
  store: NotebookSessionStore,
  selectedKey: string,
  code: string,
  timeoutMs = 600000
) {
  const selected = store.selected();
  if (
    !selected ||
    !store.visible() ||
    store.snapshot.commandBusy ||
    store.unresolved(selected) ||
    !store.snapshot.scanComplete ||
    store.snapshot.error === 'pendingFailed'
  )
    return;
  store.publish({ commandBusy: true, error: null });
  const opening = store.opening;
  try {
    if (!(await readNotebookInventory(store)) || opening !== store.opening || !store.visible()) return;
    const target = store.selected();
    if (
      !target ||
      notebookTargetKey(target) !== selectedKey ||
      store.unresolved(target) ||
      store.snapshot.busyKernelIds.includes(target.kernelId)
    ) {
      store.publish({ error: 'targetChanged' });
      return;
    }
    const request = freezeManualNotebookRequest(target, code, timeoutMs, crypto.randomUUID());
    // Only terminal presentation entries may be pruned; unresolved UUIDs are never evicted.
    if (store.entries.size >= 64)
      for (const [id, entry] of store.entries) {
        if (entry.receipt?.state === 'terminal') store.entries.delete(id);
        if (store.entries.size < 32) break;
      }
    store.entries.set(request.request_id, {
      id: request.request_id,
      target,
      request,
      receipt: null,
      summary: null,
      phase: 'submitting',
      error: null,
      cancelIntent: false,
      cancelling: false,
      cancelIssued: false,
    });
    store.publish({ draft: '' });
    await submitFrozen(store, request.request_id);
  } catch {
    if (store.current()) store.publish({ error: 'invalidCode' });
  } finally {
    if (store.current()) store.publish({ commandBusy: false });
  }
}
export async function retryNotebookRequest(store: NotebookSessionStore, id: string) {
  const entry = store.entries.get(id);
  if (!store.visible() || store.snapshot.commandBusy || !entry?.request || entry.receipt || entry.phase !== 'uncertain')
    return;
  store.publish({ commandBusy: true });
  const opening = store.opening;
  try {
    if (!(await readNotebookInventory(store)) || opening !== store.opening || !store.visible()) return;
    if (!store.isLive(entry.target)) {
      store.update(id, { error: 'targetChanged' });
      return;
    }
    await submitFrozen(store, id);
  } finally {
    if (store.current()) store.publish({ commandBusy: false });
  }
}
export async function cancelNotebookRequest(store: NotebookSessionStore, id: string, explicit = true) {
  const original = store.entries.get(id);
  if (
    !store.current() ||
    !original?.request ||
    original.receipt?.requestKind === 'legacy' ||
    original.receipt?.state === 'terminal' ||
    original.cancelling ||
    !store.isLive(original.target)
  )
    return;
  if (!explicit && original.cancelIssued) return;
  store.update(id, { cancelIntent: true, error: null });
  if (!original.receipt) await readNotebookReceipt(store, id);
  const entry = store.entries.get(id);
  if (
    !entry?.request ||
    !entry.receipt ||
    entry.receipt.state === 'terminal' ||
    entry.receipt.requestKind === 'legacy' ||
    entry.cancelling ||
    (!explicit && entry.cancelIssued) ||
    !store.current() ||
    !store.isLive(entry.target)
  )
    return;
  store.update(id, { cancelling: true, cancelIssued: true });
  try {
    const receipt = await cancelManualNotebook(entry.target, entry.request);
    if (store.current()) store.receive(id, receipt);
  } catch {
    if (store.current()) store.update(id, { error: 'cancelUncertain' });
  } finally {
    if (store.current()) store.update(id, { cancelling: false });
  }
}
export async function continueNotebookCancellation(store: NotebookSessionStore) {
  for (const entry of store.entries.values()) {
    if (entry.cancelIntent && !entry.cancelIssued && entry.receipt) await cancelNotebookRequest(store, entry.id, false);
  }
}
