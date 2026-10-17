import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import { describe, expect, it } from 'vitest';
import { buildToolPublicDetailPresentation } from '@/renderer/pages/conversation/Messages/toolDetails/detailProjection';

const locales = [
  { language: 'en-US', high: 'High confidence', medium: 'Medium confidence', low: 'Low confidence' },
  { language: 'zh-CN', high: '高可信度', medium: '中等可信度', low: '低可信度' },
] as const;

const unavailableConfidences = [
  { name: 'ordinary constructor string', feasibility: { confidence: 'constructor' } },
  { name: 'ordinary __proto__ string', feasibility: { confidence: '__proto__' } },
  { name: 'unknown string', feasibility: { confidence: 'uncertain' } },
  { name: 'absent confidence', feasibility: {} },
  { name: 'null confidence', feasibility: { confidence: null } },
  { name: 'numeric confidence', feasibility: { confidence: 1 } },
] as const;

const registeredConfidences = [
  { value: 'high', label: 'high' },
  { value: 'medium', label: 'medium' },
  { value: 'low', label: 'low' },
  { value: '  HiGh\n', label: 'high' },
] as const;

const publicPlan = { steps: [{ title: 'Compare public observations' }] };
const tool: NormalizedToolCall = {
  key: 'plan-confidence-record',
  name: 'generate_plan',
  status: 'completed',
  output: JSON.stringify({ summary: 'Public plan receipt' }),
};

describe.each(locales)('public plan confidence in $language', (locale) => {
  describe.each(['nested', 'top-level'] as const)('%s feasibility', (placement) => {
    // Ordinary JSON string values must not select inherited locale keys.
    // A top-level feasibility beside an existing plan exercises the real fallback.
    it.each(unavailableConfidences)('omits assessment for $name without throwing', (test) => {
      const input = {
        task_summary: 'Compare public observations',
        plan: placement === 'nested' ? { ...publicPlan, feasibility: test.feasibility } : publicPlan,
        ...(placement === 'top-level' ? { feasibility: test.feasibility } : {}),
      };
      const item: NormalizedToolCall = { ...tool, input: JSON.stringify(input), status: 'error' };
      const original = { ...item };
      const detail = buildToolPublicDetailPresentation(item, locale.language, null);

      expect(detail.detailKind).toBe('plan');
      expect(detail.planAssessment).toBeNull();
      expect(detail.planSummary).toBe(input.task_summary);
      expect(item).toEqual(original);
    });

    it.each(registeredConfidences)('keeps registered confidence "$value" localized', (test) => {
      const feasibility = { confidence: test.value, rationale: '  Public observations remain provisional.  ' };
      const input = {
        task_summary: 'Compare public observations',
        plan: placement === 'nested' ? { ...publicPlan, feasibility } : publicPlan,
        ...(placement === 'top-level' ? { feasibility } : {}),
      };
      const item: NormalizedToolCall = { ...tool, input: JSON.stringify(input) };
      const original = { ...item };
      const detail = buildToolPublicDetailPresentation(item, locale.language, null);

      expect(detail.planAssessment).toEqual({
        confidence: locale[test.label],
        rationale: 'Public observations remain provisional.',
      });
      expect(detail.planDocument?.phases[0].steps[0].title).toBe('Compare public observations');
      expect(item).toEqual(original);
    });

    it('keeps public rationale while redacting existing private fragments and host paths', () => {
      const feasibility = {
        confidence: 'high',
        rationale: 'Public observations remain provisional; runtime policy detail; /home/fixture/private/notes.txt',
      };
      const input = {
        task_summary: 'Compare public observations',
        plan: placement === 'nested' ? { ...publicPlan, feasibility } : publicPlan,
        ...(placement === 'top-level' ? { feasibility } : {}),
      };
      const item: NormalizedToolCall = { ...tool, input: JSON.stringify(input), status: 'blocked' };
      const original = { ...item };
      const detail = buildToolPublicDetailPresentation(item, locale.language, null);

      expect(detail.planAssessment).toEqual({
        confidence: locale.high,
        rationale: 'Public observations remain provisional; [internal detail redacted] detail; notes.txt',
      });
      const rendered = JSON.stringify(detail);
      expect(rendered).not.toContain('runtime policy');
      expect(rendered).not.toContain('/home/fixture/private');
      expect(item).toEqual(original);
    });
  });
});
