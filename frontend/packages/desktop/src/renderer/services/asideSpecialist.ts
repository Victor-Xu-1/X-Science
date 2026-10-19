import {
  decodeSpecialistInputReference,
  decodeSpecialistSendAuthority,
  type SpecialistInputReference,
  type SpecialistSendAuthority,
} from '@/common/adapter/specialistBinding';
import { sameSpecialistBranch, sameSpecialistScope } from './runtime/sessionSpecialistTypes';
import type { XScienceAsideIntent, XScienceAsideReceipt } from './xScienceAside';

export function sameAsideSpecialist(a?: SpecialistInputReference, b?: SpecialistInputReference): boolean {
  return a && b
    ? a.revision === b.revision &&
        a.snapshot_id === b.snapshot_id &&
        sameSpecialistScope(a.scope, b.scope) &&
        sameSpecialistBranch(a.branch, b.branch)
    : a === b;
}

/** Compare the original definition, never a mutable selector or display name. */
export function asideSourceMatches(authority: SpecialistSendAuthority | undefined, source?: SpecialistInputReference) {
  try {
    return sameAsideSpecialist(decodeSpecialistSendAuthority(authority ?? {}).reference, source);
  } catch {
    return false;
  }
}

export function freezeAsideSpecialist(value: unknown, parent: string): SpecialistInputReference | undefined {
  if (value === undefined) return undefined;
  const reference = decodeSpecialistInputReference(value);
  if (
    reference.scope.frame_id !== parent ||
    reference.scope.root_frame_id !== parent ||
    reference.scope.frame_incarnation_id !== reference.scope.root_incarnation_id ||
    !reference.branch.id ||
    reference.branch.generation < 1
  )
    throw new Error('aside_source_unavailable');
  return reference;
}

/** Both halves are evidence of the same original transaction, not a current-head projection. */
export function decodeAsideSpecialistProof(
  value: Record<string, unknown>,
  intent: XScienceAsideIntent,
  childFrameId?: string
): Pick<XScienceAsideReceipt, 'sourceSpecialist' | 'specialistBinding'> {
  if (!intent.sourceSpecialist) {
    if (value.source_specialist !== undefined || value.specialist_binding !== undefined)
      throw new Error('aside_specialist_unverified');
    return {};
  }
  const source = decodeSpecialistInputReference(value.source_specialist);
  if (!sameAsideSpecialist(source, intent.sourceSpecialist)) throw new Error('aside_specialist_unverified');
  if (!childFrameId) {
    if (value.specialist_binding !== undefined) throw new Error('aside_specialist_unverified');
    return { sourceSpecialist: source };
  }
  const child = decodeSpecialistInputReference(value.specialist_binding);
  if (
    child.scope.frame_id !== childFrameId ||
    child.scope.root_frame_id !== childFrameId ||
    child.scope.frame_incarnation_id !== child.scope.root_incarnation_id ||
    childFrameId === intent.parentFrameId ||
    child.revision !== 1 ||
    !child.branch.id ||
    child.branch.generation < 1 ||
    child.snapshot_id !== source.snapshot_id
  )
    throw new Error('aside_specialist_unverified');
  return { sourceSpecialist: source, specialistBinding: child };
}

export function decodeAsideFollowupSpecialist(
  value: unknown,
  intent: XScienceAsideIntent,
  receipt: XScienceAsideReceipt | null,
  branch: { id: string; generation: number }
): SpecialistInputReference | undefined {
  if (!intent.sourceSpecialist) {
    if (value !== undefined) throw new Error('aside_followup_binding_invalid');
    return undefined;
  }
  const reference = decodeSpecialistInputReference(value);
  const initial = receipt?.specialistBinding;
  if (
    !initial ||
    !sameSpecialistScope(reference.scope, initial.scope) ||
    !sameSpecialistBranch(reference.branch, branch) ||
    (!intent.asSession && !sameAsideSpecialist(reference, initial))
  )
    throw new Error('aside_followup_binding_invalid');
  return reference;
}
