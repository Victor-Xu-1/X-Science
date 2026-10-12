import { describe, expect, it } from 'vitest';

import type { Assistant } from '@/common/types/agent/assistantTypes';
import {
  pickDefaultAssistantSelectionKey,
  resolveAssistantSelectionKey,
} from '@/renderer/pages/guid/hooks/useGuidAssistantSelection';

describe('guid assistant selection helpers', () => {
  const assistants: Assistant[] = [
    assistant({ id: 'builtin-writer', source: 'builtin', runtimeKey: 'claude', sort_order: 20 }),
    assistant({ id: 'bare-unsupportedRuntime', source: 'generated', runtimeKey: 'unsupportedRuntime', sort_order: 10 }),
    assistant({ id: 'user-research', source: 'user', runtimeKey: 'gemini', sort_order: 30 }),
    assistant({ id: 'xscience:aidd-expert', source: 'builtin', runtimeKey: 'xscience', sort_order: 40 }),
    assistant({
      id: 'xscience:operon',
      source: 'builtin',
      runtimeKey: 'xscience',
      agent_id: 'OPERON',
      sort_order: 50,
    }),
  ];

  it('prefers explicit X-Science assistant keys when the assistant exists', () => {
    expect(resolveAssistantSelectionKey('custom:xscience:aidd-expert', assistants)).toBe('xscience:aidd-expert');
  });

  it('does not accept non-X-Science assistant ids from stale selections', () => {
    expect(resolveAssistantSelectionKey('custom:user-research', assistants)).toBeUndefined();
    expect(resolveAssistantSelectionKey('bare-unsupportedRuntime', assistants)).toBeUndefined();
  });

  it('does not accept legacy backend keys as assistant selection ids', () => {
    expect(resolveAssistantSelectionKey('claude', assistants)).toBeUndefined();
    expect(resolveAssistantSelectionKey('unsupportedRuntime', assistants)).toBeUndefined();
  });

  it('defaults to the general OPERON expert used by the v1.1 new-task workflow', () => {
    expect(pickDefaultAssistantSelectionKey(assistants)).toBe('xscience:operon');
  });

  it('returns null when no assistants are available', () => {
    expect(pickDefaultAssistantSelectionKey([])).toBeNull();
  });
});

function assistant(
  overrides: Partial<Assistant> & { id: string; source: Assistant['source']; runtimeKey: string }
): Assistant {
  const agentId = overrides.runtimeKey === 'xscience' ? 'AIDD_EXPERT' : `agent-${overrides.runtimeKey}`;
  const isUnsupportedRuntime = overrides.runtimeKey === 'unsupportedRuntime';
  const isXScience = overrides.runtimeKey === 'xscience';
  return {
    id: overrides.id,
    source: overrides.source,
    name: overrides.id,
    name_i18n: {},
    description_i18n: {},
    enabled: true,
    sort_order: overrides.sort_order ?? 0,
    agent_id: agentId,
    agent: isXScience
      ? { type: 'xscience', source: 'builtin', acp_backend: 'xscience' }
      : isUnsupportedRuntime
        ? { type: 'unsupportedRuntime', source: 'internal' }
        : { type: 'acp', source: 'builtin', acp_backend: overrides.runtimeKey },
    enabled_skills: [],
    custom_skill_names: [],
    disabled_builtin_skills: [],
    context_i18n: {},
    prompts: [],
    prompts_i18n: {},
    models: [],
    agent_status: 'online',
    deletable: overrides.source === 'user',
    ...overrides,
  };
}
