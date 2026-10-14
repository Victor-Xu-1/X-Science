export type XScienceResumeTarget = {
  resume_event_id: string | null;
  wait_ref: string | null;
  state_ref: string;
};

export type XScienceContinueInput = {
  client_command_id: string;
  resume_target: XScienceResumeTarget;
  model?: string;
};

export function normalizeXScienceResumeTarget(value: unknown): XScienceResumeTarget | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const target = value as Record<string, unknown>;
  const validId = (id: unknown) => id === null || (typeof id === 'string' && id.length > 0 && id.length <= 256);
  if (
    !validId(target.resume_event_id) ||
    !validId(target.wait_ref) ||
    typeof target.state_ref !== 'string' ||
    !/^resume-state:[a-f0-9]{64}$/.test(target.state_ref)
  )
    return null;
  return {
    resume_event_id: target.resume_event_id as string | null,
    wait_ref: target.wait_ref as string | null,
    state_ref: target.state_ref,
  };
}

const secureCommandId = () => {
  if (typeof globalThis.crypto?.randomUUID !== 'function') throw new Error('secure_continue_identity_unavailable');
  return globalThis.crypto.randomUUID();
};

// One intent keeps its exact body through POST/GET failure, Abort and unknown
// transport outcomes. Only an explicit stale/conflict result abandons it.
export class XScienceContinueIntent {
  private pending = new Map<string, { input: XScienceContinueInput; busy: boolean; automaticRetries: number }>();

  prepare(
    frame: string,
    target: XScienceResumeTarget | null | undefined,
    model?: string,
    commandId = secureCommandId
  ): XScienceContinueInput {
    const pending = this.pending.get(frame);
    if (pending) {
      if (model !== undefined && model !== pending.input.model) throw new Error('resume_pending_controls_changed');
      return pending.input;
    }
    if (!target) throw new Error('resume_precondition_unavailable');
    const input: XScienceContinueInput = {
      client_command_id: commandId(),
      resume_target: { ...target },
      ...(model ? { model } : {}),
    };
    this.pending.set(frame, { input, busy: false, automaticRetries: 1 });
    return input;
  }

  accepted(frame: string, input: XScienceContinueInput): void {
    if (this.pending.get(frame)?.input.client_command_id === input.client_command_id) this.pending.delete(frame);
  }

  busy(frame: string): void {
    const pending = this.pending.get(frame);
    if (pending) pending.busy = true;
  }

  reject(frame: string): void {
    this.pending.delete(frame);
  }

  retryAfterSnapshot(frame: string, target: XScienceResumeTarget | null | undefined): boolean {
    const pending = this.pending.get(frame);
    if (!pending || !pending.busy || !target) return false;
    const original = pending.input.resume_target;
    if (target.state_ref !== original.state_ref || target.resume_event_id !== original.resume_event_id) {
      this.pending.delete(frame);
      return false;
    }
    if (target.wait_ref === original.wait_ref || pending.automaticRetries === 0) return false;
    pending.automaticRetries--;
    pending.busy = false;
    return true;
  }
}

export function requireContinueReceipt(value: unknown, frameId: string, input: XScienceContinueInput): void {
  if (!value || typeof value !== 'object') throw new Error('resume_receipt_unverified');
  const response = value as Record<string, unknown>;
  const receipt = response.continue_command as Record<string, unknown> | undefined;
  const target = normalizeXScienceResumeTarget(receipt?.target);
  if (
    response.root_frame_id !== frameId ||
    !receipt ||
    typeof receipt.id !== 'string' ||
    !receipt.id ||
    typeof receipt.resume_event_id !== 'string' ||
    !receipt.resume_event_id ||
    typeof receipt.replayed !== 'boolean' ||
    !['resumed', 'woken', 'already_running'].includes(String(receipt.outcome)) ||
    !target ||
    target.resume_event_id !== input.resume_target.resume_event_id ||
    target.wait_ref !== input.resume_target.wait_ref ||
    target.state_ref !== input.resume_target.state_ref
  )
    throw new Error('resume_receipt_unverified');
}
