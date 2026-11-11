import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { projectLibraryRestoreSummary } from '@/renderer/pages/library/useLibraryRestoreSelection';
import { decodeLiteratureRestorePreview } from '@/common/adapter/literatureSelection';
import {
  LiteratureMutationController,
  type LiteratureMutationView,
} from '@/renderer/services/runtime/LiteratureMutationController';
import {
  createLiteratureRestoreBulk,
  decodeLiteratureRestoreBulk,
  isLiteratureRestoreBulk,
  literatureRestoreBulkCodec,
  type LiteratureRestoreCommand,
} from '@/renderer/services/runtime/literatureRestoreBulkIntent';
import { decodePendingRecord, encodePendingRecord } from '@/renderer/services/runtime/literatureUploadIntent';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';

const owner = 'restore-controller-owner';
const key = `x-science.literature-mutation.v1:${owner}`;
const targets = (n = 3) => Array.from({ length: n }, (_, i) => ({ id: `restore-${i}`, metadata_revision: i + 2 }));
function uuid() {
  let n = 0;
  return () => `00000000-0000-4000-8000-${String(++n).padStart(12, '0')}`;
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
const ack = (command: LiteratureRestoreCommand): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1' as const,
  operation_id: command.operation_id,
  item: {
    id: command.item_id,
    title: command.item_id,
    metadata_revision: command.expected_revision + 1,
    deleted: false,
    metadata: {},
    project_ids: [],
    attachments: [],
  },
});
function pending(storage: ReturnType<typeof literaturePendingStorage>) {
  const value = decodePendingRecord(JSON.parse(storage.getItem(key)!), owner);
  if (!isLiteratureRestoreBulk(value)) throw new Error('Expected original restore intent');
  return value;
}
function refusal(status: number) {
  return new BackendHttpError({ method: 'PATCH', path: '/api/literature/items/restore-1', status, body: {} });
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner(owner);
  installLiteratureLocks();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

it('requires a closed complete revision snapshot and immutable strict restore commands and exact ACKs', () => {
  const preview = { schema: 'x-science.literature-restore-preview.v1', items: targets(), total_count: 3 };
  expect(decodeLiteratureRestorePreview(preview).items).toEqual(targets());
  for (const value of [
    { ...preview, total_count: 2 },
    { ...preview, private: {} },
    { ...preview, items: [targets()[0], targets()[0]], total_count: 2 },
    { ...preview, items: [{ id: 'a', metadata_revision: 0 }], total_count: 1 },
    { ...preview, items: [{ ...targets()[0], notes: 'private' }], total_count: 1 },
  ])
    expect(() => decodeLiteratureRestorePreview(value)).toThrow();
  const intent = createLiteratureRestoreBulk(targets(), 'trash', uuid());
  expect(decodeLiteratureRestoreBulk(JSON.parse(JSON.stringify(intent)))).toEqual(intent);
  expect(() =>
    decodeLiteratureRestoreBulk({ ...intent, commands: [{ ...intent.commands[0], title: 'overwritten' }] })
  ).toThrow();
  const command = intent.commands[0];
  for (const value of [
    { ...ack(command), operation_id: intent.operation_id },
    { ...ack(command), item: { ...ack(command).item, id: 'other' } },
    { ...ack(command), item: { ...ack(command).item, deleted: true } },
    { ...ack(command), item: { ...ack(command).item, metadata_revision: command.expected_revision } },
  ])
    expect(() => literatureRestoreBulkCodec.receipt(value, command)).toThrow();
});

it('persists one original slot before sending, persists each exact ACK before its successor, and prevents duplicate starts', async () => {
  const storage = literaturePendingStorage();
  const gate = deferred<void>();
  const wire: LiteratureRestoreCommand[] = [];
  const controller = new LiteratureMutationController(owner, {
    storage,
    uuid: uuid(),
    run: async (input) => {
      const command = input as LiteratureRestoreCommand;
      const saved = pending(storage);
      expect(saved.phase).toBe('sending');
      expect(saved.commands[saved.confirmed_items]).toEqual(command);
      expect(saved.confirmed_items).toBe(wire.length);
      wire.push(command);
      if (wire.length === 1) await gate.promise;
      return ack(command);
    },
  });
  const running = controller.startRestoreBulk(targets(), 'trash');
  await vi.waitFor(() => expect(wire).toHaveLength(1));
  expect(() => controller.startRestoreBulk(targets(), 'trash')).toThrow();
  gate.resolve();
  await running;
  expect(wire).toHaveLength(3);
  expect(storage.getItem(key)).toBeNull();
  expect(controller.getSnapshot().receipt).toEqual(ack(wire[2]));
});

it('replays only the original unknown second child across reload and a rejected replay before any suffix', async () => {
  const storage = literaturePendingStorage();
  const committed = new Map<string, ReturnType<typeof ack>>();
  const wires: LiteratureRestoreCommand[] = [];
  const run = async (command: LiteratureRestoreCommand) => {
    wires.push(command);
    const receipt = committed.get(command.operation_id) ?? ack(command);
    committed.set(command.operation_id, receipt);
    return receipt;
  };
  const first = new LiteratureMutationController(owner, {
    storage,
    uuid: uuid(),
    run: async (input) => {
      const receipt = await run(input as LiteratureRestoreCommand);
      if (wires.length === 2) throw new TypeError('ACK lost after commit');
      return receipt;
    },
  });
  await expect(first.startRestoreBulk(targets(), 'trash')).rejects.toThrow('ACK lost');
  const original = pending(storage);
  expect(original).toMatchObject({ confirmed_items: 1, phase: 'sending', paused: true });
  const rejected = new LiteratureMutationController(owner, {
    storage,
    run: async (input) => {
      wires.push(input as LiteratureRestoreCommand);
      throw refusal(409);
    },
  });
  await expect(rejected.resumeRestoreBulk()).rejects.toBeInstanceOf(BackendHttpError);
  expect(pending(storage)).toEqual(original);
  await expect(rejected.discardRestoreBulk(original.operation_id)).rejects.toThrow();
  const restored = new LiteratureMutationController(owner, {
    storage,
    run: (input) => run(input as LiteratureRestoreCommand),
  });
  await restored.resumeRestoreBulk();
  expect(wires.map((command) => command.operation_id)).toEqual([
    original.commands[0].operation_id,
    original.commands[1].operation_id,
    original.commands[1].operation_id,
    original.commands[1].operation_id,
    original.commands[2].operation_id,
  ]);
  expect(committed.size).toBe(3);
});

it('keeps known revision refusal at the immutable revision and permits only explicit unsent discard', async () => {
  const storage = literaturePendingStorage();
  const run = vi.fn(async () => {
    throw refusal(409);
  });
  const controller = new LiteratureMutationController(owner, { storage, uuid: uuid(), run });
  await expect(controller.startRestoreBulk(targets(), 'trash')).rejects.toBeInstanceOf(BackendHttpError);
  const saved = pending(storage);
  expect(saved).toMatchObject({ confirmed_items: 0, phase: 'ready', paused: true });
  expect(saved.commands.map((command) => command.expected_revision)).toEqual([2, 3, 4]);
  expect(run).toHaveBeenCalledTimes(1);
  await controller.discardRestoreBulk(saved.operation_id);
  expect(storage.getItem(key)).toBeNull();
});

it('never sends without durable intent and never sends a successor after an ACK cursor write fails', async () => {
  const storage = literaturePendingStorage();
  const run = vi.fn(async (input) => ack(input as LiteratureRestoreCommand));
  const write = storage.setItem.bind(storage);
  storage.setItem = () => {
    throw new Error('quota');
  };
  const first = new LiteratureMutationController(owner, { storage, uuid: uuid(), run });
  await expect(first.startRestoreBulk(targets(), 'trash')).rejects.toThrow();
  expect(run).not.toHaveBeenCalled();
  storage.setItem = (key, value) => {
    if (JSON.parse(value).bulk?.confirmed_items === 1) throw new Error('cursor quota');
    write(key, value);
  };
  const next = new LiteratureMutationController(owner, { storage, uuid: uuid(), run });
  await expect(next.startRestoreBulk(targets(), 'trash')).rejects.toThrow();
  expect(run).toHaveBeenCalledTimes(1);
  expect(pending(storage)).toMatchObject({ confirmed_items: 0, phase: 'sending' });
});

it('stops only after an in-flight real ACK and leaves the original suffix ready to resume', async () => {
  const storage = literaturePendingStorage();
  const gate = deferred<void>();
  const wire: LiteratureRestoreCommand[] = [];
  const controller = new LiteratureMutationController(owner, {
    storage,
    uuid: uuid(),
    run: async (input) => {
      const command = input as LiteratureRestoreCommand;
      wire.push(command);
      if (wire.length === 1) await gate.promise;
      return ack(command);
    },
  });
  const running = controller.startRestoreBulk(targets(), 'trash');
  await vi.waitFor(() => expect(wire).toHaveLength(1));
  await controller.pauseRestoreBulk(pending(storage).operation_id);
  gate.resolve();
  await running;
  expect(wire).toHaveLength(1);
  expect(pending(storage)).toMatchObject({ confirmed_items: 1, phase: 'ready', paused: true });
  await controller.resumeRestoreBulk();
  expect(wire).toHaveLength(3);
});

it('never refills a cleared slot or overwrites a newer original after a late ACK', async () => {
  const storage = literaturePendingStorage();
  const gate = deferred<void>();
  const run = vi.fn(async (input) => {
    await gate.promise;
    return ack(input as LiteratureRestoreCommand);
  });
  const controller = new LiteratureMutationController(owner, { storage, uuid: uuid(), run });
  const running = controller.startRestoreBulk(targets(), 'trash');
  await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
  const newer = createLiteratureRestoreBulk([{ id: 'new', metadata_revision: 6 }], 'other-trash', () =>
    crypto.randomUUID()
  );
  storage.setItem(key, encodePendingRecord(owner, newer));
  gate.resolve();
  await expect(running).rejects.toThrow();
  expect(pending(storage)).toEqual(newer);
  expect(run).toHaveBeenCalledTimes(1);
});

it('isolates account reset and retains a sending child for its original account when a late ACK settles', async () => {
  const storage = literaturePendingStorage();
  const gate = deferred<void>();
  const run = vi.fn(async (input) => {
    await gate.promise;
    return ack(input as LiteratureRestoreCommand);
  });
  const controller = new LiteratureMutationController(owner, { storage, uuid: uuid(), run });
  const running = controller.startRestoreBulk(targets(), 'trash');
  await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
  const original = storage.getItem(key);
  setRendererAccountOwner('different-account');
  await expect(running).rejects.toMatchObject({ name: 'AbortError' });
  gate.resolve();
  await Promise.resolve();
  expect(storage.getItem(key)).toBe(original);
  expect(run).toHaveBeenCalledTimes(1);
});

it('replays the original final child after a lost clear without fabricating a terminal receipt', async () => {
  const storage = literaturePendingStorage();
  const remove = storage.removeItem.bind(storage);
  storage.removeItem = () => {
    throw new Error('clear unavailable');
  };
  const wire: LiteratureRestoreCommand[] = [];
  const run = async (input: unknown) => {
    const command = input as LiteratureRestoreCommand;
    wire.push(command);
    return ack(command);
  };
  const controller = new LiteratureMutationController(owner, { storage, uuid: uuid(), run });
  await expect(controller.startRestoreBulk(targets(2), 'trash')).rejects.toThrow();
  expect(pending(storage)).toMatchObject({ confirmed_items: 2, phase: 'ready' });
  storage.removeItem = remove;
  const restored = new LiteratureMutationController(owner, { storage, run });
  await restored.resumeRestoreBulk();
  expect(wire).toHaveLength(3);
  expect(wire[2]).toEqual(wire[1]);
  expect(storage.getItem(key)).toBeNull();
});

it('projects only same verified restore child progress and releases every identity, pause, error or settlement boundary', () => {
  const plan = createLiteratureRestoreBulk(targets(), 'trash', uuid());
  const view: LiteratureMutationView = {
    pending: plan,
    action: plan,
    mutating: true,
    error: null,
    uploadState: null,
    canDiscardRejectedUpload: false,
  };
  const advanced = decodeLiteratureRestoreBulk({ ...plan, confirmed_items: 1, phase: 'sending' });
  const next = { ...view, pending: advanced, action: advanced, receipt: ack(plan.commands[0]) };
  expect(projectLibraryRestoreSummary(next, view)).toBe(view);
  const revised = decodeLiteratureRestoreBulk({
    ...advanced,
    commands: advanced.commands.map((command, i) =>
      i === 1 ? { ...command, expected_revision: command.expected_revision + 1 } : command
    ),
  });
  const children = decodeLiteratureRestoreBulk({
    ...advanced,
    commands: advanced.commands.map((command, i) =>
      i === 1 ? { ...command, operation_id: crypto.randomUUID() } : command
    ),
  });
  const paused = decodeLiteratureRestoreBulk({ ...advanced, paused: true });
  for (const changed of [
    { ...next, pending: revised, action: revised },
    { ...next, pending: children, action: children },
    { ...next, pending: paused, action: paused },
    { ...next, pending: null },
    { ...next, mutating: false },
    ...(['uncertain', 'rejected', 'storage'] as const).map((error) => Object.assign({}, next, { error })),
    {
      ...next,
      action: createLiteratureRestoreBulk([{ id: 'different', metadata_revision: 3 }], 'other', () =>
        crypto.randomUUID()
      ),
    },
    { ...next, receipt: { ...ack(plan.commands[0]), operation_id: crypto.randomUUID() } },
  ])
    expect(projectLibraryRestoreSummary(changed, view)).toBe(changed);
});
