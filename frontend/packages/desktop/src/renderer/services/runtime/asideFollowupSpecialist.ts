import { decodeSpecialistInputReference } from '@/common/adapter/specialistBinding';
import { loadSessionSpecialist } from './sessionSpecialist';
import { sameSpecialistBranch, SpecialistPreparationRequired } from './sessionSpecialistTypes';
import { decodeAsideFollowupSpecialist } from '../asideSpecialist';
import type { XScienceAsideIntent, XScienceAsideReceipt } from '../xScienceAside';

/** Hidden asides cannot reconfigure: their verified receipt is immutable input authority. */
export async function freezeAsideFollowupSpecialist(
  intent: XScienceAsideIntent,
  receipt: XScienceAsideReceipt,
  ownerId: string,
  branch: { id: string; generation: number }
) {
  if (!intent.sourceSpecialist) return {};
  const initial = receipt.specialistBinding;
  if (!initial) throw new SpecialistPreparationRequired();
  if (!intent.asSession) {
    const specialistBinding = decodeAsideFollowupSpecialist(initial, intent, receipt, branch)!;
    return { specialistBinding, targetAgent: null };
  }
  const snapshot = await loadSessionSpecialist(
    {
      ownerId,
      frameId: receipt.frameId,
      frameIncarnationId: initial.scope.frame_incarnation_id,
      rootFrameId: initial.scope.root_frame_id,
    },
    initial.scope
  );
  if (
    snapshot.phase === 'legacy' ||
    snapshot.preparation_error ||
    !sameSpecialistBranch(snapshot.branch, snapshot.prepared_branch) ||
    !sameSpecialistBranch(snapshot.branch, branch)
  )
    throw new SpecialistPreparationRequired();
  const specialistBinding = decodeSpecialistInputReference({
    scope: snapshot.scope,
    branch: snapshot.prepared_branch,
    revision: snapshot.revision,
    snapshot_id: snapshot.desired.snapshot_id,
  });
  decodeAsideFollowupSpecialist(specialistBinding, intent, receipt, branch);
  return { specialistBinding, targetAgent: snapshot.desired.selection_name };
}
