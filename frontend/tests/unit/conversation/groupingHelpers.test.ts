/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { describe, expect, it } from 'vitest';
import type { TChatConversation } from '@/common/config/storage';
import { buildGroupedHistory } from '@/renderer/pages/conversation/GroupedHistory/utils/groupingHelpers';

const t = (key: string): string => key;

const conversation = (id: string, extra: TChatConversation['extra'], modified_at: number): TChatConversation =>
  ({
    id,
    name: id,
    type: 'acp',
    created_at: modified_at,
    modified_at,
    extra,
  }) as TChatConversation;

describe('buildGroupedHistory', () => {
  it('keeps scheduled-task conversations in the regular conversation timeline', () => {
    const result = buildGroupedHistory(
      [conversation('cron-conversation', { backend: 'unexpectedBackend', cron_job_id: 'job-1' }, 100)],
      t
    );

    expect(result.timelineSections[0]?.items).toEqual([
      expect.objectContaining({
        type: 'conversation',
        conversation: expect.objectContaining({ id: 'cron-conversation' }),
      }),
    ]);
  });

  it('keeps scheduled-task conversations with workspaces in the project section', () => {
    const result = buildGroupedHistory(
      [
        conversation(
          'cron-project-conversation',
          {
            backend: 'unexpectedBackend',
            cron_job_id: 'job-1',
            workspace: '/repo/x-science',
            custom_workspace: true,
          },
          100
        ),
      ],
      t
    );

    expect(result.timelineSections[0]?.items).toEqual([
      expect.objectContaining({
        type: 'workspace',
        workspaceGroup: expect.objectContaining({
          workspace: '/repo/x-science',
          conversations: [expect.objectContaining({ id: 'cron-project-conversation' })],
        }),
      }),
    ]);
  });

  it('keeps legacy team-marked conversations visible as normal conversations', () => {
    const result = buildGroupedHistory(
      [conversation('legacy-conversation', { backend: 'xscience', team_id: 'retired-team-1' }, 100)],
      t
    );

    expect(result.timelineSections[0]?.items).toEqual([
      expect.objectContaining({
        type: 'conversation',
        conversation: expect.objectContaining({ id: 'legacy-conversation' }),
      }),
    ]);
  });

  it('promotes X-Science backend projects into the XScience projects section', () => {
    const result = buildGroupedHistory(
      [
        {
          ...conversation(
            'proj_example',
            {
              backend: 'xscience',
              project_id: 'proj_example',
              conversation_count: 4,
              artifact_count: 83,
              workspace: 'x-science://proj_example',
              custom_workspace: false,
            },
            100
          ),
          type: 'xscience-project',
          name: 'Example project',
          source: 'xscience',
        } as TChatConversation,
      ],
      t
    );

    expect(result.timelineSections[0]?.items).toEqual([
      expect.objectContaining({
        type: 'workspace',
        workspaceGroup: expect.objectContaining({
          workspace: 'x-science://proj_example',
          display_name: 'Example project',
          conversations: [expect.objectContaining({ id: 'proj_example', type: 'xscience-project' })],
        }),
      }),
    ]);
  });
});
