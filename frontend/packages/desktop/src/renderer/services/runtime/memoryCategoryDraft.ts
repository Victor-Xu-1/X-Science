import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '@/renderer/services/rendererAccountScope';
import {
  createXScienceMemoryCategory,
  updateXScienceMemoryCategory,
  memoryCategoryWriteOutcome,
} from '@/renderer/services/xScienceMemory';

export type CategoryFields = Readonly<{ name: string; guidance: string; autoRecall: boolean }>;
export type CategorySeed = CategoryFields & { id?: string };
export type CategoryDraft = Readonly<{
  episode: number;
  id?: string;
  baseline: CategoryFields;
  value: CategoryFields;
  revision: number;
  phase: 'editing' | 'saving' | 'failed' | 'uncertain';
}>;
export type ViewLease = Readonly<{ id: number; scope: string }>;
export type ActionLease = Readonly<{ view: ViewLease; activation: number; episode: number }>;
export type RefreshLease = Readonly<{ view: ViewLease; activation: number; serial: number; claim: number }>;
export type FailureLease = Readonly<{ view: ViewLease; activation: number; attempt: number }>;
export type CategorySnapshot = Readonly<{
  scope: string;
  draft: CategoryDraft | null;
  visibleView: number | null;
  activation: number;
  commitSerial: number;
  failedAttempt: number | null;
  failureKind: 'rejected' | 'uncertain' | 'conflict' | null;
  editorVisible: boolean;
  commitOutcome: 'saved' | 'missing';
}>;
export type SaveOutcome = Readonly<{ kind: 'committed' | 'failed' | 'uncertain' | 'stale' | 'denied' }>;
export type CategoryOwnerEnvironment = {
  account: () => { id: string; scope: string };
  registerReset: (reset: () => void) => () => void;
  write: (value: CategorySeed, signal: AbortSignal, operationId: string) => Promise<unknown>;
  operationId?: () => string;
  reportError?: (error: unknown) => void;
};

type ViewRecord = { lease: ViewLease; priority: number; seenCommit: number; changedCommit: number };
type SaveToken = { scope: string; episode: number; revision: number; attempt: number; operationId: string };
type FrozenOperation = Readonly<{
  id: string;
  scope: string;
  episode: number;
  revision: number;
  payload: CategorySeed;
}>;
type Inflight = { token: SaveToken; abort: AbortController; promise: Promise<SaveOutcome> };

const emptyFields = (): CategoryFields => Object.freeze({ name: '', guidance: '', autoRecall: true });
const copyFields = (value: CategoryFields): CategoryFields =>
  Object.freeze({ name: value.name, guidance: value.guidance, autoRecall: value.autoRecall });

/** One document/account owner. View disposal is never a draft discard. */
export class MemoryCategoryDraftOwner {
  private snapshot: CategorySnapshot;
  private listeners = new Set<() => void>();
  private views = new Map<number, ViewRecord>();
  private nextView = 0;
  private nextEpisode = 0;
  private nextAttempt = 0;
  private nextClaim = 0;
  private savedAnnouncement = 0;
  private failureAnnouncement = 0;
  private inflight: Inflight | null = null;
  private operation: FrozenOperation | null = null;
  private refreshClaim: RefreshLease | null = null;
  private releaseReset: () => void;

  constructor(private readonly env: CategoryOwnerEnvironment) {
    this.snapshot = this.blank();
    this.releaseReset = env.registerReset(() => this.resetAccount());
  }

  private blank(): CategorySnapshot {
    return Object.freeze({
      scope: this.env.account().scope,
      draft: null,
      visibleView: null,
      activation: 0,
      commitSerial: 0,
      failedAttempt: null,
      failureKind: null,
      editorVisible: false,
      commitOutcome: 'saved',
    });
  }

  private publish(patch: Partial<CategorySnapshot>): void {
    this.snapshot = Object.freeze({ ...this.snapshot, ...patch });
    for (const listener of this.listeners) listener();
  }

  private resetAccount(): void {
    this.inflight?.abort.abort();
    this.inflight = null;
    this.operation = null;
    this.refreshClaim = null;
    this.views.clear();
    this.savedAnnouncement = 0;
    this.failureAnnouncement = 0;
    this.snapshot = this.blank();
    for (const listener of this.listeners) listener();
  }

  private live(view: ViewLease | null): view is ViewLease {
    const account = this.env.account();
    return Boolean(
      view &&
      account.id &&
      account.scope === this.snapshot.scope &&
      view.scope === account.scope &&
      this.views.get(view.id)?.lease === view
    );
  }

  private owns(view: ViewLease | null): view is ViewLease {
    return this.live(view) && this.snapshot.visibleView === view.id;
  }

  private activate(view: ViewLease): void {
    this.refreshClaim = null;
    this.publish({ visibleView: view.id, activation: this.snapshot.activation + 1 });
  }

  private bestView(): ViewLease | undefined {
    return [...this.views.values()]
      .filter((record) => this.live(record.lease))
      .sort((a, b) => b.priority - a.priority || b.lease.id - a.lease.id)[0]?.lease;
  }

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  readonly getSnapshot = (): CategorySnapshot => this.snapshot;

  isViewRegistered(view: ViewLease | null): boolean {
    return this.live(view);
  }

  registerView(layer: 'page' | 'modal' = 'page'): ViewLease | null {
    const account = this.env.account();
    if (!account.id) return null;
    if (account.scope !== this.snapshot.scope) this.resetAccount();
    const lease = Object.freeze({ id: ++this.nextView, scope: account.scope });
    this.views.set(lease.id, { lease, priority: layer === 'modal' ? 2 : 1, seenCommit: 0, changedCommit: 0 });
    const best = this.bestView();
    if (best && best.id !== this.snapshot.visibleView) this.activate(best);
    return lease;
  }

  unregisterView(view: ViewLease | null): void {
    if (!view || this.views.get(view.id)?.lease !== view) return;
    this.views.delete(view.id);
    if (this.snapshot.visibleView !== view.id) return;
    this.refreshClaim = null;
    const next = this.bestView();
    this.publish({ visibleView: next?.id ?? null, activation: this.snapshot.activation + 1 });
  }

  open(view: ViewLease | null, seed?: CategorySeed): boolean {
    if (!this.owns(view)) return false;
    // Opening elsewhere resumes the existing episode; it cannot overwrite it.
    let draft = this.snapshot.draft;
    if (!this.snapshot.draft) {
      const fields = seed ? copyFields(seed) : emptyFields();
      draft = Object.freeze({
        episode: ++this.nextEpisode,
        ...(seed?.id ? { id: seed.id } : {}),
        baseline: fields,
        value: fields,
        revision: 0,
        phase: 'editing',
      });
    }
    this.refreshClaim = null;
    this.publish({
      draft,
      visibleView: view.id,
      activation: this.snapshot.activation + 1,
      editorVisible: true,
      failedAttempt: draft?.phase === 'failed' || draft?.phase === 'uncertain' ? this.snapshot.failedAttempt : null,
    });
    return true;
  }

  actionLease(view: ViewLease | null): ActionLease | null {
    if (!this.owns(view) || !this.snapshot.draft) return null;
    return Object.freeze({
      view,
      activation: this.snapshot.activation,
      episode: this.snapshot.draft.episode,
    });
  }

  private currentAction(action: ActionLease | null): action is ActionLease {
    return Boolean(
      action &&
      this.owns(action.view) &&
      this.snapshot.editorVisible &&
      action.activation === this.snapshot.activation &&
      action.episode === this.snapshot.draft?.episode
    );
  }

  update(action: ActionLease | null, patch: Partial<CategoryFields>): boolean {
    if (
      !this.currentAction(action) ||
      this.snapshot.draft?.phase === 'saving' ||
      this.snapshot.draft?.phase === 'uncertain'
    )
      return false;
    const draft = this.snapshot.draft!;
    this.operation = null;
    this.publish({
      draft: Object.freeze({
        ...draft,
        value: copyFields({ ...draft.value, ...patch }),
        revision: draft.revision + 1,
        phase: 'editing',
      }),
    });
    return true;
  }

  close(action: ActionLease | null): boolean {
    if (!this.currentAction(action) || this.snapshot.draft?.phase === 'saving') return false;
    if (this.snapshot.draft?.phase === 'uncertain') {
      this.publish({ editorVisible: false, activation: this.snapshot.activation + 1 });
    } else {
      this.operation = null;
      this.publish({ draft: null, failedAttempt: null, failureKind: null, editorVisible: false });
    }
    return true;
  }

  private currentSave(token: SaveToken): boolean {
    return Boolean(
      this.env.account().scope === token.scope &&
      this.snapshot.scope === token.scope &&
      this.inflight?.token === token &&
      this.operation?.id === token.operationId &&
      this.snapshot.draft?.episode === token.episode &&
      this.snapshot.draft.revision === token.revision
    );
  }

  save(action: ActionLease | null): Promise<SaveOutcome> {
    if (!this.currentAction(action)) return Promise.resolve({ kind: 'denied' });
    const draft = this.snapshot.draft!;
    if (this.inflight && this.currentSave(this.inflight.token)) return this.inflight.promise;
    if (!draft.value.name.trim() || !draft.value.guidance.trim()) return Promise.resolve({ kind: 'denied' });

    const token: SaveToken = {
      scope: this.snapshot.scope,
      episode: draft.episode,
      revision: draft.revision,
      attempt: ++this.nextAttempt,
      operationId: this.operation?.id ?? this.env.operationId?.() ?? crypto.randomUUID(),
    };
    const abort = new AbortController();
    const payload: CategorySeed = {
      ...(draft.id ? { id: draft.id } : {}),
      name: draft.value.name.trim(),
      guidance: draft.value.guidance.trim(),
      autoRecall: draft.value.autoRecall,
    };
    this.operation ??= Object.freeze({
      id: token.operationId,
      scope: token.scope,
      episode: token.episode,
      revision: token.revision,
      payload: Object.freeze(payload),
    });
    const operation = this.operation;
    const commit = (missing: boolean): SaveOutcome => {
      if (!this.currentSave(token)) return { kind: 'stale' };
      this.inflight = null;
      this.operation = null;
      this.refreshClaim = null;
      this.publish({
        draft: null,
        failedAttempt: null,
        failureKind: null,
        editorVisible: false,
        commitSerial: this.snapshot.commitSerial + 1,
        commitOutcome: missing ? 'missing' : 'saved',
      });
      return { kind: 'committed' };
    };
    const promise: Promise<SaveOutcome> = Promise.resolve()
      .then(() => {
        if (!this.currentSave(token)) throw new Error('Memory category request superseded before dispatch');
        return this.env.write(operation.payload, abort.signal, operation.id);
      })
      .then(
        (): SaveOutcome => commit(false),
        (error): SaveOutcome => {
          if (!this.currentSave(token)) return { kind: 'stale' };
          const outcome = memoryCategoryWriteOutcome(error);
          if (outcome === 'committed-missing') return commit(true);
          this.env.reportError?.(error);
          const rejected = outcome === 'rejected';
          this.inflight = null;
          this.publish({
            draft: Object.freeze({ ...this.snapshot.draft!, phase: rejected ? 'failed' : 'uncertain' }),
            failureKind: rejected ? 'rejected' : outcome === 'conflict' ? 'conflict' : 'uncertain',
            failedAttempt: token.attempt,
          });
          return { kind: rejected ? 'failed' : 'uncertain' };
        }
      );
    this.inflight = { token, abort, promise };
    this.publish({ draft: Object.freeze({ ...draft, phase: 'saving' }), failedAttempt: null, failureKind: null });
    return promise;
  }

  captureRefresh(view: ViewLease | null): RefreshLease | null {
    if (!this.owns(view)) return null;
    const record = this.views.get(view.id)!;
    if (record.seenCommit >= this.snapshot.commitSerial) return null;
    const lease = Object.freeze({
      view,
      activation: this.snapshot.activation,
      serial: this.snapshot.commitSerial,
      claim: ++this.nextClaim,
    });
    this.refreshClaim = lease;
    return lease;
  }

  isRefreshCurrent(lease: RefreshLease): boolean {
    return (
      this.refreshClaim === lease &&
      this.owns(lease.view) &&
      lease.activation === this.snapshot.activation &&
      lease.serial === this.snapshot.commitSerial
    );
  }

  claimChanged(lease: RefreshLease): boolean {
    if (!this.isRefreshCurrent(lease)) return false;
    const record = this.views.get(lease.view.id)!;
    if (record.changedCommit >= lease.serial) return false;
    record.changedCommit = lease.serial;
    return true;
  }

  claimSaved(lease: RefreshLease): boolean {
    if (!this.isRefreshCurrent(lease) || this.savedAnnouncement >= lease.serial) return false;
    this.savedAnnouncement = lease.serial;
    return true;
  }

  acknowledgeRefresh(lease: RefreshLease): boolean {
    if (!this.isRefreshCurrent(lease)) return false;
    this.views.get(lease.view.id)!.seenCommit = lease.serial;
    this.refreshClaim = null;
    return true;
  }

  captureFailure(view: ViewLease | null): FailureLease | null {
    if (!this.owns(view) || this.snapshot.failedAttempt === null) return null;
    return Object.freeze({
      view,
      activation: this.snapshot.activation,
      attempt: this.snapshot.failedAttempt,
    });
  }

  claimFailure(lease: FailureLease): boolean {
    if (
      !this.owns(lease.view) ||
      lease.activation !== this.snapshot.activation ||
      lease.attempt !== this.snapshot.failedAttempt ||
      this.failureAnnouncement >= lease.attempt
    )
      return false;
    this.failureAnnouncement = lease.attempt;
    return true;
  }

  dispose(): void {
    this.releaseReset();
    this.resetAccount();
    this.listeners.clear();
  }
}

export const memoryCategoryDraftOwner = new MemoryCategoryDraftOwner({
  account: () => ({ id: getRendererAccountOwnerId(), scope: getRendererAccountScopeToken() }),
  registerReset: (reset) => registerRendererAccountReset('memory-category-draft-owner', reset),
  reportError: (error) => console.error('X-Science memory mutation failed:', error),
  write: (value, signal, operationId) => {
    const { id, ...input } = value;
    return id
      ? updateXScienceMemoryCategory(id, input, { signal, operationId })
      : createXScienceMemoryCategory(input, { signal, operationId });
  },
});
