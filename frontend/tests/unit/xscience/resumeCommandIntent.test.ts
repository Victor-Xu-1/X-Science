import { describe, expect, it } from 'vitest';
import {
  normalizeXScienceResumeTarget,
  requireContinueReceipt,
  XScienceContinueIntent,
  type XScienceResumeTarget,
} from '@/renderer/components/xScience/runtime/resumeCommandIntent';

const target: XScienceResumeTarget = {
  resume_event_id: 'resume',
  wait_ref: 'wait-1',
  state_ref: `resume-state:${'a'.repeat(64)}`,
};
const key = '006239c2-cc88-45fc-8e9f-d6e16a0c755d';

describe('Continue request intent', () => {
  it('keeps original body through uncertain POST, follow-up GET failure, and Abort', () => {
    const intent = new XScienceContinueIntent();
    const input = intent.prepare('frame', target, 'model-a', () => key);
    const later = { ...target, wait_ref: 'wait-2', state_ref: `resume-state:${'b'.repeat(64)}` };
    expect(intent.prepare('frame', later)).toBe(input);
    intent.prepare('another-frame', target, undefined, () => 'another-command');
    expect(intent.prepare('frame', later)).toBe(input);
    expect(input.resume_target).toEqual(target);
    expect(() => intent.prepare('frame', later, 'model-b')).toThrow('resume_pending_controls_changed');
    intent.accepted('frame', input);
    expect(intent.prepare('frame', later, undefined, () => 'new-command').resume_target).toEqual(later);
  });

  it('retries only one explicit busy after an existing refresh proves same-wait progress', () => {
    const intent = new XScienceContinueIntent();
    const input = intent.prepare('frame', target, undefined, () => key);
    intent.busy('frame');
    expect(intent.retryAfterSnapshot('frame', target)).toBe(false);
    expect(intent.retryAfterSnapshot('other-frame', { ...target, wait_ref: 'backstop-2' })).toBe(false);
    expect(intent.retryAfterSnapshot('frame', { ...target, wait_ref: 'backstop-2' })).toBe(true);
    expect(intent.prepare('frame', { ...target, wait_ref: 'backstop-2' })).toBe(input);
    intent.busy('frame');
    expect(intent.retryAfterSnapshot('frame', { ...target, wait_ref: 'backstop-3' })).toBe(false);
  });

  it('does not automatically retarget a new waiting condition', () => {
    const intent = new XScienceContinueIntent();
    intent.prepare('frame', target, undefined, () => key);
    intent.busy('frame');
    const next = { ...target, wait_ref: 'new-wait', state_ref: `resume-state:${'b'.repeat(64)}` };
    expect(intent.retryAfterSnapshot('frame', next)).toBe(false);
    expect(intent.prepare('frame', next, undefined, () => 'new-explicit-intent').client_command_id).toBe(
      'new-explicit-intent'
    );
  });

  it('requires the compact accepted command receipt, not a bare 2xx or unrelated target', () => {
    const input = new XScienceContinueIntent().prepare('frame', target, undefined, () => key);
    expect(() => requireContinueReceipt({ root_frame_id: 'frame' }, 'frame', input)).toThrow(
      'resume_receipt_unverified'
    );
    const receipt = {
      id: 'receipt',
      target: { state_ref: target.state_ref, wait_ref: target.wait_ref, resume_event_id: target.resume_event_id },
      resume_event_id: 'resume',
      outcome: 'woken',
      replayed: true,
    };
    expect(() =>
      requireContinueReceipt({ root_frame_id: 'frame', continue_command: receipt }, 'frame', input)
    ).not.toThrow();
    expect(() => requireContinueReceipt({ root_frame_id: 'other', continue_command: receipt }, 'frame', input)).toThrow(
      'resume_receipt_unverified'
    );
    expect(normalizeXScienceResumeTarget({ state_ref: target.state_ref })).toBeNull();
  });
});
