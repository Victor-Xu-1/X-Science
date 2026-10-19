import { useEffect, useMemo } from 'react';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { sessionDelegationIdentity, type DelegationPolicy } from '@/renderer/services/runtime/sessionDelegationPolicy';
import { SessionDelegationPolicyController } from '@/renderer/services/runtime/sessionDelegationPolicyController';

export class GuidDelegationPreparationError extends Error {
  constructor() {
    super('The conversation delegation policy or draft target is not confirmed');
  }
}
type Materialized = {
  creationScope: string;
  conversation: TChatConversation;
  controller?: SessionDelegationPolicyController;
  release?: () => void;
  message?: Record<string, unknown>;
  messageSignature?: string;
  handedOff?: boolean;
};

/** One unsent draft owner. Creating a frame is not permission to hand off its task. */
class GuidDelegationHandoff {
  private selected: Materialized | null = null;
  constructor(private readonly ownerId: string) {}
  dispose = (): void => {
    this.selected?.release?.();
    this.selected = null;
  };
  async materialize(
    creationScope: string,
    create: () => Promise<TChatConversation>,
    desired: DelegationPolicy | undefined,
    current: () => boolean
  ) {
    const ensureCurrent = () => {
      if (!current()) throw new GuidDelegationPreparationError();
    };
    ensureCurrent();
    if (this.selected && this.selected.creationScope !== creationScope) {
      if (this.selected.message) throw new GuidDelegationPreparationError();
      this.dispose();
    }
    let selected = this.selected;
    if (!selected) {
      const conversation = await create();
      ensureCurrent();
      if (!conversation?.id) throw new GuidDelegationPreparationError();
      selected = { creationScope, conversation };
      this.selected = selected;
    }
    // The backend's no-record policy is allow. Do not create a redundant policy
    // command for the normal new-task path. A retained uncertain deny must first
    // be reconciled even if the user has since explicitly selected allow.
    if (desired !== 'deny' && !selected.controller) return selected.conversation;
    if (!selected.controller) {
      const identity = sessionDelegationIdentity(this.ownerId, selected.conversation.id, selected.conversation.extra);
      if (!identity) throw new GuidDelegationPreparationError();
      selected.controller = new SessionDelegationPolicyController(identity);
      selected.release = selected.controller.attach();
    }
    const controller = selected.controller;
    await controller.refresh();
    ensureCurrent();
    if (controller.getSnapshot().retryable) {
      await controller.retry();
      ensureCurrent();
    }
    const observed = controller.getSnapshot();
    if (!observed.available || observed.error || !observed.snapshot) throw new GuidDelegationPreparationError();
    const enabled = desired !== 'deny';
    if ((observed.snapshot.policy === 'allow') !== enabled) {
      await controller.change(enabled);
      ensureCurrent();
    }
    const confirmed = controller.getSnapshot();
    if (
      !confirmed.available ||
      confirmed.error ||
      !confirmed.snapshot ||
      (confirmed.snapshot.policy === 'allow') !== enabled
    )
      throw new GuidDelegationPreparationError();
    return selected.conversation;
  }
  private freezeMessage(conversationId: string, message: Record<string, unknown>) {
    const selected = this.selected;
    if (!selected || selected.conversation.id !== conversationId) throw new GuidDelegationPreparationError();
    const { loading_id: _identity, ...payload } = message;
    const signature = JSON.stringify(payload);
    if (selected.messageSignature && selected.messageSignature !== signature)
      throw new GuidDelegationPreparationError();
    selected.messageSignature = signature;
    selected.message ??= message;
    return selected.message;
  }
  storeMessage(conversationId: string, message: Record<string, unknown>, write: (value: string) => void): void {
    const frozen = this.freezeMessage(conversationId, message);
    const selected = this.selected!;
    try {
      write(JSON.stringify(frozen));
      selected.handedOff = true;
    } catch (error) {
      if (!selected.handedOff) {
        selected.message = undefined;
        selected.messageSignature = undefined;
      }
      throw error;
    }
  }
}
export function useGuidDelegationHandoff(ownerId: string, draftGeneration = 0) {
  const account = getRendererAccountScopeToken();
  const handoff = useMemo(() => new GuidDelegationHandoff(ownerId), [account, ownerId, draftGeneration]);
  useEffect(() => () => handoff.dispose(), [handoff]);
  return handoff;
}
