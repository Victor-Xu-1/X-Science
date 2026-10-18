import { isBackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodePdfReadingPending,
  pdfReadingExpected,
  samePdfReadingScope,
  type PdfReadingContext,
  type PdfReadingPendingMutation,
  type PdfReadingMutation,
  type PdfReadingSource,
  type PdfReadingSelection,
  type PdfReadingPosition,
} from '@/common/adapter/pdfReadingContext';
import { getRendererAccountScopeToken } from '../rendererAccountScope';
import { inspectPdfReadingCandidates, loadPdfReadingContext, mutatePdfReadingContext } from './pdfReadingContext';

export type PdfReadingView = {
  context: PdfReadingContext | null;
  loading: boolean;
  mutating: boolean;
  pending: PdfReadingPendingMutation | null;
  error: 'load' | 'uncertain' | 'conflict' | 'mutation' | null;
};
export type PdfReadingControllerOptions = {
  ownerId: string;
  frameId: string;
  frameIncarnationId?: string;
  persistPending: (pending: PdfReadingPendingMutation | undefined) => void;
  load?: typeof loadPdfReadingContext;
  mutate?: typeof mutatePdfReadingContext;
  inspect?: typeof inspectPdfReadingCandidates;
};

/** One composer owner. Preview controls borrow this instance; no second runner,
 * binding store or retry loop is created. Persisted draft requests are hints
 * for exact retry, never accepted binding authority. */
export class PdfReadingController {
  private view: PdfReadingView = { context: null, loading: false, mutating: false, pending: null, error: null };
  private listeners = new Set<() => void>();
  private readonly account = getRendererAccountScopeToken();
  private disposed = false;
  private lifetime = 0;
  private reads = 0;
  private readAbort?: AbortController;
  private readPromise?: Promise<PdfReadingContext>;
  private operationPromise?: Promise<PdfReadingContext>;
  private writesStarted = false;
  private positions = new Map<string, PdfReadingPosition>();
  constructor(private options: PdfReadingControllerOptions) {}
  activate() {
    if (this.account !== getRendererAccountScopeToken()) return;
    this.disposed = false;
  }
  getSnapshot = (): PdfReadingView => this.view;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private current() {
    return !this.disposed && this.account === getRendererAccountScopeToken();
  }
  private emit(patch: Partial<PdfReadingView>) {
    if (!this.current()) return;
    this.view = { ...this.view, ...patch };
    for (const listener of this.listeners) listener();
  }
  private accept(context: PdfReadingContext) {
    if (
      context.scope.frame_id !== this.options.frameId ||
      (this.options.frameIncarnationId && context.scope.frame_incarnation_id !== this.options.frameIncarnationId) ||
      (this.view.context && !samePdfReadingScope(this.view.context.scope, context.scope))
    )
      throw new Error('PDF reading scope changed');
    if (!this.view.context || context.revision >= this.view.context.revision) this.emit({ context });
  }
  restorePending(value: unknown) {
    if (!this.current() || this.writesStarted || this.view.pending) return;
    const pending = decodePdfReadingPending(value);
    if (
      !pending ||
      pending.ownerId !== this.options.ownerId ||
      pending.frameId !== this.options.frameId ||
      (this.options.frameIncarnationId &&
        pending.request.expected_frame_incarnation_id !== this.options.frameIncarnationId)
    )
      return;
    this.emit({ pending, error: 'uncertain' });
  }
  load = (): Promise<PdfReadingContext> => {
    if (!this.current()) return Promise.reject(new Error('PDF reading scope changed'));
    if (this.readPromise) return this.readPromise;
    const generation = ++this.reads;
    const abort = new AbortController();
    this.readAbort = abort;
    this.emit({ loading: true });
    const pending = (this.options.load ?? loadPdfReadingContext)(this.options.frameId, abort.signal)
      .then((context) => {
        if (!this.current() || generation !== this.reads) throw new Error('PDF reading scope changed');
        this.accept(context);
        this.emit({ error: this.view.pending ? this.view.error : null });
        return context;
      })
      .catch((error: unknown) => {
        if (this.current() && generation === this.reads)
          this.emit({ error: this.view.pending ? this.view.error : 'load' });
        throw error;
      })
      .finally(() => {
        if (generation === this.reads) {
          this.readPromise = undefined;
          this.emit({ loading: false });
        }
      });
    this.readPromise = pending;
    return pending;
  };
  refresh = async (): Promise<void> => {
    const rejected = this.view.pending && ['conflict', 'mutation'].includes(this.view.error ?? '');
    await this.load();
    if (!this.current()) return;
    if (rejected && !this.view.mutating) {
      this.options.persistPending(undefined);
      this.emit({ pending: null, error: null });
    }
  };
  private submit(request: PdfReadingMutation): Promise<PdfReadingContext> {
    if (!this.current()) return Promise.reject(new Error('PDF reading scope changed'));
    if (this.operationPromise) return this.operationPromise;
    const existingPending = this.view.pending;
    if (existingPending && JSON.stringify(existingPending.request) !== JSON.stringify(request)) {
      return Promise.reject(new Error('Resolve the original PDF reading request first'));
    }
    this.writesStarted = true;
    const lifetime = this.lifetime;
    const pending = existingPending ?? {
      ownerId: this.options.ownerId,
      frameId: this.options.frameId,
      request: structuredClone(request),
    };
    // Exact retry consumes the existing durable draft request. Only a new
    // mutation writes it before network IO; retries must not rewrite it.
    if (!existingPending) this.options.persistPending(pending);
    this.emit({ pending, mutating: true, error: null });
    const promise = (this.options.mutate ?? mutatePdfReadingContext)(this.options.frameId, pending.request)
      .then((context) => {
        if (!this.current() || lifetime !== this.lifetime) throw new Error('PDF reading scope changed');
        this.accept(context);
        this.options.persistPending(undefined);
        this.emit({ pending: null, error: null });
        return context;
      })
      .catch((error: unknown) => {
        if (this.current() && lifetime === this.lifetime) {
          const kind =
            isBackendHttpError(error) && error.status < 500
              ? error.status === 409
                ? 'conflict'
                : 'mutation'
              : 'uncertain';
          this.emit({ error: kind });
        }
        throw error;
      })
      .finally(() => {
        this.operationPromise = undefined;
        if (lifetime === this.lifetime) this.emit({ mutating: false });
      });
    this.operationPromise = promise;
    return promise;
  }
  retry = (): Promise<PdfReadingContext> => {
    if (!this.view.pending || this.view.error !== 'uncertain')
      return Promise.reject(new Error('No uncertain PDF reading request'));
    return this.submit(this.view.pending.request);
  };
  prepareSources = async (sources: PdfReadingSource[]): Promise<void> => {
    const lifetime = this.lifetime;
    const pending = this.view.pending;
    if (pending) {
      if (
        pending.request.schema !== 'x-science.pdf-reading-link.v1' ||
        !pending.request.sources?.every((item) =>
          sources.some((source) => source.artifact_id === item.artifact_id && source.version_id === item.version_id)
        )
      ) {
        throw new Error('Resolve the original PDF reading request first');
      }
      await this.retry();
    }
    const context = this.view.context ?? (await this.load());
    if (!this.current() || lifetime !== this.lifetime) throw new Error('PDF reading scope changed');
    const additions = sources.filter(
      (source) =>
        !context.bindings.some(
          (binding) => binding.artifact_id === source.artifact_id && binding.version_id === source.version_id
        )
    );
    if (!additions.length) return;
    const inspected = await (this.options.inspect ?? inspectPdfReadingCandidates)(this.options.frameId, additions);
    if (!this.current() || lifetime !== this.lifetime || !samePdfReadingScope(inspected.scope, context.scope)) {
      throw new Error('PDF reading scope changed');
    }
    if (
      inspected.candidates.some(
        (item) => !item.eligible && !['single_page', 'too_large', 'not_pdf'].includes(item.reason ?? '')
      )
    ) {
      throw new Error('PDF reading inspection is unavailable');
    }
    // Ordinary single-page/large attachments remain attachments. Only actual
    // eligible immutable versions are admitted as reading bindings.
    await this.link(inspected.candidates.filter((item) => item.eligible).map((item) => item.source));
  };
  link = async (sources: PdfReadingSource[]): Promise<PdfReadingContext> => {
    if (this.view.pending) throw new Error('Resolve the original PDF reading request first');
    const context = this.view.context ?? (await this.load());
    if (!this.current()) throw new Error('PDF reading scope changed');
    const additions = sources.filter(
      (source) =>
        !context.bindings.some(
          (binding) => binding.artifact_id === source.artifact_id && binding.version_id === source.version_id
        )
    );
    if (additions.length === 0) return context;
    if (context.bindings.length + additions.length > 3) throw new Error('A conversation can link up to 3 PDFs');
    return this.submit({
      schema: 'x-science.pdf-reading-link.v1',
      operation_id: crypto.randomUUID(),
      ...pdfReadingExpected(context),
      sources: structuredClone(additions),
    });
  };
  unlink = (bindingId: string): Promise<PdfReadingContext> => {
    const context = this.view.context;
    if (!context || this.view.pending || !context.bindings.some((binding) => binding.binding_id === bindingId)) {
      return Promise.reject(new Error('PDF binding is unavailable'));
    }
    return this.submit({
      schema: 'x-science.pdf-reading-unlink.v1',
      operation_id: crypto.randomUUID(),
      ...pdfReadingExpected(context),
      binding_id: bindingId,
    });
  };
  reportPosition = (source: PdfReadingSource, position: PdfReadingPosition): void => {
    if (
      !this.current() ||
      !Number.isSafeInteger(position.pageNumber) ||
      !Number.isSafeInteger(position.pageCount) ||
      position.pageNumber < 1 ||
      position.pageCount < 1 ||
      position.pageNumber > position.pageCount
    )
      return;
    this.positions.set(source.version_id, { ...position });
    while (this.positions.size > 100) this.positions.delete(this.positions.keys().next().value!);
  };
  positionFor = (versionId: string) => this.positions.get(versionId);
  capture = (activeVersionId?: string): PdfReadingSelection | undefined => {
    if (!this.current()) throw new Error('PDF reading scope changed');
    if (this.view.pending) throw new Error('Resolve the original PDF reading request before sending');
    const context = this.view.context;
    if (!context?.bindings.length) return undefined;
    const active = context.bindings.find((binding) => binding.version_id === activeVersionId) ?? context.bindings[0];
    const position = this.positions.get(active.version_id);
    return {
      schema: 'x-science.pdf-reading-selection.v1',
      ...pdfReadingExpected(context),
      binding_ids: context.bindings.map((binding) => binding.binding_id),
      active_binding_id: active.binding_id,
      ...(position && position.pageCount === active.page_count ? { page_number: position.pageNumber } : {}),
    };
  };
  dispose() {
    this.disposed = true;
    this.lifetime++;
    this.reads++;
    this.readAbort?.abort();
    this.readPromise = undefined;
    this.view = {
      ...this.view,
      loading: false,
      mutating: false,
      error: this.view.pending ? 'uncertain' : this.view.error,
    };
    this.listeners.clear();
    // An in-flight POST is not cancellation. Its exact draft request remains
    // available to the originating owner after remount/reload.
  }
}
