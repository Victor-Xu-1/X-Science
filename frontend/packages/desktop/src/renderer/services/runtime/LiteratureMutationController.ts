import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { decodeLiteratureReceipt, type LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '../rendererAccountScope';
import {
  classifyXScienceAttachmentInitFailure,
  initializeXScienceProjectAttachment,
  uploadXScienceProjectAttachment,
} from '../onboardingService';
import { digestLibraryFile } from './libraryFileDigest';
import { runLiteratureCommand } from './literatureLibrary';
import { runLiteratureCollectionCommand } from './literatureCollections';
import { runLiteraturePublicationCommand } from './literaturePublicationCheck';
import { runLiteratureReadingStatusCommand } from './literatureReadingStatus';
import { withLiteratureMutationLock } from './literatureMutationLock';
import { createLiteratureReadingBulkOperations } from './literatureReadingBulkOperations';
import { isLiteratureReadingBulk } from './literatureReadingBulkIntent';
import type { LiteratureReadingStatus, LiteratureReadingStatusReceipt } from '@/common/adapter/literatureReadingStatus';
import {
  catalogueUploadFailureCertainty,
  createUploadIntent,
  currentUploadPending,
  decodeMutationCommand,
  decodePendingRecord,
  encodePendingRecord,
  isCollectionCommand,
  isUploadIntent,
  matchingSettlement,
  OriginalPendingConflict,
  samePending,
  uploadOriginIdentity,
  withArtifactReceipt,
  withCatalogueRejection,
  withInitReceipt,
  type LiteratureMutationCommand,
  type LiteratureMutationReceipt,
  type LiteraturePending,
  type LiteratureUploadIntent,
  type ReceiptFor,
} from './literatureUploadIntent';

export type { LiteratureMutationCommand, LiteratureMutationReceipt } from './literatureUploadIntent';
export type LiteratureUploadState =
  | 'hashing'
  | 'waiting-lock'
  | 'initializing'
  | 'uploading'
  | 'catalogue'
  | 'paused'
  | 'file-required'
  | 'file-mismatch'
  | 'conflict'
  | null;
export type LiteratureMutationView = {
  pending: LiteraturePending | null;
  /** Projection of this owner's last action, including a definite rejection. */
  action?: LiteraturePending;
  mutating: boolean;
  error: 'uncertain' | 'rejected' | 'storage' | null;
  receipt?: LiteratureMutationReceipt;
  uploadState: LiteratureUploadState;
  canDiscardRejectedUpload: boolean;
};
type PdfDraft = { projectId: string; title: string; file: File };
type Flight = {
  generation: number;
  abort: AbortController;
  upload: boolean;
  pending?: LiteraturePending;
  draft?: PdfDraft;
  bulkPause?: boolean;
  bulkCanContinue?: () => boolean;
};
type Options = {
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
  run?: typeof runLiteratureCommand;
  runCollection?: typeof runLiteratureCollectionCommand;
  runPublication?: typeof runLiteraturePublicationCommand;
  runReadingStatus?: typeof runLiteratureReadingStatusCommand;
  initialize?: typeof initializeXScienceProjectAttachment;
  upload?: typeof uploadXScienceProjectAttachment;
  digest?: typeof digestLibraryFile;
  uuid?: () => string;
};
class PendingStorageError extends Error {
  constructor(cause: unknown) {
    super('Library pending storage unavailable', { cause });
  }
}
const interrupted = () => new DOMException('Library operation was cancelled', 'AbortError');
const definitelyRejected = (error: unknown) => isBackendHttpError(error) && error.status >= 400 && error.status < 500;

/** Settle promptly, but observe both branches of even a noncooperative late operation. */
function observeAbort<T>(signal: AbortSignal, operation: () => Promise<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const cleanup = () => signal.removeEventListener('abort', abort);
    const abort = () => {
      cleanup();
      reject(interrupted());
    };
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener('abort', abort, { once: true });
    void Promise.resolve()
      .then(() => {
        if (signal.aborted) throw interrupted();
        return operation();
      })
      .then(
        (value) => {
          cleanup();
          if (signal.aborted) reject(interrupted());
          else resolve(value);
        },
        (error: unknown) => {
          cleanup();
          reject(signal.aborted ? interrupted() : error);
        }
      );
  });
}

let instanceSequence = 0;
/** One account owner and one durable slot for catalogue commands and PDF admission. */
export class LiteratureMutationController {
  private view: LiteratureMutationView = {
    pending: null,
    mutating: false,
    error: null,
    uploadState: null,
    canDiscardRejectedUpload: false,
  };
  private listeners = new Set<() => void>();
  private readonly account = getRendererAccountScopeToken();
  private readonly key: string;
  private readonly unregisterReset: () => void;
  private promise?: Promise<LiteratureMutationReceipt>;
  private flight?: Flight;
  private generation = 0;
  private selectedFile?: { origin: string; file: File };
  private rejectedUpload?: string;
  private readonly bulkReading = createLiteratureReadingBulkOperations<Flight>({
    current: () => this.current(),
    pending: () => this.view.pending,
    running: () => this.promise,
    begin: (intent, work, canContinue) =>
      this.begin(false, work, intent, undefined, canContinue) as Promise<LiteratureReadingStatusReceipt>,
    checkActive: (flight) => this.requireActive(flight),
    pauseRequested: (flight) =>
      Boolean(flight.bulkPause) || Boolean(flight.bulkCanContinue && !flight.bulkCanContinue()),
    locked: (flight, work) =>
      this.locked(
        () =>
          work({
            read: () => this.read(),
            write: (expected, next) => this.write(expected, next, flight),
            settle: (expected, receipt) => this.settle(expected, true, null, receipt, flight),
            publish: (update) => this.emit(update, flight),
          }),
        flight
      ),
    send: (command, flight) => this.dispatch(command, flight.abort.signal),
    // Authentication/CSRF refusal occurs before the original operation ledger can be consulted.
    rejected: (error) =>
      definitelyRejected(error) && isBackendHttpError(error) && error.status !== 401 && error.status !== 403,
    uuid: () => (this.options.uuid ? this.options.uuid() : crypto.randomUUID()),
  });
  startReadingStatusBulk = (
    ids: readonly string[],
    status: LiteratureReadingStatus,
    scopeKey: string,
    canContinue?: () => boolean
  ) => this.bulkReading.start(ids, status, scopeKey, canContinue);
  resumeReadingStatusBulk = (stopAfterCurrent = false, canContinue?: () => boolean) => {
    if (stopAfterCurrent && this.flight && isLiteratureReadingBulk(this.flight.pending)) this.flight.bulkPause = true;
    return this.bulkReading.resume(stopAfterCurrent, canContinue);
  };
  pauseReadingStatusBulk = (operationId: string) => {
    if (this.flight && isLiteratureReadingBulk(this.flight.pending) && this.flight.pending.operation_id === operationId)
      this.flight.bulkPause = true;
    return this.bulkReading.pause(operationId);
  };
  discardReadingStatusBulk = (operationId: string) => this.bulkReading.discard(operationId);
  constructor(
    private ownerId: string,
    private options: Options = {}
  ) {
    this.key = `x-science.literature-mutation.v1:${encodeURIComponent(ownerId)}`;
    this.unregisterReset = registerRendererAccountReset(`literature-mutation-instance:${++instanceSequence}`, () => {
      ++this.generation;
      this.flight?.abort.abort();
      this.selectedFile = undefined;
      this.rejectedUpload = undefined;
      this.listeners.clear();
      this.unregisterReset();
    });
    this.restore();
  }
  getSnapshot = () => this.view;
  isCurrentAccount = () => this.current();
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private current() {
    return Boolean(this.ownerId) && this.account === getRendererAccountScopeToken();
  }
  private active(flight: Flight) {
    return (
      this.current() && this.flight === flight && this.generation === flight.generation && !flight.abort.signal.aborted
    );
  }
  private requireActive(flight: Flight) {
    if (!this.active(flight)) throw interrupted();
  }
  private emit(patch: Partial<LiteratureMutationView>, flight?: Flight) {
    if (!this.current() || (flight && !this.active(flight))) return;
    this.view = { ...this.view, ...patch };
    this.view.canDiscardRejectedUpload = Boolean(
      !this.view.mutating &&
      this.rejectedUpload &&
      this.view.pending &&
      isUploadIntent(this.view.pending) &&
      this.view.pending.phase === 'catalogue' &&
      this.view.pending.catalogue_rejected &&
      encodePendingRecord(this.ownerId, this.view.pending) === this.rejectedUpload
    );
    for (const listener of this.listeners) listener();
  }
  private storageAction<T>(action: () => T, flight?: Flight): T {
    try {
      return action();
    } catch (error) {
      this.emit({ error: 'storage' }, flight);
      throw new PendingStorageError(error);
    }
  }
  private read(): LiteraturePending | null {
    return this.storageAction(() => {
      const raw = (this.options.storage ?? localStorage).getItem(this.key);
      return raw === null ? null : decodePendingRecord(JSON.parse(raw), this.ownerId);
    });
  }
  private projection(pending: LiteraturePending | null): LiteratureUploadState {
    if (!pending || !isUploadIntent(pending)) return null;
    return pending.phase === 'catalogue' || this.selectedFile?.origin === uploadOriginIdentity(pending)
      ? 'paused'
      : 'file-required';
  }
  private async locked<T>(critical: () => T | Promise<T>, flight?: Flight): Promise<T> {
    let acquired = false;
    try {
      return await withLiteratureMutationLock(
        this.key,
        () => (flight ? this.active(flight) : this.current()),
        () => {
          acquired = true;
          return critical();
        }
      );
    } catch (error) {
      if (!acquired && !(error instanceof OriginalPendingConflict) && (!flight || this.active(flight))) {
        this.emit({ error: 'storage' }, flight);
        throw new PendingStorageError(error);
      }
      throw error;
    }
  }
  /** Only callers holding the original slot lock may write or settle. */
  private write(expected: LiteraturePending | null, next: LiteraturePending, flight?: Flight) {
    if (flight) this.requireActive(flight);
    else if (!this.current()) throw interrupted();
    const actual = this.read();
    if (actual ? !expected || !samePending(actual, expected) : Boolean(expected)) {
      this.emit(
        { pending: actual, action: actual ?? undefined, error: 'uncertain', uploadState: this.projection(actual) },
        flight
      );
      throw new OriginalPendingConflict();
    }
    this.storageAction(
      () => (this.options.storage ?? localStorage).setItem(this.key, encodePendingRecord(this.ownerId, next)),
      flight
    );
    this.rejectedUpload = undefined;
    this.emit({ pending: next, action: next, error: null }, flight);
  }
  private settle(
    expected: LiteraturePending,
    clear: boolean,
    error: LiteratureMutationView['error'],
    receipt?: LiteratureMutationReceipt,
    flight?: Flight
  ) {
    if (flight) this.requireActive(flight);
    const plan = matchingSettlement(this.read(), expected, clear);
    if (plan.remove) this.storageAction(() => (this.options.storage ?? localStorage).removeItem(this.key), flight);
    if (plan.remove) {
      this.rejectedUpload = undefined;
      if (isUploadIntent(expected)) this.selectedFile = undefined;
    }
    this.emit(
      {
        pending: plan.pending,
        action: plan.pending ?? expected,
        error: plan.pending ? 'uncertain' : error,
        uploadState: this.projection(plan.pending),
        ...(receipt ? { receipt } : {}),
      },
      flight
    );
  }
  private dispatch(command: LiteratureMutationCommand, signal: AbortSignal) {
    if (command.kind === 'check-publication')
      return (this.options.runPublication ?? runLiteraturePublicationCommand)(command, signal);
    if (command.kind === 'set-reading-status')
      return (this.options.runReadingStatus ?? runLiteratureReadingStatusCommand)(command, signal);
    return isCollectionCommand(command)
      ? (this.options.runCollection ?? runLiteratureCollectionCommand)(command)
      : (this.options.run ?? runLiteratureCommand)(command);
  }
  private begin(
    upload: boolean,
    work: (flight: Flight) => Promise<LiteratureMutationReceipt>,
    pending?: LiteraturePending,
    draft?: PdfDraft,
    bulkCanContinue?: () => boolean
  ) {
    const flight: Flight = {
      generation: ++this.generation,
      abort: new AbortController(),
      upload,
      pending,
      draft,
      bulkCanContinue,
    };
    const promise = observeAbort(flight.abort.signal, () => work(flight)).finally(() => {
      if (this.promise === promise) {
        this.promise = undefined;
        this.flight = undefined;
        const running = ['hashing', 'waiting-lock', 'initializing', 'uploading', 'catalogue'].includes(
          this.view.uploadState ?? ''
        );
        this.emit({ mutating: false, ...(running ? { uploadState: this.projection(this.view.pending) } : {}) });
      }
    });
    this.flight = flight;
    this.promise = promise;
    this.emit({ mutating: true, action: pending });
    return promise;
  }
  private async execute(command: LiteratureMutationCommand, flight: Flight) {
    await this.locked(() => {
      const pending = this.read();
      if (pending && !samePending(pending, command)) {
        this.emit({ pending, action: pending, error: 'uncertain', uploadState: this.projection(pending) }, flight);
        throw new OriginalPendingConflict();
      }
      this.write(pending, command, flight);
    }, flight);
    this.requireActive(flight);
    let receipt: LiteratureMutationReceipt;
    try {
      receipt = await this.dispatch(command, flight.abort.signal);
    } catch (error) {
      const rejected = definitelyRejected(error);
      if (this.active(flight))
        await this.locked(
          () => this.settle(command, rejected, rejected ? 'rejected' : 'uncertain', undefined, flight),
          flight
        );
      throw error;
    }
    this.requireActive(flight);
    await this.locked(() => this.settle(command, true, null, receipt, flight), flight);
    return receipt;
  }
  restore = () => {
    if (!this.current() || this.promise) return;
    try {
      const pending = this.read();
      this.emit({
        pending,
        action: pending ?? undefined,
        error: pending ? 'uncertain' : null,
        uploadState: this.projection(pending),
      });
    } catch {
      this.emit({ error: 'storage' });
    }
  };
  submit = <Command extends LiteratureMutationCommand>(input: Command): Promise<ReceiptFor<Command>> => {
    if (!this.current()) return Promise.reject(new Error('Library account changed'));
    const command = decodeMutationCommand(input);
    if (this.promise)
      return this.flight?.pending && !isUploadIntent(this.flight.pending) && samePending(this.flight.pending, command)
        ? (this.promise as Promise<ReceiptFor<Command>>)
        : Promise.reject(new OriginalPendingConflict());
    return this.begin(false, (flight) => this.execute(command, flight), command) as Promise<ReceiptFor<Command>>;
  };
  private draft(input: PdfDraft): PdfDraft {
    if (
      !(input.file instanceof File) ||
      !input.file.name.trim() ||
      input.file.size <= 0 ||
      !input.projectId.trim() ||
      !input.title.trim() ||
      input.title.trim().length > 4096
    )
      throw new Error('A project, title and nonempty PDF File are required');
    return { file: input.file, projectId: input.projectId.trim(), title: input.title.trim() };
  }
  importPdf = (input: PdfDraft): Promise<LiteratureReceipt> => {
    if (!this.current()) return Promise.reject(new Error('Library account changed'));
    const draft = this.draft(input);
    if (this.promise)
      return this.flight?.draft?.file === draft.file &&
        this.flight.draft.projectId === draft.projectId &&
        this.flight.draft.title === draft.title
        ? (this.promise as Promise<LiteratureReceipt>)
        : Promise.reject(new OriginalPendingConflict());
    return this.begin(
      true,
      async (flight) => {
        this.emit({ uploadState: 'hashing' }, flight);
        const hash = await (this.options.digest ?? digestLibraryFile)(draft.file, flight.abort.signal);
        this.requireActive(flight);
        const uuid = this.options.uuid ?? (() => crypto.randomUUID());
        const intent = createUploadIntent({
          projectId: draft.projectId,
          title: draft.title,
          filename: draft.file.name,
          size: draft.file.size,
          contentType: draft.file.type || 'application/octet-stream',
          hash,
          catalogueOperationId: uuid(),
          initOperationId: uuid(),
        });
        return this.uploadLock(flight, async () => {
          this.write(null, intent, flight);
          this.selectedFile = { origin: uploadOriginIdentity(intent), file: draft.file };
          return this.advance(intent, draft.file, flight);
        });
      },
      undefined,
      draft
    ) as Promise<LiteratureReceipt>;
  };
  private uploadLock(flight: Flight, critical: () => Promise<LiteratureReceipt>): Promise<LiteratureReceipt> {
    this.emit({ uploadState: 'waiting-lock' }, flight);
    return observeAbort(flight.abort.signal, () =>
      this.locked(() => observeAbort(flight.abort.signal, critical), flight)
    );
  }
  private async advance(
    expected: LiteratureUploadIntent,
    file: File | undefined,
    flight: Flight
  ): Promise<LiteratureReceipt> {
    const actual = this.read();
    let intent: LiteratureUploadIntent;
    try {
      intent = currentUploadPending(actual, expected);
    } catch (error) {
      this.emit(
        {
          pending: actual,
          action: actual ?? undefined,
          error: actual ? 'uncertain' : null,
          uploadState: this.projection(actual),
        },
        flight
      );
      throw error;
    }
    const replayingCatalogue = intent.phase === 'catalogue';
    try {
      if (intent.phase === 'init') {
        this.requireActive(flight);
        this.emit({ uploadState: 'initializing' }, flight);
        const ack = await (this.options.initialize ?? initializeXScienceProjectAttachment)(intent.init, {
          signal: flight.abort.signal,
        });
        this.requireActive(flight);
        const next = withInitReceipt(intent, ack);
        this.write(intent, next, flight);
        intent = next;
      }
      if (intent.phase === 'upload') {
        this.requireActive(flight);
        if (!file) throw new Error('Select the original File to resume the Library upload');
        this.emit({ uploadState: 'uploading' }, flight);
        const artifact = await (this.options.upload ?? uploadXScienceProjectAttachment)(intent.init.project_id, file, {
          signal: flight.abort.signal,
          pending: { projectId: intent.init.project_id, uploadId: intent.init_ack.upload_id },
        });
        this.requireActive(flight);
        const next = withArtifactReceipt(intent, artifact);
        this.write(intent, next, flight);
        intent = next;
      }
      if (intent.phase !== 'catalogue') throw new Error('Library upload phase changed');
      this.requireActive(flight);
      if (replayingCatalogue) {
        const next = withCatalogueRejection(intent, false);
        this.write(intent, next, flight);
        intent = next;
      }
      if (intent.phase !== 'catalogue') throw new Error('Library upload phase changed');
      this.requireActive(flight);
      this.emit({ uploadState: 'catalogue' }, flight);
      const raw = await this.dispatch(intent.command, flight.abort.signal);
      this.requireActive(flight);
      const receipt = decodeLiteratureReceipt(raw, intent.operation_id);
      if (!receipt.attachment_id || !receipt.version_id) throw new Error('Literature import not acknowledged');
      this.settle(intent, true, null, receipt, flight);
      return receipt;
    } catch (error) {
      if (
        this.active(flight) &&
        !(error instanceof PendingStorageError) &&
        !(error instanceof OriginalPendingConflict)
      ) {
        const certainty =
          intent.phase === 'init'
            ? classifyXScienceAttachmentInitFailure(error)
            : intent.phase === 'catalogue' && isBackendHttpError(error)
              ? catalogueUploadFailureCertainty(error.status, error.code)
              : 'unknown';
        if (intent.phase === 'init' && certainty === 'rejected')
          this.settle(intent, true, 'rejected', undefined, flight);
        else {
          if (intent.phase === 'catalogue' && certainty === 'rejected') {
            const rejected = withCatalogueRejection(intent, true);
            this.write(intent, rejected, flight);
            this.requireActive(flight);
            intent = rejected;
            this.rejectedUpload = encodePendingRecord(this.ownerId, rejected);
          } else this.rejectedUpload = undefined;
          this.emit(
            {
              pending: this.read(),
              error: certainty === 'rejected' ? 'rejected' : 'uncertain',
              uploadState: certainty === 'conflict' ? 'conflict' : this.projection(intent),
            },
            flight
          );
        }
      }
      throw error;
    }
  }
  private async resumeFile(
    intent: LiteratureUploadIntent,
    file: File | undefined,
    flight: Flight
  ): Promise<File | undefined> {
    if (intent.phase === 'catalogue') return undefined;
    const remembered = this.selectedFile?.origin === uploadOriginIdentity(intent) ? this.selectedFile.file : undefined;
    const selected = file ?? remembered;
    if (!(selected instanceof File)) {
      this.emit({ uploadState: 'file-required' }, flight);
      throw new Error('Select the original File to resume the Library upload');
    }
    if (selected !== remembered) {
      this.emit({ uploadState: 'hashing' }, flight);
      const hash = await (this.options.digest ?? digestLibraryFile)(selected, flight.abort.signal);
      this.requireActive(flight);
      if (hash !== intent.file_sha256) {
        this.emit({ uploadState: 'file-mismatch' }, flight);
        throw new Error('The selected File bytes differ from the original Library upload');
      }
    }
    this.requireActive(flight);
    const original =
      selected.name === intent.init.filename &&
      (selected.type || 'application/octet-stream') === intent.init.content_type
        ? selected
        : new File([selected], intent.init.filename, { type: intent.init.content_type });
    this.selectedFile = { origin: uploadOriginIdentity(intent), file: original };
    return original;
  }
  retry = (file?: File): Promise<LiteratureMutationReceipt> => {
    if (!this.current()) return Promise.reject(new Error('Library account changed'));
    if (this.promise) return this.promise;
    let pending: LiteraturePending | null;
    try {
      pending = this.read();
    } catch (error) {
      return Promise.reject(error);
    }
    this.emit({
      pending,
      action: pending ?? undefined,
      error: pending ? 'uncertain' : null,
      uploadState: this.projection(pending),
    });
    if (!pending) return Promise.reject(new Error('No Library request to retry'));
    if (isLiteratureReadingBulk(pending)) return this.resumeReadingStatusBulk(true);
    if (!isUploadIntent(pending)) return this.submit(pending);
    const expected = pending;
    return this.begin(
      true,
      async (flight) => {
        const selected = await this.resumeFile(expected, file, flight);
        this.requireActive(flight);
        return this.uploadLock(flight, () => this.advance(expected, selected, flight));
      },
      expected
    );
  };
  cancelPdfImport = (expected?: Promise<LiteratureMutationReceipt>): void => {
    if ((expected && this.promise !== expected) || !this.flight?.upload) return;
    ++this.generation;
    this.flight.abort.abort();
    const pending = this.view.pending;
    this.emit({
      uploadState: this.projection(pending),
      ...(pending && isUploadIntent(pending) ? { error: 'uncertain' as const } : {}),
    });
  };
  discardRejectedPdfImport = async (): Promise<void> => {
    if (this.promise || !this.current()) throw new Error('Library upload is still active or its account changed');
    await this.locked(() => {
      const pending = this.read();
      if (
        !pending ||
        !isUploadIntent(pending) ||
        pending.phase !== 'catalogue' ||
        !pending.catalogue_rejected ||
        !this.rejectedUpload ||
        encodePendingRecord(this.ownerId, pending) !== this.rejectedUpload
      ) {
        this.rejectedUpload = undefined;
        this.emit({
          pending,
          action: pending ?? undefined,
          error: pending ? 'uncertain' : null,
          uploadState: this.projection(pending),
        });
        throw new Error('The original Library import is not definitely rejected');
      }
      this.settle(pending, true, null);
    });
  };
}

const controllers = new Map<string, LiteratureMutationController>();
registerRendererAccountReset('literature-mutation-owner', () => controllers.clear());
export function getLiteratureMutationController(ownerId: string) {
  const key = getRendererAccountScopeToken();
  let controller = controllers.get(key);
  if (!controller) {
    controller = new LiteratureMutationController(ownerId);
    controllers.set(key, controller);
  }
  return controller;
}
