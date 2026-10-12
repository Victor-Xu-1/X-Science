import { describe, expect, it } from 'vitest';

import type { XScienceAgent } from '@/renderer/services/xScienceCapabilities';
import { toXScienceAssistant } from '@/renderer/services/xScienceCapabilities';

describe('toXScienceAssistant', () => {
  it('maps a bundled expert to consistent Chinese and English presentation metadata', () => {
    const agent: XScienceAgent = {
      name: 'OPERON',
      displayName: 'Operon',
      description: 'Coordinate biomedical research workflows and specialist tools.',
      healthy: true,
      enabled: true,
      source: 'managed',
      skillsLocked: true,
      unrestricted: false,
      supportsPlanMode: true,
      userHidden: false,
      skillNames: ['alphafold2', 'pubmed-search'],
    };

    expect(toXScienceAssistant(agent, 2)).toMatchObject({
      id: 'xscience:operon',
      name: 'General Research Assistant',
      name_i18n: {
        'en-US': 'General Research Assistant',
        'zh-CN': '通用科研助手',
      },
      description_i18n: {
        'en-US': expect.stringContaining('scientific computing assistant'),
        'zh-CN': expect.stringContaining('通用科研计算助手'),
      },
      enabled: true,
      sort_order: 10002,
      agent_id: 'OPERON',
      agent_status: 'online',
      enabled_skills: ['alphafold2', 'pubmed-search'],
      agent: {
        type: 'xscience',
        source: 'builtin',
        acp_backend: 'xscience',
      },
    });
  });

  it('preserves the backend health and enabled state for an unavailable expert', () => {
    const agent: XScienceAgent = {
      name: 'PROTEOMICS',
      displayName: 'Proteomics',
      description: 'Analyze proteomics data.',
      healthy: false,
      enabled: false,
      source: 'managed',
      skillsLocked: false,
      unrestricted: false,
      supportsPlanMode: false,
      userHidden: false,
      skillNames: [],
    };

    expect(toXScienceAssistant(agent, 0)).toMatchObject({
      id: 'xscience:proteomics',
      enabled: false,
      agent_status: 'offline',
      agent_status_message: 'X-Science expert is not healthy.',
    });
  });
});
