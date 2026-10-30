import { literatureValueIdentity } from './literatureValueIdentity';
import { LITERATURE_METADATA_PATCH_BYTES } from './literatureMetadataCapacity';

export type LiteratureMetadataPatch = {
  set?: Record<string, unknown>;
  remove?: string[];
};

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const fail = (): never => {
  throw new Error('Invalid literature Library contract');
};

/** Copy actual JSON values before transport; never stringify invalid input into a different change. */
function snapshotValue(value: unknown, depth = 0): unknown {
  if (depth > 16) return fail();
  if (
    value === null ||
    typeof value === 'string' ||
    typeof value === 'boolean' ||
    (typeof value === 'number' && Number.isFinite(value))
  )
    return value;
  if (Array.isArray(value)) {
    if (value.length > 65536 || Reflect.ownKeys(value).length !== value.length + 1) return fail();
    return Array.from({ length: value.length }, (_, index) => {
      const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
      if (!descriptor || !('value' in descriptor) || !descriptor.enumerable) return fail();
      return snapshotValue(descriptor.value, depth + 1);
    });
  }
  if (!record(value) || (Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null))
    return fail();
  return Object.fromEntries(
    Reflect.ownKeys(value).map((key) => {
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (typeof key !== 'string' || !descriptor || !descriptor.enumerable || !('value' in descriptor)) return fail();
      return [key, snapshotValue(descriptor.value, depth + 1)];
    })
  );
}

/** Field actions are immutable request data, never a replacement of unedited metadata. */
export function decodeLiteratureMetadataPatch(value: unknown): LiteratureMetadataPatch {
  if (!record(value)) return fail();
  value = snapshotValue(value);
  if (!record(value) || Object.keys(value).some((key) => key !== 'set' && key !== 'remove')) return fail();
  const hasSet = Object.hasOwn(value, 'set');
  const hasRemove = Object.hasOwn(value, 'remove');
  if ((hasSet && !record(value.set)) || (hasRemove && !Array.isArray(value.remove))) return fail();
  const set = hasSet ? (value.set as Record<string, unknown>) : undefined;
  const remove = hasRemove ? (value.remove as unknown[]) : undefined;
  if (remove && (!remove.every((key) => typeof key === 'string') || new Set(remove).size !== remove.length))
    return fail();
  if (remove?.some((key) => set && Object.hasOwn(set, key as string))) return fail();
  if (!Object.keys(set ?? {}).length && !remove?.length) return fail();
  const patch: LiteratureMetadataPatch = {
    ...(set !== undefined ? { set } : {}),
    ...(remove !== undefined ? { remove: [...remove] as string[] } : {}),
  };
  if (new TextEncoder().encode(JSON.stringify(patch)).length > LITERATURE_METADATA_PATCH_BYTES) return fail();
  return patch;
}

/** Only requested keys participate in ACK equality; untouched values may exceed JS number range. */
export function literatureMetadataPatchMatches(
  metadata: Record<string, unknown>,
  patch: LiteratureMetadataPatch
): boolean {
  return (
    Object.entries(patch.set ?? {}).every(
      ([key, value]) =>
        Object.hasOwn(metadata, key) && literatureValueIdentity(metadata[key]) === literatureValueIdentity(value)
    ) && (patch.remove ?? []).every((key) => !Object.hasOwn(metadata, key))
  );
}
