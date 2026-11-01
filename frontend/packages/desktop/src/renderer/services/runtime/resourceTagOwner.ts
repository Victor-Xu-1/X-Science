import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeResourceTagCommand,
  decodeResourceTagReceipt,
  decodeResourceTagSnapshot,
  type ResourceTagCommand,
  type ResourceTagReceipt,
  type ResourceTagRef,
  type ResourceTagSnapshot,
} from '@/common/adapter/resourceTags';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import { withRendererMutationLock, type RendererMutationStorage } from './rendererMutationSlot';
import { loadResourceTags, runResourceTagCommand, type ResourceTagRequestOptions } from './resourceTags';
import {
  createResourceTagMutationQueue,
  decodeResourceTagQueueEntry,
  resourceTagDefiniteRejection,
  resourceTagAssignmentEntry,
  resourceTagCreationEntry,
  type ResourceTagQueueEntry,
  type ResourceTagRejection,
} from './resourceTagMutationQueue';

export type ResourceTagView = Readonly<{
  snapshot: ResourceTagSnapshot | null;
  pending: readonly ResourceTagQueueEntry[];
  status: 'idle' | 'loading' | 'saving' | 'recovery' | 'error';
  busy: boolean;
  error: Error | null;
}>;
export type ResourceTagOwnerOptions = {
  storage?: () => RendererMutationStorage;
  current?: () => boolean;
  operationId?: () => string;
  load?: (options: ResourceTagRequestOptions) => Promise<ResourceTagSnapshot>;
  run?: (command: ResourceTagCommand, options: ResourceTagRequestOptions) => Promise<ResourceTagReceipt>;
  events?: Pick<Window, 'addEventListener' | 'removeEventListener'> | null;
};
const asError = (reason: unknown) =>
  reason instanceof Error ? reason : new Error('Resource tag operation failed', { cause: reason });
const rejectionError = (rejection: ResourceTagRejection) =>
  new BackendHttpError({
    method: 'WRITE',
    path: '/api/tags',
    status: rejection.status,
    body: { code: rejection.code },
  });
export const EMPTY_RESOURCE_TAG_VIEW: ResourceTagView = Object.freeze({
  snapshot: null,
  pending: Object.freeze([]),
  status: 'idle',
  busy: false,
  error: null,
});
/** Account-level state owner. Page unmounts only unsubscribe; accepted work outlives its menu. */
export class ResourceTagOwner {
  private view = EMPTY_RESOURCE_TAG_VIEW;
  private listeners = new Set<() => void>();
  private readonly scope = getRendererAccountScopeToken();
  private readonly abort = new AbortController();
  private readonly queue;
  private readonly current;
  private readonly events;
  private running: Promise<void> | null = null;
  private reads = 0;
  private readGeneration = 0;
  private started = false;
  private stopped = false;
  private waiting = new Set<string>();
  private outcomes = new Map<string, Error | null>();
  // Session-only evidence of an exact response; serialized queue phases are never server authority.
  private acknowledged = new Map<string, string>();
  constructor(
    readonly ownerId: string,
    private readonly options: ResourceTagOwnerOptions = {}
  ) {
    this.current = () =>
      !this.stopped &&
      (options.current?.() ??
        (getRendererAccountScopeToken() === this.scope && getRendererAccountOwnerId() === ownerId));
    this.queue = createResourceTagMutationQueue({
      ownerId,
      current: this.current,
      storage: options.storage ?? (() => localStorage),
    });
    this.events = options.events === undefined ? (typeof window === 'undefined' ? null : window) : options.events;
  }
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  getSnapshot = () => this.view;
  private requireCurrent() {
    if (!this.current()) throw new Error('Resource tag account changed');
  }
  private publish(patch: Partial<ResourceTagView> = {}) {
    if (!this.current()) return;
    const next = { ...this.view, ...patch },
      busy = this.running !== null || this.reads > 0;
    this.view = Object.freeze({
      ...next,
      busy,
      status: busy
        ? this.running
          ? 'saving'
          : 'loading'
        : next.pending.length
          ? 'recovery'
          : next.error
            ? 'error'
            : 'idle',
    });
    this.listeners.forEach((listener) => listener());
  }
  private sync() {
    this.requireCurrent();
    const pending = this.queue.read()?.entries ?? Object.freeze([]);
    for (const [operation, proof] of this.acknowledged) {
      if (!pending.some((entry) => entry.command.operation_id === operation && JSON.stringify(entry) === proof))
        this.acknowledged.delete(operation);
    }
    this.publish({ pending });
  }
  private requestOptions(): ResourceTagRequestOptions {
    return { accountToken: this.scope, signal: this.abort.signal };
  }
  private onStorage = (event: Event) => {
    if ((event as StorageEvent).key !== this.queue.key && (event as StorageEvent).key !== null) return;
    if (!this.current()) return;
    try {
      this.sync();
      if (this.view.pending.length) void this.drain(false);
      else void this.reload().catch((): void => undefined);
    } catch (error) {
      this.publish({ error: asError(error) });
    }
  };
  start = () => {
    if (this.started || !this.current()) return;
    this.started = true;
    this.events?.addEventListener('storage', this.onStorage);
    try {
      this.sync(); // Restore the frozen intent before preparing a fresh read or write.
      if (this.view.pending.length) void this.drain(true);
      else void this.reload().catch((): void => undefined);
    } catch (error) {
      this.publish({ error: asError(error) });
    }
  };
  stop = () => {
    this.stopped = true;
    this.abort.abort(new Error('Resource tag account changed'));
    this.events?.removeEventListener('storage', this.onStorage);
    this.listeners.clear();
  };
  private async canonical(minimumRevision = 0) {
    this.requireCurrent();
    const generation = ++this.readGeneration;
    this.reads++;
    this.publish();
    try {
      const snapshot = decodeResourceTagSnapshot(await (this.options.load ?? loadResourceTags)(this.requestOptions()));
      this.requireCurrent();
      if (
        generation !== this.readGeneration ||
        snapshot.revision < Math.max(minimumRevision, this.view.snapshot?.revision ?? 0)
      )
        throw new Error('Resource tag read was superseded or stale');
      this.publish({ snapshot }); // Equal revision is valid: permissions may have changed.
      return snapshot;
    } finally {
      this.reads--;
      this.publish();
    }
  }
  reload = async () => {
    const generation = this.readGeneration + 1;
    this.publish({ error: null });
    try {
      await this.canonical();
    } catch (error) {
      if (generation === this.readGeneration) this.publish({ error: asError(error) });
      throw error;
    }
  };
  private async send(command: ResourceTagCommand) {
    this.requireCurrent();
    const receipt = decodeResourceTagReceipt(
      await (this.options.run ?? runResourceTagCommand)(command, this.requestOptions()),
      command
    );
    this.requireCurrent();
    return receipt;
  }
  private outcome(entry: ResourceTagQueueEntry, error: Error | null) {
    if (this.waiting.has(entry.group_id)) this.outcomes.set(entry.group_id, error);
    if (error) this.publish({ error });
  }
  private async head(entry: ResourceTagQueueEntry) {
    if (entry.phase !== 'readback' || this.acknowledged.get(entry.command.operation_id) !== JSON.stringify(entry)) {
      const sending = decodeResourceTagQueueEntry({ ...entry, phase: 'unknown', minimum_revision: 0, rejection: null });
      if (!(await this.queue.replaceHead(entry, sending))) throw new Error('Resource tag original queue head changed');
      entry = sending;
      this.sync();
      try {
        const receipt = await this.send(entry.command);
        if (entry.command.kind === 'create' && entry.continuation) {
          const command = decodeResourceTagCommand({
            kind: 'assignment',
            operation_id: entry.continuation.operation_id,
            tag_id: receipt.tag_id,
            ...entry.continuation.target,
            assigned: true,
          });
          const next = decodeResourceTagQueueEntry({
            ...entry,
            command,
            phase: 'ready',
            minimum_revision: Math.max(entry.minimum_revision, receipt.snapshot.revision),
          });
          if (!(await this.queue.replaceHead(entry, next))) throw new Error('Resource tag original queue head changed');
          this.sync();
          return;
        }
        const next = decodeResourceTagQueueEntry({
          ...entry,
          phase: 'readback',
          minimum_revision: Math.max(entry.minimum_revision, receipt.snapshot.revision),
        });
        if (!(await this.queue.replaceHead(entry, next))) throw new Error('Resource tag original queue head changed');
        entry = next;
        this.acknowledged.set(entry.command.operation_id, JSON.stringify(entry));
      } catch (error) {
        const rejection = resourceTagDefiniteRejection(error);
        if (!rejection) throw error;
        const next = decodeResourceTagQueueEntry({ ...entry, phase: 'readback', rejection });
        if (!(await this.queue.replaceHead(entry, next)))
          throw new Error('Resource tag original queue head changed', { cause: error });
        entry = next;
        this.acknowledged.set(entry.command.operation_id, JSON.stringify(entry));
      }
      this.sync();
    }
    await this.canonical(entry.minimum_revision);
    if (!(await this.queue.replaceHead(entry, null))) throw new Error('Resource tag original queue head changed');
    this.outcome(entry, entry.rejection ? rejectionError(entry.rejection) : null);
    this.sync();
  }
  private drain(recover: boolean): Promise<void> {
    if (this.running) return this.running;
    const work = withRendererMutationLock(
      this.queue.sendKey,
      this.current,
      async () => {
        let allowUnknown = recover;
        while (this.current()) {
          const entry = this.queue.read()?.entries[0];
          this.sync();
          if (!entry || (entry.phase === 'unknown' && !allowUnknown)) break;
          allowUnknown = false;
          // FIFO is deliberate; append takes a separate short storage lock during this bounded send.
          // eslint-disable-next-line no-await-in-loop
          await this.head(entry);
        }
      },
      { label: 'Resource tag sender' }
    ).then((): void => undefined);
    this.running = work
      .catch((error) => {
        this.publish({ error: asError(error) });
      })
      .finally(() => {
        this.running = null;
        try {
          this.sync();
        } catch (error) {
          this.publish({ error: asError(error) });
        }
      });
    this.publish();
    return this.running;
  }
  retry = async () => {
    this.requireCurrent();
    this.publish({ error: null });
    let pending;
    try {
      pending = this.queue.read();
    } catch (error) {
      this.publish({ error: asError(error) });
      throw error;
    }
    if (pending === null) return this.reload();
    await this.drain(true);
    this.requireCurrent();
    if (this.view.pending.length || this.view.error)
      throw this.view.error ?? new Error('Resource tag recovery is required');
  };
  /** Another tab may have settled the group; storage disappearance alone is not an ACK. */
  private async verifyElsewhere(entry: ResourceTagQueueEntry) {
    try {
      let receipt = await this.send(entry.command);
      if (entry.command.kind === 'create' && entry.continuation) {
        receipt = await this.send(
          decodeResourceTagCommand({
            kind: 'assignment',
            operation_id: entry.continuation.operation_id,
            tag_id: receipt.tag_id,
            ...entry.continuation.target,
            assigned: true,
          })
        );
      }
      await this.canonical(receipt.snapshot.revision);
    } catch (error) {
      if (resourceTagDefiniteRejection(error)) await this.canonical();
      throw error;
    }
  }
  private async accept(entry: ResourceTagQueueEntry) {
    this.requireCurrent();
    this.waiting.add(entry.group_id);
    try {
      await this.queue.append(entry); // No optimistic checkbox or HTTP before durable acceptance.
      this.sync();
      this.publish({ error: null });
      await this.drain(false);
      this.requireCurrent();
      if (this.outcomes.has(entry.group_id)) {
        const error = this.outcomes.get(entry.group_id);
        if (error) throw error;
      } else if (this.queue.read()?.entries.some((item) => item.group_id === entry.group_id)) {
        throw this.view.error ?? new Error('Resource tag recovery is required');
      } else await this.verifyElsewhere(entry);
    } catch (error) {
      this.publish({ error: asError(error) });
      throw error;
    } finally {
      this.waiting.delete(entry.group_id);
      this.outcomes.delete(entry.group_id);
    }
  }
  assign = async (target: ResourceTagRef, tagId: string, assigned: boolean) =>
    this.accept(
      resourceTagAssignmentEntry(target, tagId, assigned, (this.options.operationId ?? (() => crypto.randomUUID()))())
    );
  createAndAssign = async (target: ResourceTagRef, name: string) => {
    const id = this.options.operationId ?? (() => crypto.randomUUID());
    return this.accept(resourceTagCreationEntry(target, name, id(), id()));
  };
}
const owners = new Map<string, ResourceTagOwner>();
registerRendererAccountReset('resource-tag-owner', () => {
  owners.forEach((owner) => owner.stop());
  owners.clear();
});
export function getResourceTagOwner(account = getRendererAccountScopeToken()): ResourceTagOwner {
  if (account !== getRendererAccountScopeToken()) throw new Error('Resource tag account changed');
  let owner = owners.get(account);
  if (!owner) {
    owner = new ResourceTagOwner(getRendererAccountOwnerId());
    owners.set(account, owner);
  }
  return owner;
}
