import {
  cancelXScienceAsideInput,
  cancelXScienceAsideMessageIntent,
  closeXScienceAside,
  createXScienceAside,
  lookupXScienceAside,
  loadXScienceAsideBranches,
  sendXScienceAsideFollowup,
  type XScienceAsideClose,
  type XScienceAsideIntent,
  type XScienceAsideLookup,
  type XScienceAsideReceipt,
  type XScienceAsideMessageCancellation,
} from './xScienceAside';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from './rendererAccountScope';
import {
  clearAsidePresentation,
  saveAsidePresentation,
  type AsidePresentation,
  type AsideConsumer,
} from './xScienceAsidePresentation';
import { loadXScienceSessionOptions, toXScienceMessageSessionOptions } from './xScienceSessionOptions';
import { isXScienceHttpError, type XScienceGatewayOptions } from './xScienceHttp';
import { resumeXScienceFrame } from './xScienceRuntimeOperations';
import type { XScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

/** One UI action owner, not a task scheduler. Server ownership/receipts remain authoritative. */
export class XScienceAsideOperation {
  readonly intent: XScienceAsideIntent;
  private readonly account = getRendererAccountScopeToken();
  private readonly owner = getRendererAccountOwnerId();
  private active = true;
  private verifiedReceipt = false;
  private storageError = false;
  private value: AsidePresentation;
  private closedProof = false;
  private closeProof: XScienceAsideClose | XScienceAsideMessageCancellation | null = null;
  private cancelledFollowup: XScienceAsideMessageCancellation | null = null;
  private dispatched = false;
  private starting: Promise<XScienceAsideReceipt> | null = null;
  private closing: Promise<XScienceAsideClose | XScienceAsideMessageCancellation> | null = null;
  private sending: Promise<boolean> | null = null;
  private sendingText = '';
  private resuming: Promise<void> | null = null;

  constructor(
    intent: XScienceAsideIntent,
    recovered?: AsidePresentation,
    private options: XScienceGatewayOptions = {},
    consumer: AsideConsumer | null = null
  ) {
    this.intent = Object.freeze({ ...intent });
    if (
      recovered &&
      (recovered.intent.intentId !== intent.intentId ||
        recovered.intent.parentFrameId !== intent.parentFrameId ||
        recovered.intent.request !== intent.request ||
        recovered.intent.model !== intent.model ||
        recovered.intent.asSession !== intent.asSession ||
        recovered.intent.sourceBranchId !== intent.sourceBranchId ||
        recovered.intent.expectedGeneration !== intent.expectedGeneration)
    )
      throw new Error('aside_presentation_replaced');
    this.value = recovered ?? {
      intent: this.intent,
      receipt: null,
      inputMessageId: null,
      pendingFollowup: null,
      pendingResume: null,
      closeRequested: false,
      draft: '',
      consumer,
    };
    // Restored values are only presentation hints; never start/cancel from construction.
    this.dispatched = Boolean(recovered);
    if (!recovered) this.persist();
  }

  get receipt() {
    return this.verifiedReceipt ? this.value.receipt : null;
  }
  get persistenceError() {
    return this.storageError;
  }
  get closed() {
    return this.closedProof;
  }
  get closeRequested() {
    return this.value.closeRequested;
  }
  get inputMessageId() {
    return this.value.inputMessageId;
  }
  get cancelTarget() {
    return this.value.inputMessageId ?? undefined;
  }
  get presentation() {
    return this.value;
  }
  acknowledgeConsumer(): void {
    this.assertOwner();
    this.value = { ...this.value, consumer: null };
    if (this.closedProof) this.clearHint();
    else this.persist(false);
  }
  retire(): void {
    this.active = false;
  }
  setDraft(draft: string): void {
    this.assertOwner();
    this.value = { ...this.value, draft };
    this.persist();
  }

  async reconcile(): Promise<XScienceAsideLookup> {
    this.assertOwner();
    const result = await lookupXScienceAside(this.intent, this.options);
    this.assertOwner();
    this.applyLookup(result);
    return result;
  }

  start(): Promise<XScienceAsideReceipt> {
    try {
      this.assertOwner();
    } catch (error) {
      return Promise.reject(error);
    }
    if (this.closedProof || this.closeRequested) return Promise.reject(new Error('aside_close_pending'));
    if (this.receipt) return Promise.resolve(this.receipt);
    if (this.starting) return this.starting;
    const run = async () => {
      if (this.dispatched) {
        const known = await this.reconcile();
        if (known.status === 'accepted') return known.receipt;
        if (known.status !== 'absent') throw new Error('aside_intent_cancelled');
      }
      this.assertOwner();
      if (this.closeRequested || this.closedProof) throw new Error('aside_close_pending');
      this.dispatched = true;
      this.persist();
      try {
        const receipt = await createXScienceAside(this.intent, this.options);
        this.assertOwner();
        this.accept(receipt);
        return receipt;
      } catch (error) {
        this.assertOwner();
        const known = await this.reconcile().catch(() => null);
        if (known?.status === 'accepted') return known.receipt;
        throw error;
      }
    };
    const promise = run().finally(() => {
      if (this.starting === promise) this.starting = null;
    });
    this.starting = promise;
    return promise;
  }

  close(): Promise<XScienceAsideClose | XScienceAsideMessageCancellation> {
    try {
      this.assertOwner();
    } catch (error) {
      return Promise.reject(error);
    }
    if (this.closedProof && this.closeProof) return Promise.resolve(this.closeProof);
    if (this.closing) return this.closing;
    this.value = { ...this.value, closeRequested: true };
    this.persist();
    const target = this.cancelTarget;
    const pending = this.value.pendingFollowup;
    const run = async () => {
      if (pending) {
        if (!this.receipt) await this.reconcile();
        if (!this.receipt) throw new Error('aside_followup_receipt_unavailable');
        const proof = await cancelXScienceAsideMessageIntent(this.receipt.frameId, pending, this.options);
        this.assertOwner();
        this.cancelledFollowup = proof;
        this.closeProof = proof;
        this.closedProof = true;
        // Keep the frozen mutation in any retained consumer hint. After a
        // reload only replaying its DELETE can re-establish this close proof.
        this.value = {
          ...this.value,
          pendingFollowup: pending,
          ...(proof.inputMessageId ? { inputMessageId: proof.inputMessageId } : {}),
        };
        this.clearHint();
        return proof;
      }
      const result = await closeXScienceAside(this.intent, target, this.options);
      this.assertOwner();
      if (result.status !== 'cancelled-before-acceptance') this.accept(result.receipt);
      this.closedProof = true;
      this.closeProof = result;
      this.clearHint();
      return result;
    };
    const promise = run().finally(() => {
      if (this.closing === promise) this.closing = null;
    });
    this.closing = promise;
    return promise;
  }

  async cancel(): Promise<void> {
    this.assertOwner();
    if (this.closedProof) throw new Error('aside_intent_closed');
    if (this.value.pendingFollowup && this.receipt) {
      const pending = this.value.pendingFollowup;
      const proof = await cancelXScienceAsideMessageIntent(this.receipt.frameId, pending, this.options);
      this.assertOwner();
      this.cancelledFollowup = proof;
      this.value = {
        ...this.value,
        pendingFollowup: this.closeRequested ? pending : null,
        ...(proof.inputMessageId ? { inputMessageId: proof.inputMessageId } : {}),
      };
      this.persist(false);
      return;
    }
    if (!this.receipt) {
      await this.close();
      return;
    }
    const target = this.cancelTarget;
    if (!target) throw new Error('aside_cancel_target_unavailable');
    await cancelXScienceAsideInput(this.receipt.frameId, target, this.options);
    this.assertOwner();
  }

  send(text: string): Promise<boolean> {
    try {
      this.assertOwner();
    } catch (error) {
      return Promise.reject(error);
    }
    if (!text.trim() || !this.receipt || this.closeRequested || this.closedProof || this.resuming)
      return Promise.resolve(false);
    if (this.sending) return text.trim() === this.sendingText ? this.sending : Promise.resolve(false);
    this.sendingText = text.trim();
    const run = async () => {
      const receipt = this.receipt!;
      const normalized = text.trim();
      if (this.value.pendingFollowup && this.value.pendingFollowup.text !== normalized)
        throw new Error('aside_followup_controls_changed');
      if (!this.value.pendingFollowup) {
        const [sessionOptions, branches] = await Promise.all([
          loadXScienceSessionOptions(receipt.frameId),
          loadXScienceAsideBranches(receipt.frameId, this.options),
        ]);
        this.assertOwner();
        if (this.closeRequested || !branches.activeBranchId) return false;
        this.value = {
          ...this.value,
          pendingFollowup: Object.freeze({
            loadingId: crypto.randomUUID(),
            text: normalized,
            sessionOptions: Object.freeze({
              ...toXScienceMessageSessionOptions(sessionOptions, {
                targetBranchId: branches.activeBranchId,
                expectedBranchId: branches.activeBranchId,
                expectedGeneration: branches.generation,
              }),
              ...(this.intent.model ? { model: this.intent.model } : {}),
            }),
          }),
        };
        this.persist();
      }
      const command = this.value.pendingFollowup!;
      let accepted: Awaited<ReturnType<typeof sendXScienceAsideFollowup>>;
      try {
        accepted = await sendXScienceAsideFollowup(receipt.frameId, command, this.options);
      } catch (error) {
        this.assertOwner();
        if (this.cancelledFollowup?.loadingId === command.loadingId) return false;
        throw error;
      }
      this.assertOwner();
      if (this.cancelledFollowup?.loadingId === command.loadingId) return false;
      if (this.closedProof) return true;
      this.value = {
        ...this.value,
        inputMessageId: accepted.msg_id,
        pendingFollowup: this.closeRequested ? command : null,
      };
      this.persist(false);
      // An in-memory Close may race with admission. Finish that exact requested
      // cancellation after the accepted input is known; never guess the latest.
      if (this.closeRequested) await this.close();
      return true;
    };
    const promise = run().finally(() => {
      if (this.sending === promise) this.sending = null;
    });
    this.sending = promise;
    return promise;
  }

  resume(snapshot: XScienceRuntimeSnapshot): Promise<void> {
    try {
      this.assertOwner();
    } catch (error) {
      return Promise.reject(error);
    }
    if (
      !this.receipt ||
      snapshot.frameId !== this.receipt.frameId ||
      this.closeRequested ||
      this.closedProof ||
      this.value.pendingFollowup ||
      this.sending
    ) {
      return Promise.reject(new Error('aside_resume_unavailable'));
    }
    if (this.resuming) return this.resuming;
    if (!this.value.pendingResume) {
      if (!snapshot.canResume || !snapshot.resumeTarget) return Promise.reject(new Error('aside_resume_unavailable'));
      this.value = {
        ...this.value,
        pendingResume: {
          client_command_id: crypto.randomUUID(),
          resume_target: { ...snapshot.resumeTarget },
          ...(this.intent.model ? { model: this.intent.model } : {}),
        },
      };
      this.persist();
    }
    const input = this.value.pendingResume;
    const run = async () => {
      try {
        await resumeXScienceFrame(this.receipt!.frameId, input!, this.options);
      } catch (error) {
        this.assertOwner();
        if (isXScienceHttpError(error) && error.code === 'RESUME_TARGET_STALE') {
          this.value = { ...this.value, pendingResume: null };
          this.persist(false);
        }
        throw error;
      }
      this.assertOwner();
      this.value = { ...this.value, pendingResume: null };
      this.persist(false);
    };
    const promise = run().finally(() => {
      if (this.resuming === promise) this.resuming = null;
    });
    this.resuming = promise;
    return promise;
  }

  acknowledgeBranch(): void {
    this.assertOwner();
    if (this.intent.asSession && this.receipt) this.clearHint();
  }

  private accept(receipt: XScienceAsideReceipt): void {
    if (this.closedProof && !this.receipt) throw new Error('aside_receipt_after_cancelled_admission');
    const previous = this.receipt;
    if (
      previous &&
      (previous.frameId !== receipt.frameId ||
        previous.intentId !== receipt.intentId ||
        previous.inputMessageId !== receipt.inputMessageId ||
        previous.sourceBranchId !== receipt.sourceBranchId ||
        previous.sourceGeneration !== receipt.sourceGeneration ||
        previous.asSession !== receipt.asSession ||
        previous.throughPublicationSequence !== receipt.throughPublicationSequence)
    )
      throw new Error('aside_receipt_changed');
    this.verifiedReceipt = true;
    this.value = { ...this.value, receipt, inputMessageId: this.value.inputMessageId ?? receipt.inputMessageId };
    if (!this.closedProof) this.persist(false);
  }

  private applyLookup(result: XScienceAsideLookup): void {
    if (result.status === 'cancelled-before-acceptance') {
      this.closedProof = true;
      this.closeProof = result;
      this.clearHint();
    } else if (result.status === 'accepted') {
      this.accept(result.receipt);
      if (this.closeRequested && !this.value.pendingFollowup && result.cancelledInputMessageId === this.cancelTarget) {
        this.closedProof = true;
        this.closeProof = {
          status: 'cancelled',
          receipt: result.receipt,
          inputMessageId: result.cancelledInputMessageId!,
        };
        this.clearHint();
      }
    }
  }

  private assertOwner(): void {
    if (
      !this.active ||
      !this.owner ||
      getRendererAccountScopeToken() !== this.account ||
      getRendererAccountOwnerId() !== this.owner
    )
      throw new Error('aside_action_owner_changed');
  }
  private persist(required = true): void {
    this.assertOwner();
    try {
      saveAsidePresentation(this.value);
      this.storageError = false;
    } catch (error) {
      this.storageError = true;
      if (required) throw error;
    }
  }
  private clearHint(): void {
    this.assertOwner();
    // An accepted transferred queue item remains reserved until its exact UI
    // consumer acknowledges removal. Cancellation is not a Main-send receipt.
    if (this.receipt && this.value.consumer) {
      this.persist(false);
      return;
    }
    try {
      clearAsidePresentation(this.intent);
      this.storageError = false;
    } catch {
      this.storageError = true;
    }
  }
}
