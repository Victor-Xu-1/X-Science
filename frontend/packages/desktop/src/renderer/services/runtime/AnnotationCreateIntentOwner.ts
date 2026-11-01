import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '../rendererAccountScope';
import {
  createXScienceAnnotationTarget,
  loadXScienceAnnotationTarget,
  type CreateXScienceArtifactAnnotationInput,
  type XScienceAnnotationTarget,
  type XScienceArtifactAnnotation,
} from '../xScienceAnnotations';
import {
  createRendererMutationSlot,
  withRendererMutationLock,
  type RendererMutationStorage,
  type RendererMutationSlot,
} from './rendererMutationSlot';
import {
  annotationIntentStorageKey,
  canonicalAnnotationSource,
  canonicalAnnotationTarget,
  decodeAnnotationCreateIntent,
  encodeAnnotationCreateIntent,
  freezeAnnotationCreateBody,
  sameAnnotationCreateIntent,
  validateAnnotationCreateAck,
  type AnnotationCreateIntent,
  type AnnotationCreateSource,
} from './annotationCreateIntent';
import { waitForAnnotationCreate } from './annotationCreateRequest';

export type AnnotationCreateStatus =
  | 'idle'
  | 'restored'
  | 'resolving'
  | 'sending'
  | 'saved'
  | 'uncertain'
  | 'sourceUnavailable'
  | 'conflict'
  | 'missing'
  | 'storage';
export type AnnotationCreateView = Readonly<{
  status: AnnotationCreateStatus;
  busy: boolean;
  pending: AnnotationCreateIntent | null;
  lastSuccess?: Readonly<{ operationId: string; annotation: XScienceArtifactAnnotation }>;
}>;
export type AnnotationCreateOptions = {
  storage?: RendererMutationStorage;
  uuid?: () => string;
  timeoutMs?: number;
  send?: (intent: AnnotationCreateIntent, signal: AbortSignal) => Promise<XScienceArtifactAnnotation>;
  loadSource?: (target: XScienceAnnotationTarget) => Promise<{ targetKey: string; currentChecksum: string | null }>;
};

/** One account/target intent owner. Canonical annotation lists remain in their original stores. */
export class AnnotationCreateIntentOwner {
  private readonly account = getRendererAccountScopeToken();
  private readonly target: Readonly<XScienceAnnotationTarget>;
  private readonly key: string;
  private readonly slot: RendererMutationSlot<AnnotationCreateIntent>;
  private view: AnnotationCreateView = { status: 'idle', busy: false, pending: null };
  private listeners = new Set<() => void>();
  private promise?: Promise<XScienceArtifactAnnotation | null>;
  private flight?: AbortController;
  private disposed = false;
  constructor(
    private readonly ownerId: string,
    target: XScienceAnnotationTarget,
    private readonly options: AnnotationCreateOptions = {}
  ) {
    this.target = canonicalAnnotationTarget(target);
    this.key = annotationIntentStorageKey(ownerId, target);
    this.slot = createRendererMutationSlot<AnnotationCreateIntent>({
      key: this.key,
      storage: () => this.options.storage ?? localStorage,
      decode: (value) => decodeAnnotationCreateIntent(value, this.ownerId, this.target),
      encode: encodeAnnotationCreateIntent,
      same: sameAnnotationCreateIntent,
    });
    this.restore();
  }
  get storageKey() {
    return this.key;
  }
  isCurrentAccount = () => this.current();
  getSnapshot = () => this.view;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private current() {
    return (
      !this.disposed &&
      Boolean(this.ownerId) &&
      this.ownerId === getRendererAccountOwnerId() &&
      this.account === getRendererAccountScopeToken()
    );
  }
  private active(abort: AbortController) {
    return this.current() && this.flight === abort && !abort.signal.aborted;
  }
  private requireActive(abort: AbortController) {
    if (!this.active(abort)) throw abort.signal.reason ?? new Error('Annotation account changed');
  }
  private emit(patch: Partial<AnnotationCreateView>) {
    if (!this.current()) return;
    this.view = { ...this.view, ...patch };
    for (const listener of this.listeners) listener();
  }
  private storage<T>(work: () => T): T {
    try {
      return work();
    } catch (error) {
      this.emit({ status: 'storage' });
      throw error;
    }
  }
  private locked<T>(abort: AbortController, work: () => T): Promise<T> {
    return withRendererMutationLock(this.key, () => this.active(abort), work, { label: 'Annotation' });
  }
  restore = () => {
    if (!this.current() || this.promise) return;
    try {
      const pending = this.storage(() => this.slot.read());
      this.emit({ pending, busy: false, status: pending ? 'restored' : 'idle' });
    } catch {
      this.emit({ busy: false, status: 'storage' });
    }
  };
  dispose = () => {
    this.disposed = true;
    this.flight?.abort(new Error('Annotation account changed'));
    this.listeners.clear();
  };
  private begin(work: (abort: AbortController) => Promise<XScienceArtifactAnnotation | null>) {
    if (!this.current()) return Promise.reject(new Error('Annotation account changed'));
    if (this.promise) return this.promise;
    const abort = new AbortController();
    const promise = Promise.resolve()
      .then(() => work(abort))
      .catch((error: unknown) => {
        if (this.current()) {
          let status: AnnotationCreateStatus = this.view.status === 'storage' ? 'storage' : 'uncertain';
          if (isBackendHttpError(error)) {
            if ([401, 403, 404].includes(error.status)) status = 'sourceUnavailable';
            else if (error.status === 409 && error.code === 'ANNOTATION_CREATE_IDEMPOTENCY_CONFLICT')
              status = 'conflict';
          }
          this.emit({ status });
        }
        throw error;
      })
      .finally(() => {
        if (this.flight === abort) {
          this.flight = undefined;
          this.promise = undefined;
          this.emit({ busy: false });
        }
      });
    this.flight = abort;
    this.promise = promise;
    this.emit({ busy: true, status: 'resolving' });
    return promise;
  }
  start = (input: CreateXScienceArtifactAnnotationInput, sourceHint?: AnnotationCreateSource | null) => {
    if (this.promise) return Promise.reject(new Error('An annotation request is already in progress'));
    const body = freezeAnnotationCreateBody(input);
    const hint = sourceHint ? { targetKey: sourceHint.targetKey, checksum: sourceHint.checksum } : null;
    return this.begin(async (abort) => {
      const original = await this.locked(abort, () => this.storage(() => this.slot.read()));
      this.requireActive(abort);
      if (original) {
        this.emit({ pending: original, status: 'restored' });
        throw new Error('Restore the original annotation request first');
      }
      let source = hint;
      if (!source) {
        const loaded = await waitForAnnotationCreate(abort, this.options.timeoutMs ?? 30_000, () =>
          (this.options.loadSource ?? loadXScienceAnnotationTarget)(this.target)
        );
        source = { targetKey: loaded.targetKey, checksum: loaded.currentChecksum ?? '' };
      }
      this.requireActive(abort);
      const frozenSource = canonicalAnnotationSource(this.target, source);
      const intent = await this.locked(abort, () => {
        const existing = this.storage(() => this.slot.read());
        if (existing) {
          this.emit({ pending: existing });
          throw new Error('Restore the original annotation request first');
        }
        const value = decodeAnnotationCreateIntent(
          {
            schema: 'x-science.annotation-create.v1',
            ownerId: this.ownerId,
            operationId: this.options.uuid?.() ?? crypto.randomUUID(),
            target: this.target,
            source: frozenSource,
            body,
          },
          this.ownerId,
          this.target
        );
        this.storage(() => this.slot.replace(null, value));
        this.emit({ pending: value });
        return value;
      });
      return this.sendFrozen(intent, abort);
    });
  };
  /** Does not inspect a new draft or prepare today's source metadata. */
  retryFrozen = () =>
    this.begin(async (abort) => {
      const intent = await this.locked(abort, () => this.storage(() => this.slot.read()));
      this.requireActive(abort);
      if (!intent) {
        this.emit({ pending: null, status: 'idle' });
        return null;
      }
      this.emit({ pending: intent });
      return this.sendFrozen(intent, abort);
    });
  private async sendFrozen(
    intent: AnnotationCreateIntent,
    abort: AbortController
  ): Promise<XScienceArtifactAnnotation | null> {
    this.requireActive(abort);
    this.emit({ status: 'sending' });
    try {
      const send =
        this.options.send ??
        ((value: AnnotationCreateIntent, signal: AbortSignal) =>
          createXScienceAnnotationTarget(value.target, value.body, value.source.checksum, undefined, {
            idempotencyKey: value.operationId,
            signal,
          }));
      const annotation = await waitForAnnotationCreate(abort, this.options.timeoutMs ?? 30_000, (signal) =>
        send(intent, signal)
      );
      this.requireActive(abort);
      validateAnnotationCreateAck(intent, annotation);
      return await this.locked(abort, () =>
        this.storage(() => {
          const plan = this.slot.settle(intent, true);
          if (!plan.remove) {
            this.emit({ pending: plan.pending, status: plan.pending ? 'restored' : 'idle' });
            return null;
          }
          this.emit({ pending: null, status: 'saved', lastSuccess: { operationId: intent.operationId, annotation } });
          return annotation;
        })
      );
    } catch (error) {
      if (
        this.active(abort) &&
        isBackendHttpError(error) &&
        error.status === 410 &&
        error.code === 'ANNOTATION_COMMITTED_RESULT_MISSING'
      ) {
        return this.locked(abort, () =>
          this.storage((): null => {
            const plan = this.slot.settle(intent, true);
            this.emit(
              plan.pending
                ? { pending: plan.pending, status: 'restored' }
                : { pending: null, status: 'missing', lastSuccess: undefined }
            );
            return null;
          })
        );
      }
      throw error;
    }
  }
}
