/** Small ordinary-input wire contract shared by IPC, queue and initial-message decoders. */
export type SpecialistBranch = Readonly<{ id: string; generation: number }>;
export type SpecialistScope = Readonly<{
  frame_id: string;
  frame_incarnation_id: string;
  root_frame_id: string;
  root_incarnation_id: string;
}>;
export type SpecialistInputReference = Readonly<{
  scope: SpecialistScope;
  branch: SpecialistBranch;
  revision: number;
  snapshot_id: string;
}>;
export type SpecialistSendAuthority = Readonly<{ reference?: SpecialistInputReference; targetAgent?: string }>;
const invalid = (): never => {
  throw new Error('Invalid specialist input reference');
};
const object = (v: unknown): Record<string, unknown> =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : invalid();
const id = (v: unknown, empty = false): string =>
  typeof v === 'string' &&
  v.length <= 512 &&
  ((empty && v === '') ||
    (v.length > 0 && v.trim() === v && ![...v].some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)))
    ? v
    : invalid();
const integer = (v: unknown) => (typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 ? v : invalid());
export function decodeSpecialistInputReference(value: unknown): SpecialistInputReference {
  const row = object(value),
    scope = object(row.scope),
    branch = object(row.branch);
  const revision = integer(row.revision),
    generation = integer(branch.generation),
    branchId = id(branch.id, true);
  if (!revision || (branchId === '') !== (generation === 0)) invalid();
  return Object.freeze({
    scope: Object.freeze({
      frame_id: id(scope.frame_id),
      frame_incarnation_id: id(scope.frame_incarnation_id),
      root_frame_id: id(scope.root_frame_id),
      root_incarnation_id: id(scope.root_incarnation_id),
    }),
    branch: Object.freeze({ id: branchId, generation }),
    revision,
    snapshot_id: id(row.snapshot_id),
  });
}
export function decodeSpecialistSendAuthority(value: unknown): SpecialistSendAuthority {
  const row = object(value);
  if (row.reference === undefined) {
    if (row.targetAgent !== undefined) invalid();
    return Object.freeze({});
  }
  return Object.freeze({
    reference: decodeSpecialistInputReference(row.reference),
    ...(row.targetAgent === undefined ? {} : { targetAgent: id(row.targetAgent) }),
  });
}
