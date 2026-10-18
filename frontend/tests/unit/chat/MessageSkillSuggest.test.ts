import { describe, expect, it, vi } from 'vitest';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import MessageSkillSuggest, {
  normalizeSkillSuggestionPayload,
} from '@/renderer/pages/conversation/Messages/components/MessageSkillSuggest';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: React.ReactNode }) => React.createElement('article', null, children),
}));

describe('normalizeSkillSuggestionPayload', () => {
  it('accepts the current payload and the legacy snake-case content field', () => {
    expect(
      normalizeSkillSuggestionPayload({
        cron_job_id: 'job-1',
        name: 'Literature Review',
        description: 'Review a topic',
        skill_content: '# Instructions',
      })
    ).toEqual({
      name: 'Literature Review',
      description: 'Review a topic',
      content: '# Instructions',
    });
  });

  it('rejects malformed, null, and incomplete artifacts instead of rendering an empty save action', () => {
    expect(normalizeSkillSuggestionPayload(null)).toBeNull();
    expect(normalizeSkillSuggestionPayload('{invalid json')).toBeNull();
    expect(normalizeSkillSuggestionPayload({ name: 'Missing content' })).toBeNull();
    expect(normalizeSkillSuggestionPayload({ cron_job_id: 'job-1', name: 'Empty', skillContent: '   ' })).toBeNull();
  });

  it('preserves historical suggestion content as read-only text without a second Save action', () => {
    const html = renderToStaticMarkup(
      React.createElement(MessageSkillSuggest, {
        artifact: {
          id: 'historical-suggestion',
          conversation_id: 'conversation',
          kind: 'skill_suggest',
          status: 'pending',
          created_at: 1,
          updated_at: 1,
          payload: {
            cron_job_id: 'old-job',
            name: 'Reusable workflow',
            description: 'Original suggestion',
            skill_content: '# Preserved instructions',
          },
        },
      })
    );
    expect(html).toContain('Reusable workflow');
    expect(html).toContain('Original suggestion');
    expect(html).toContain('# Preserved instructions');
    expect(html).not.toContain('<button');
    expect(html).not.toContain('skill-suggest-card');
  });
});
