import { isBackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureCommand,
  literatureValueIdentity,
  type LiteratureCommand,
  type LiteratureReceipt,
} from '@/common/adapter/literatureLibrary';
import {
  decodeLiteratureCollectionCommand,
  type LiteratureCollectionCommand,
  type LiteratureCollectionReceipt,
} from '@/common/adapter/literatureCollections';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '../rendererAccountScope';
import { runLiteratureCommand } from './literatureLibrary';
import { runLiteratureCollectionCommand } from './literatureCollections';
import { withLiteratureMutationLock } from './literatureMutationLock';

class OriginalPendingConflict extends Error {
  constructor() {
    super('Resolve the original Library change first');
  }
}

export type LiteratureMutationCommand = LiteratureCommand | LiteratureCollectionCommand;
export type LiteratureMutationReceipt = LiteratureReceipt | LiteratureCollectionReceipt;
type ReceiptFor<Command extends LiteratureMutationCommand> = Command extends LiteratureCollectionCommand
  ? LiteratureCollectionReceipt
  : LiteratureReceipt;

const isCollectionCommand = (command: LiteratureMutationCommand): command is LiteratureCollectionCommand =>
  command.kind.startsWith('collection-');
const decodeMutationCommand = (value: unknown): LiteratureMutationCommand => {
  if (
    value &&
    typeof value === 'object' &&
    'kind' in value &&
    typeof value.kind === 'string' &&
    value.kind.startsWith('collection-')
  )
    return decodeLiteratureCollectionCommand(value);
  return decodeLiteratureCommand(value);
};

export type LiteratureMutationView = {
  pending: LiteratureMutationCommand | null;
  mutating: boolean;
  error: 'uncertain' | 'rejected' | 'storage' | null;
  receipt?: LiteratureMutationReceipt;
};

/** One renderer-account mutation owner. The server ledger remains the commit authority. */
export class LiteratureMutationController {
  private view: LiteratureMutationView = { pending: null, mutating: false, error: null };
  private listeners = new Set<() => void>();
  private readonly account = getRendererAccountScopeToken();
  private promise?: Promise<LiteratureMutationReceipt>;
  private flightCommand?: LiteratureMutationCommand;
  private readonly key: string;
  constructor(
    private ownerId: string,
    private options: {
      storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
      run?: typeof runLiteratureCommand;
      runCollection?: typeof runLiteratureCollectionCommand;
    } = {}
  ) {
    this.key = `x-science.literature-mutation.v1:${encodeURIComponent(ownerId)}`;
    this.restore();
  }
  getSnapshot = () => this.view;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private current() {
    return Boolean(this.ownerId) && this.account === getRendererAccountScopeToken();
  }
  private storage() {
    return this.options.storage ?? localStorage;
  }
  private emit(patch: Partial<LiteratureMutationView>) {
    if (!this.current()) return;
    this.view = { ...this.view, ...patch };
    for (const listener of this.listeners) listener();
  }
  private readPending(): LiteratureMutationCommand | null {
    const raw = this.storage().getItem(this.key);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (
      !value ||
      typeof value !== 'object' ||
      !('ownerId' in value) ||
      value.ownerId !== this.ownerId ||
      !('command' in value)
    )
      throw new Error('Library pending request scope changed');
    return decodeMutationCommand(value.command);
  }
  private sameCommand(left: LiteratureMutationCommand, right: LiteratureMutationCommand) {
    return literatureValueIdentity(left) === literatureValueIdentity(right);
  }
  private async locked(critical: () => void) {
    try {
      await withLiteratureMutationLock(this.key, () => this.current(), critical);
    } catch (error) {
      if (error instanceof OriginalPendingConflict) throw error;
      this.emit({ error: 'storage' });
      throw new Error('Library pending storage unavailable', { cause: error });
    }
  }
  private reserve(command: LiteratureMutationCommand) {
    return this.locked(() => {
      const pending = this.readPending();
      if (pending && !this.sameCommand(pending, command)) {
        this.emit({ pending, error: 'uncertain' });
        throw new OriginalPendingConflict();
      }
      // Persist before transport. RAM state is not cross-renderer admission authority.
      this.storage().setItem(this.key, JSON.stringify({ ownerId: this.ownerId, command }));
      this.emit({ pending: command, error: null });
    });
  }
  private settle(
    command: LiteratureMutationCommand,
    outcome: 'ack' | 'rejected' | 'uncertain',
    receipt?: LiteratureMutationReceipt
  ) {
    return this.locked(() => {
      const pending = this.readPending();
      const matching = pending && this.sameCommand(pending, command);
      if (matching && outcome !== 'uncertain') this.storage().removeItem(this.key);
      // A duplicate response can arrive after another renderer has admitted its next operation.
      // Project that durable operation, never remove it or resurrect the completed original.
      const remaining = matching && outcome !== 'uncertain' ? null : pending;
      this.emit({
        pending: remaining,
        error: remaining ? 'uncertain' : outcome === 'rejected' ? 'rejected' : null,
        ...(receipt ? { receipt } : {}),
      });
    });
  }
  private async execute(command: LiteratureMutationCommand) {
    await this.reserve(command);
    if (!this.current()) throw new Error('Library account changed');
    let receipt: LiteratureMutationReceipt;
    try {
      // Deliberately outside the storage lock; synchronous transport exceptions are recoverable too.
      receipt = isCollectionCommand(command)
        ? await (this.options.runCollection ?? runLiteratureCollectionCommand)(command)
        : await (this.options.run ?? runLiteratureCommand)(command);
    } catch (error) {
      if (this.current()) {
        const rejected = isBackendHttpError(error) && error.status >= 400 && error.status < 500;
        await this.settle(command, rejected ? 'rejected' : 'uncertain');
      }
      throw error;
    }
    if (!this.current()) throw new Error('Library account changed');
    await this.settle(command, 'ack', receipt);
    return receipt;
  }
  restore = () => {
    if (!this.current() || this.promise) return;
    try {
      const pending = this.readPending();
      this.emit({ pending, error: pending ? 'uncertain' : null });
    } catch {
      this.emit({ error: 'storage' });
    }
  };
  submit = <Command extends LiteratureMutationCommand>(input: Command): Promise<ReceiptFor<Command>> => {
    if (!this.current()) return Promise.reject(new Error('Library account changed'));
    const command = decodeMutationCommand(input);
    if (this.promise) {
      return this.flightCommand && this.sameCommand(this.flightCommand, command)
        ? (this.promise as Promise<ReceiptFor<Command>>)
        : Promise.reject(new OriginalPendingConflict());
    }
    const promise = Promise.resolve()
      .then(() => this.execute(command))
      .finally(() => {
        if (this.promise === promise) {
          this.promise = undefined;
          this.flightCommand = undefined;
          this.emit({ mutating: false });
        }
      });
    this.promise = promise;
    this.flightCommand = command;
    // Establish the flight before notifying subscribers, which may synchronously submit again.
    this.emit({ mutating: true });
    return promise as Promise<ReceiptFor<Command>>;
  };
  retry = () =>
    this.view.pending ? this.submit(this.view.pending) : Promise.reject(new Error('No Library request to retry'));
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
