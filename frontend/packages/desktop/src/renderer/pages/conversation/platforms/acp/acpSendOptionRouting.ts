import { decodeSpecialistSendAuthority, type SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
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
  sideDraft: (draft: string, authority?: SpecialistSendAuthority) => unknown;
  branchDraft: (draft: string, authority?: SpecialistSendAuthority) => unknown;
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
  // An edited legacy {} is still its original captured intent, not the current selector.
  let authority: SpecialistSendAuthority;
  try {
    authority = decodeSpecialistSendAuthority(context.queuedSpecialist ?? context.captureSpecialist());
  } catch {
    context.warnNormalSend();
    return;
  }
  const send = intent === 'side_chat' ? context.sideDraft : context.branchDraft;
  if (authority.reference) void send(context.draft, authority);
  else void send(context.draft);
}
