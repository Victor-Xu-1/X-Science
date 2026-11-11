import {
  decodeLiteratureCommand,
  decodeLiteratureReceipt,
  literatureValueIdentity,
  type LiteratureCommand,
  type LiteratureReceipt,
} from '@/common/adapter/literatureLibrary';
import { decodeLiteratureRestorePreview, type LiteratureRestoreTarget } from '@/common/adapter/literatureSelection';
import type { LiteratureBulkCodec } from './literatureBulkFlow';

export type LiteratureRestoreCommand = Extract<LiteratureCommand, { kind: 'edit' }> & { readonly deleted: false };
export type LiteratureRestoreBulkIntent = Readonly<{
  schema: 'x-science.library-restore-bulk.v1';
  kind: 'restore-bulk';
  operation_id: string;
  scope_key: string;
  commands: readonly LiteratureRestoreCommand[];
  confirmed_items: number;
  phase: 'ready' | 'sending';
  paused: boolean;
}>;
const fail = (): never => {
  throw new Error('Invalid Library restore bulk intent');
};
const keys = ['schema', 'kind', 'operation_id', 'scope_key', 'commands', 'confirmed_items', 'phase', 'paused'];
export function isLiteratureRestoreBulk(value: unknown): value is LiteratureRestoreBulkIntent {
  return Boolean(
    value &&
    typeof value === 'object' &&
    'schema' in value &&
    'kind' in value &&
    value.schema === 'x-science.library-restore-bulk.v1' &&
    value.kind === 'restore-bulk'
  );
}
export function decodeLiteratureRestoreBulk(value: unknown): LiteratureRestoreBulkIntent {
  if (
    !isLiteratureRestoreBulk(value) ||
    Reflect.ownKeys(value).length !== keys.length ||
    !Reflect.ownKeys(value).every((key) => typeof key === 'string' && keys.includes(key)) ||
    !Array.isArray(value.commands) ||
    !value.commands.length ||
    !Number.isSafeInteger(value.confirmed_items) ||
    value.confirmed_items < 0 ||
    value.confirmed_items > value.commands.length ||
    !['ready', 'sending'].includes(value.phase) ||
    typeof value.paused !== 'boolean' ||
    typeof value.scope_key !== 'string' ||
    !value.scope_key ||
    value.scope_key.trim() !== value.scope_key ||
    /[\p{Cc}\p{Cs}]/u.test(value.scope_key)
  )
    return fail();
  const ids = new Set<string>();
  const operations = new Set<string>();
  const commands = value.commands.map((input): LiteratureRestoreCommand => {
    if (
      !input ||
      typeof input !== 'object' ||
      Reflect.ownKeys(input).length !== 5 ||
      !Reflect.ownKeys(input).every(
        (key) =>
          typeof key === 'string' && ['kind', 'operation_id', 'item_id', 'expected_revision', 'deleted'].includes(key)
      )
    )
      return fail();
    const command = decodeLiteratureCommand(input);
    if (
      command.kind !== 'edit' ||
      command.deleted !== false ||
      command.expected_revision < 1 ||
      command.expected_revision === Number.MAX_SAFE_INTEGER ||
      ids.has(command.item_id) ||
      operations.has(command.operation_id)
    )
      return fail();
    ids.add(command.item_id);
    operations.add(command.operation_id);
    return Object.freeze({ ...command, deleted: false });
  });
  const root = decodeLiteratureCommand({ ...commands[0], operation_id: value.operation_id }).operation_id;
  if (operations.has(root)) return fail();
  return Object.freeze({
    schema: 'x-science.library-restore-bulk.v1',
    kind: 'restore-bulk',
    operation_id: root,
    scope_key: value.scope_key,
    commands: Object.freeze(commands),
    confirmed_items: value.confirmed_items,
    phase: value.phase,
    paused: value.paused,
  });
}
export function createLiteratureRestoreBulk(
  items: readonly LiteratureRestoreTarget[],
  scopeKey: string,
  uuid: () => string
): LiteratureRestoreBulkIntent {
  const checked = decodeLiteratureRestorePreview({
    schema: 'x-science.literature-restore-preview.v1',
    items,
    total_count: items.length,
  });
  return decodeLiteratureRestoreBulk({
    schema: 'x-science.library-restore-bulk.v1',
    kind: 'restore-bulk',
    operation_id: uuid(),
    scope_key: scopeKey,
    commands: checked.items.map((item) => ({
      kind: 'edit',
      operation_id: uuid(),
      item_id: item.id,
      expected_revision: item.metadata_revision,
      deleted: false,
    })),
    confirmed_items: 0,
    phase: 'ready',
    paused: false,
  });
}
function current(intent: LiteratureRestoreBulkIntent): LiteratureRestoreCommand {
  return intent.commands[Math.min(intent.confirmed_items, intent.commands.length - 1)] ?? fail();
}
function receipt(value: unknown, command: LiteratureRestoreCommand): LiteratureReceipt {
  const ack = decodeLiteratureReceipt(value, command.operation_id);
  if (
    ack.item.id !== command.item_id ||
    ack.item.deleted ||
    ack.item.metadata_revision !== command.expected_revision + 1 ||
    ack.attachment_id !== undefined
  )
    return fail();
  return ack;
}
const restoreOrigin = (intent: LiteratureRestoreBulkIntent) => ({
  ...intent,
  phase: 'ready',
  confirmed_items: 0,
  paused: false,
});

export const literatureRestoreBulkCodec: LiteratureBulkCodec<
  LiteratureRestoreBulkIntent,
  LiteratureRestoreCommand,
  LiteratureReceipt
> = {
  is: isLiteratureRestoreBulk,
  decode: decodeLiteratureRestoreBulk,
  claim: (intent) => (intent.phase === 'sending' ? intent : Object.freeze({ ...intent, phase: 'sending' })),
  current,
  receipt,
  advance: (intent, ack) => {
    if (intent.phase !== 'sending') return fail();
    receipt(ack, current(intent));
    return Object.freeze({
      ...intent,
      phase: 'ready',
      confirmed_items: Math.min(intent.confirmed_items + 1, intent.commands.length),
    });
  },
  position: (intent) => intent.confirmed_items,
  count: (intent) => intent.commands.length,
  sameOrigin: (left, right) => {
    return literatureValueIdentity(restoreOrigin(left)) === literatureValueIdentity(restoreOrigin(right));
  },
};
