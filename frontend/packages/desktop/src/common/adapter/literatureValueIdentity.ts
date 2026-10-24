const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const fail = (): never => {
  throw new Error('Invalid literature Library contract');
};

/** Canonical JSON equality for immutable requests and acknowledgements, never an execution authority. */
export function literatureValueIdentity(value: unknown, depth = 0): string {
  if (depth > 16) return fail();
  if (Array.isArray(value)) return `[${value.map((child) => literatureValueIdentity(child, depth + 1)).join(',')}]`;
  if (record(value))
    return `{${Object.entries(value)
      .toSorted(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0))
      .map(([key, child]) => `${JSON.stringify(key)}:${literatureValueIdentity(child, depth + 1)}`)
      .join(',')}}`;
  if (
    value === null ||
    typeof value === 'string' ||
    typeof value === 'boolean' ||
    (typeof value === 'number' && Number.isFinite(value))
  )
    return JSON.stringify(value);
  return fail();
}
