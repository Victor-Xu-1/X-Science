import { useEffect, useMemo } from 'react';
import type { TChatConversation } from '@/common/config/storage';
import { decodeSpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { sessionDelegationIdentity, type DelegationPolicy } from '@/renderer/services/runtime/sessionDelegationPolicy';
import { SessionDelegationPolicyController } from '@/renderer/services/runtime/sessionDelegationPolicyController';
import {
  EMPTY_SPECIALIST_VIEW,
  SessionSpecialistController,
} from '@/renderer/services/runtime/sessionSpecialistController';
import {
  SpecialistPreparationRequired,
  sameSpecialistBranch,
  type SpecialistSendAuthority,
} from '@/renderer/services/runtime/sessionSpecialistTypes';

export type GuidSpecialistIntent = Readonly<{ id: string; name: string | null }>;

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
  specialist?: SessionSpecialistController;
  releaseSpecialist?: () => void;
  specialistIntent?: GuidSpecialistIntent;
  current?: () => boolean;
};

/** One unsent draft owner. Creating a frame is not permission to hand off its task. */
class GuidDelegationHandoff {
  private selected: Materialized | null = null;
  private listeners = new Set<() => void>();
  constructor(private readonly ownerId: string) {}
  dispose = (): void => {
    this.selected?.release?.();
    this.selected?.releaseSpecialist?.();
    this.selected = null;
    this.listeners.forEach((listener) => listener());
  };
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  getSpecialistSnapshot = () =>
    this.selected?.current?.()
      ? (this.selected.specialist?.getSnapshot() ?? EMPTY_SPECIALIST_VIEW)
      : EMPTY_SPECIALIST_VIEW;
  retrySpecialist = async () => {
    if (this.selected?.current?.()) await this.selected.specialist?.retry();
  };
  refreshSpecialist = async () => {
    if (this.selected?.current?.()) await this.selected.specialist?.refresh();
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
    selected.current = current;
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
  async prepareSpecialist(
    intent: GuidSpecialistIntent | undefined,
    current: () => boolean
  ): Promise<SpecialistSendAuthority> {
    const selected = this.selected;
    const ensureCurrent = () => {
      if (!selected || this.selected !== selected || !current()) throw new SpecialistPreparationRequired();
    };
    ensureCurrent();
    if (selected!.handedOff && selected!.message) {
      if (selected!.specialistIntent?.id !== intent?.id) throw new SpecialistPreparationRequired();
      const options = selected!.message.session_options as Record<string, unknown> | undefined;
      return options?.specialist_binding
        ? decodeSpecialistSendAuthority({
            reference: options.specialist_binding,
            targetAgent: options.target_agent,
          })
        : {};
    }
    if (!intent) return {};
    if (!selected!.specialist) {
      const identity = sessionDelegationIdentity(this.ownerId, selected!.conversation.id, selected!.conversation.extra);
      if (!identity) throw new SpecialistPreparationRequired();
      const controller = new SessionSpecialistController(identity);
      selected!.specialist = controller;
      const unsubscribe = controller.subscribe(() => this.listeners.forEach((listener) => listener()));
      const detach = controller.attach();
      selected!.releaseSpecialist = () => {
        unsubscribe();
        detach();
      };
    }
    const controller = selected!.specialist!,
      sameIntent = selected!.specialistIntent?.id === intent.id;
    selected!.specialistIntent = intent;
    await controller.refresh();
    ensureCurrent();
    const uncertain = controller.getSnapshot().retryable;
    if (uncertain) {
      await controller.retry();
      ensureCurrent();
    }
    const observed = controller.getSnapshot(),
      snapshot = observed.snapshot;
    const matches = () => {
      const value = controller.getSnapshot().snapshot;
      return Boolean(
        value?.revision &&
        (intent.name === null ? value.desired.is_default : value.desired.selection_name === intent.name)
      );
    };
    if (
      !observed.available ||
      !snapshot ||
      (observed.error && (sameIntent || !['conflict', 'unavailable', 'invalid'].includes(observed.error))) ||
      (sameIntent &&
        snapshot.revision > 0 &&
        (!sameSpecialistBranch(snapshot.branch, snapshot.prepared_branch) || (uncertain && !matches())))
    )
      throw new SpecialistPreparationRequired();
    if (
      !matches() ||
      !sameSpecialistBranch(snapshot.branch, snapshot.prepared_branch) ||
      observed.error ||
      snapshot.preparation_error
    ) {
      await controller.change(intent.name);
      ensureCurrent();
    }
    const confirmed = controller.getSnapshot();
    if (!confirmed.available || confirmed.error || !matches()) throw new SpecialistPreparationRequired();
    return controller.capture();
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
