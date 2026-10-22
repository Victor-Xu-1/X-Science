import { isBackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureCommand,
  literatureValueIdentity,
  type LiteratureCommand,
  type LiteratureReceipt,
} from '@/common/adapter/literatureLibrary';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '../rendererAccountScope';
import { runLiteratureCommand } from './literatureLibrary';

export type LiteratureMutationView = {
  pending: LiteratureCommand | null;
  mutating: boolean;
  error: 'uncertain' | 'rejected' | 'storage' | null;
  receipt?: LiteratureReceipt;
};

/** One renderer-account mutation owner. The server ledger remains the commit authority. */
export class LiteratureMutationController {
  private view: LiteratureMutationView = { pending: null, mutating: false, error: null };
  private listeners = new Set<() => void>();
  private readonly account = getRendererAccountScopeToken();
  private promise?: Promise<LiteratureReceipt>;
  private readonly key: string;
  constructor(
    private ownerId: string,
    private options: {
      storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
      run?: typeof runLiteratureCommand;
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
  restore = () => {
    if (!this.current() || this.promise) return;
    try {
      const raw = this.storage().getItem(this.key);
      if (!raw) {
        this.emit({ pending: null, error: null });
        return;
      }
      const value: unknown = JSON.parse(raw);
      if (
        !value ||
        typeof value !== 'object' ||
        !('ownerId' in value) ||
        value.ownerId !== this.ownerId ||
        !('command' in value)
      )
        throw new Error('Library pending request scope changed');
      this.emit({ pending: decodeLiteratureCommand(value.command), error: 'uncertain' });
    } catch {
      this.emit({ error: 'storage' });
    }
  };
  submit = (input: LiteratureCommand): Promise<LiteratureReceipt> => {
    if (!this.current()) return Promise.reject(new Error('Library account changed'));
    const command = decodeLiteratureCommand(input);
    if (this.view.error === 'storage') return Promise.reject(new Error('Library pending storage unavailable'));
    if (this.view.pending && literatureValueIdentity(this.view.pending) !== literatureValueIdentity(command))
      return Promise.reject(new Error('Resolve the original Library change first'));
    if (this.promise) return this.promise;
    try {
      // Persist before any write. A lost acknowledgement can replay only this exact command.
      this.storage().setItem(this.key, JSON.stringify({ ownerId: this.ownerId, command }));
    } catch {
      this.emit({ error: 'storage' });
      return Promise.reject(new Error('Library pending storage unavailable'));
    }
    this.emit({ pending: command, mutating: true, error: null });
    const promise = (this.options.run ?? runLiteratureCommand)(command)
      .then((receipt) => {
        if (!this.current()) throw new Error('Library account changed');
        try {
          this.storage().removeItem(this.key);
        } catch {
          this.emit({ error: 'storage' });
          throw new Error('Library receipt storage unavailable');
        }
        this.emit({ pending: null, error: null, receipt });
        return receipt;
      })
      .catch((error: unknown) => {
        if (this.current() && this.view.error !== 'storage') {
          const rejected = isBackendHttpError(error) && error.status >= 400 && error.status < 500;
          if (rejected) {
            try {
              this.storage().removeItem(this.key);
            } catch {
              this.emit({ error: 'storage' });
              throw error;
            }
          }
          this.emit({ pending: rejected ? null : command, error: rejected ? 'rejected' : 'uncertain' });
        }
        throw error;
      })
      .finally(() => {
        if (this.promise === promise) {
          this.promise = undefined;
          this.emit({ mutating: false });
        }
      });
    this.promise = promise;
    return promise;
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
