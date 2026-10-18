import { ipcBridge } from '@/common';
import { registerRendererAccountReset, rendererAccountScopedKey } from '../rendererAccountScope';
import {
  cancelNotebookRequest,
  continueNotebookCancellation,
  retryNotebookRequest,
  runNotebookCode,
} from './NotebookSessionActions';
import {
  readNotebookHistory,
  readNotebookPending,
  readNotebookReceipt,
  refreshNotebookSession,
} from './NotebookSessionReads';
import { NotebookSessionStore, notebookTargetKey } from './NotebookSessionStore';

export class NotebookSessionController extends NotebookSessionStore {
  private cleanupObservation: (() => void) | null = null;
  private lastNavigation: string | undefined;
  subscribe = (listener: () => void): (() => void) => {
    if (!this.current()) return () => {};
    this.listeners.add(listener);
    if (this.listeners.size === 1) this.startObservation();
    return () => {
      this.listeners.delete(listener);
      if (this.listeners.size === 0) {
        this.cleanupObservation?.();
        this.cleanupObservation = null;
        this.retireView();
      }
    };
  };
  private startObservation() {
    const opening = ++this.opening;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let wakeTimer: ReturnType<typeof setTimeout> | undefined;
    const current = () => this.visible() && opening === this.opening;
    const refresh = async (discover = false) => {
      if (!current()) return;
      await refreshNotebookSession(this, discover);
      if (current()) await continueNotebookCancellation(this);
    };
    const poll = async () => {
      await refresh();
      if (current())
        timer = setTimeout(() => {
          void poll();
        }, 3000);
    };
    const wake = () => {
      if (wakeTimer || !current()) return;
      wakeTimer = setTimeout(() => {
        wakeTimer = undefined;
        void refresh();
      }, 100);
    };
    const unsubscribe = ipcBridge.kernel.executionCellUpdate.on((event) => {
      if (event.frame_id === this.frameId || event.root_frame_id === this.rootFrameId) wake();
    });
    const reconnect = ipcBridge.realtime.reconnected.on(() => {
      if (current()) void refresh(true);
    });
    this.cleanupObservation = () => {
      clearTimeout(timer);
      clearTimeout(wakeTimer);
      unsubscribe();
      reconnect();
    };
    // An old opening's aborted reads must release their per-resource reservations first.
    const previous = this.refreshPromise;
    this.publish({ status: 'loading', targets: [], historyLoading: false, scanning: false });
    void Promise.resolve(previous)
      .catch((error: unknown) => {
        console.warn('[Notebook] Previous observation failed', {
          errorName: error instanceof Error ? error.name : typeof error,
        });
      })
      .then(async () => {
        if (!current()) return;
        await refresh(true);
        if (current())
          timer = setTimeout(() => {
            void poll();
          }, 3000);
      });
  }
  navigate(kernelId?: string, focusRequest?: string) {
    if (!this.current() || !focusRequest || this.lastNavigation === focusRequest) return;
    this.lastNavigation = focusRequest;
    this.preferredKernelId = kernelId;
    const selected = this.snapshot.targets.find((target) => target.kernelId === kernelId);
    this.publish({ selectedKey: selected ? notebookTargetKey(selected) : null, draft: '' });
  }
  refresh = () => refreshNotebookSession(this, true);
  loadOlder = () => readNotebookHistory(this, true);
  loadMorePending = () => readNotebookPending(this, true);
  lookup = async (id: string) => {
    await readNotebookReceipt(this, id);
    await continueNotebookCancellation(this);
  };
  run = (key: string, code: string) => runNotebookCode(this, key, code);
  retry = (id: string) => retryNotebookRequest(this, id);
  cancel = (id: string) => cancelNotebookRequest(this, id);
  override dispose() {
    this.cleanupObservation?.();
    this.cleanupObservation = null;
    super.dispose();
  }
}

const controllers = new Map<string, NotebookSessionController>();
export function getNotebookSessionController(frameId: string, rootFrameId: string): NotebookSessionController {
  const key = rendererAccountScopedKey(`manual-notebook:${JSON.stringify([frameId, rootFrameId])}`);
  const existing = controllers.get(key);
  if (existing && !(existing.snapshot.status === 'scopeChanged' && existing.listeners.size === 0)) return existing;
  if (existing) {
    existing.dispose();
    controllers.delete(key);
  }
  if (controllers.size >= 16) {
    for (const [candidateKey, candidate] of controllers) {
      if (candidate.listeners.size === 0 && !candidate.unresolved()) {
        candidate.dispose();
        controllers.delete(candidateKey);
        break;
      }
    }
  }
  if (controllers.size >= 16) throw new Error('notebook_open_session_limit');
  const controller = new NotebookSessionController(frameId, rootFrameId);
  controllers.set(key, controller);
  return controller;
}
registerRendererAccountReset('manual-notebook-sessions', () => {
  controllers.forEach((controller) => controller.dispose());
  controllers.clear();
});
