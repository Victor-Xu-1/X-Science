import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  LIBRARY_COLUMN_IDS,
  LIBRARY_COLUMN_V2_IDS,
  liftLibraryColumnPreferences,
  parseLibraryColumnPreferences,
  parseLibraryColumnPreferencesWire,
  projectLibraryColumnPreferences,
  projectLibraryNotesColumns,
  type LibraryColumnWireId,
  type LibraryColumnPreferences,
  type LibraryColumnPreferencesWire,
  type LibraryColumnPreferencesV2,
} from '@/common/adapter/libraryColumnPreferences';

export type LibraryColumnPreferenceStatus = 'loading' | 'ready' | 'saving' | 'invalid' | 'read-error' | 'write-error';
export type LibraryColumnPreferenceView = Readonly<{
  preferences: LibraryColumnPreferences;
  notesPreferences?: LibraryColumnPreferencesV2;
  status: LibraryColumnPreferenceStatus;
}>;
export type LibraryColumnPreferenceTransport = {
  load(signal: AbortSignal): Promise<unknown>;
  save(value: LibraryColumnPreferencesWire, signal: AbortSignal): Promise<void>;
};

const sameOrder = (left: readonly LibraryColumnWireId[], right: readonly LibraryColumnWireId[]) =>
  left.length === right.length && left.every((id, index) => id === right[index]);

/** One mounted view owns its requests; the server setting remains last-write-wins, not CAS. */
export class LibraryColumnPreferenceOwner {
  private view: LibraryColumnPreferenceView = { preferences: DEFAULT_LIBRARY_COLUMN_PREFERENCES, status: 'loading' };
  private listeners = new Set<() => void>();
  private active = true;
  private request: AbortController | null = null;
  private reloadQueued = false;
  private loaded = false;
  private blocked = false;
  private revision = 0;
  private order?: { value: readonly LibraryColumnWireId[]; revision: number };
  private visibility = new Map<LibraryColumnWireId, { value: boolean; revision: number }>();
  private confirmed: LibraryColumnPreferencesWire = DEFAULT_LIBRARY_COLUMN_PREFERENCES;

  constructor(
    private readonly transport: LibraryColumnPreferenceTransport,
    private readonly isCurrent: () => boolean,
    private readonly notesEnabled = false
  ) {}

  getSnapshot = (): LibraryColumnPreferenceView => this.view;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  private current() {
    return this.active && this.isCurrent();
  }
  private settledStatus(): LibraryColumnPreferenceStatus {
    if (!this.loaded) return 'loading';
    if (this.dirty()) return this.blocked ? 'write-error' : 'saving';
    return 'ready';
  }
  private publish(status = this.settledStatus()) {
    if (!this.current()) return;
    const merged = this.merge(this.confirmed);
    this.view = {
      preferences: projectLibraryColumnPreferences(merged),
      ...(this.notesEnabled ? { notesPreferences: projectLibraryNotesColumns(merged) } : {}),
      status,
    };
    this.listeners.forEach((listener) => listener());
  }
  private merge(base: LibraryColumnPreferencesWire): LibraryColumnPreferencesWire {
    const supported = this.notesEnabled ? projectLibraryNotesColumns(base) : projectLibraryColumnPreferences(base);
    const order = this.order?.value ?? supported.order;
    const visible = supported.visible as readonly LibraryColumnWireId[];
    if (this.notesEnabled)
      return {
        version: 2,
        order,
        visible: order.filter((id) => this.visibility.get(id)?.value ?? visible.includes(id)),
      };
    const legacyOrder = order.filter((id) => id !== 'notes');
    return liftLibraryColumnPreferences(base, {
      version: 1,
      order: legacyOrder,
      visible: legacyOrder.filter((id) => this.visibility.get(id)?.value ?? visible.includes(id)),
    });
  }
  private dirty() {
    return this.order !== undefined || this.visibility.size > 0;
  }
  private receive(value: unknown): boolean {
    const remote = value === undefined ? DEFAULT_LIBRARY_COLUMN_PREFERENCES : parseLibraryColumnPreferencesWire(value);
    if (!remote) return false;
    this.confirmed = remote;
    this.loaded = true;
    // A read can confirm a lost PUT response, or an equal change from another view.
    const supported = this.notesEnabled ? projectLibraryNotesColumns(remote) : projectLibraryColumnPreferences(remote);
    const visible = supported.visible as readonly LibraryColumnWireId[];
    if (this.order && sameOrder(this.order.value, supported.order)) this.order = undefined;
    for (const [id, intent] of this.visibility) if (intent.value === visible.includes(id)) this.visibility.delete(id);
    return true;
  }
  private finish(request: AbortController) {
    if (this.request !== request) return;
    this.request = null;
    if (!this.current()) return;
    if (this.reloadQueued) {
      this.reloadQueued = false;
      this.reload();
    } else if (this.loaded && this.dirty() && !this.blocked) void this.flush();
  }

  start = (): void => {
    this.active = true;
    this.reload();
  };
  reload = (): void => {
    if (!this.current()) return;
    if (this.request) {
      this.reloadQueued = true;
      return;
    }
    const request = new AbortController();
    this.request = request;
    this.publish('loading');
    void this.read(request);
  };
  private async read(request: AbortController) {
    try {
      const remote = await this.transport.load(request.signal);
      if (!this.current() || request.signal.aborted) return;
      if (!this.receive(remote)) {
        this.blocked = true;
        this.publish('invalid');
        return;
      }
      if (!this.dirty()) this.blocked = false;
      this.publish();
    } catch {
      if (this.current() && !request.signal.aborted) {
        this.blocked = true;
        this.publish('read-error');
      }
    } finally {
      this.finish(request);
    }
  }

  change = (value: LibraryColumnPreferencesWire): void => {
    if (!this.current() || this.view.status === 'invalid' || this.view.status === 'read-error') return;
    const next = this.notesEnabled ? parseLibraryColumnPreferencesWire(value) : parseLibraryColumnPreferences(value);
    if (!next) return;
    if (this.notesEnabled && next.version !== 2) return;
    const previous = this.notesEnabled
      ? (this.view.notesPreferences ?? projectLibraryNotesColumns(this.view.preferences))
      : this.view.preferences;
    const nextVisible = next.visible as readonly LibraryColumnWireId[];
    const previousVisible = previous.visible as readonly LibraryColumnWireId[];
    const revision = ++this.revision;
    if (!sameOrder(previous.order, next.order)) this.order = { value: next.order, revision };
    for (const id of this.notesEnabled ? LIBRARY_COLUMN_V2_IDS : LIBRARY_COLUMN_IDS)
      if (previousVisible.includes(id) !== nextVisible.includes(id))
        this.visibility.set(id, { value: nextVisible.includes(id), revision });
    this.publish();
    if (!this.request && this.loaded && !this.blocked && this.dirty()) void this.flush();
  };
  reset = (): void => {
    if (!this.current() || this.request) return;
    const revision = ++this.revision;
    const defaults = this.notesEnabled
      ? projectLibraryNotesColumns(DEFAULT_LIBRARY_COLUMN_PREFERENCES)
      : DEFAULT_LIBRARY_COLUMN_PREFERENCES;
    const visible = defaults.visible as readonly LibraryColumnWireId[];
    this.order = { value: defaults.order, revision };
    for (const id of this.notesEnabled ? LIBRARY_COLUMN_V2_IDS : LIBRARY_COLUMN_IDS)
      this.visibility.set(id, { value: visible.includes(id), revision });
    this.loaded = true;
    this.blocked = false;
    void this.flush();
  };
  retry = (): void => {
    if (!this.current() || this.request) return;
    this.blocked = false;
    // Never repeat a PUT with an unknown outcome before reading its durable result.
    this.reload();
  };
  private async flush() {
    if (!this.current() || this.request || !this.loaded || this.blocked || !this.dirty()) return;
    const request = new AbortController();
    this.request = request;
    const revision = this.revision;
    const sent = this.merge(this.confirmed);
    this.publish('saving');
    try {
      await this.transport.save(sent, request.signal);
      if (!this.current() || request.signal.aborted) return;
      this.confirmed = sent;
      if (this.order && this.order.revision <= revision) this.order = undefined;
      for (const [id, intent] of this.visibility) if (intent.revision <= revision) this.visibility.delete(id);
      this.publish();
    } catch {
      if (!this.current() || request.signal.aborted) return;
      this.blocked = true;
      // One bounded readback only. A failed/mismatching read requires explicit retry.
      try {
        const remote = await this.transport.load(request.signal);
        if (!this.current() || request.signal.aborted) return;
        if (!this.receive(remote)) {
          this.publish('invalid');
          return;
        }
        if (!this.dirty()) this.blocked = false;
      } catch {
        // Keep the intended layout visible and explicitly unsaved.
      }
      if (this.current() && !request.signal.aborted) this.publish();
    } finally {
      this.finish(request);
    }
  }
  dispose = (): void => {
    this.active = false;
    this.request?.abort();
    this.request = null;
    this.reloadQueued = false;
    this.listeners.clear();
  };
}
