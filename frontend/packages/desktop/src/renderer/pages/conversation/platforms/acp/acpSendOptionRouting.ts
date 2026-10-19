import type { SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import type { XScienceSendIntent } from '@/renderer/components/xScience/runtime/XScienceSendOptionsMenu';

type SendOptionContext = {
  blocked: boolean;
  draft: string;
  structuredContext: boolean;
  invalidSpecialist: boolean;
  queuedSpecialist?: SpecialistSendAuthority;
  captureSpecialist: () => SpecialistSendAuthority;
  warnNormalSend: () => void;
  planFirst: (draft: string) => void;
  sideDraft: (draft: string) => unknown;
  branchDraft: (draft: string) => unknown;
};

/** Dispatch only: execution and immutable authority remain with their existing owners. */
export function routeAcpSendOption(intent: XScienceSendIntent, context: SendOptionContext): void {
  if (context.blocked || !context.draft.trim()) return;
  if (intent === 'plan_first') {
    context.planFirst(context.draft);
    return;
  }
  if (context.structuredContext || context.invalidSpecialist) {
    context.warnNormalSend();
    return;
  }
  // Atomic aside admission cannot yet transfer a scoped immutable definition.
  // An edited legacy {} is still its original captured intent, not the current selector.
  try {
    const authority = context.queuedSpecialist ?? context.captureSpecialist();
    if (authority.reference) {
      context.warnNormalSend();
      return;
    }
  } catch {
    context.warnNormalSend();
    return;
  }
  if (intent === 'side_chat') void context.sideDraft(context.draft);
  else void context.branchDraft(context.draft);
}
