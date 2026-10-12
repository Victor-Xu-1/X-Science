import { describe, expect, it, vi } from 'vitest';
import { loadXScienceAgents, toXScienceAssistant } from '@/renderer/services/xScienceCapabilities';

describe('X-Science expert capabilities', () => {
  it('loads the rich profile catalog, hides technical agents, and preserves user ownership', async () => {
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            name: 'AIDD_EXPERT',
            displayName: 'AI药物研发专家',
            description: 'AI驱动的药物发现与设计专家',
            source: 'bundled',
            healthy: true,
            enabled: true,
            userHidden: false,
            skillNames: ['alphafold2'],
          },
          {
            name: 'BOOKMARKER',
            displayName: '会话重点标记专家',
            source: 'bundled',
            healthy: true,
            enabled: true,
            userHidden: true,
          },
          {
            id: 'database-profile-id',
            name: 'USER_REVIEWER',
            displayName: '证据审阅专家',
            description: '审阅证据',
            source: 'user',
            healthy: true,
            enabled: false,
            userHidden: false,
          },
          {
            name: 'OPERON',
            displayName: 'éç¨ç§ç å©æ',
            description: 'General scientific assistant',
            source: 'bundled',
            healthy: true,
            enabled: true,
            userHidden: false,
          },
        ]),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    );

    const agents = await loadXScienceAgents({ baseUrl: 'http://fusion.test', fetchImpl });

    expect(fetchImpl).toHaveBeenCalledWith(
      'http://fusion.test/api/x-science/expert-profiles',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
    expect(agents.map((agent) => agent.name)).toEqual(['AIDD_EXPERT', 'USER_REVIEWER', 'OPERON']);
    expect(toXScienceAssistant(agents[0], 0)).toMatchObject({
      source: 'builtin',
      name: 'AI Drug Discovery Expert',
      name_i18n: {
        'en-US': 'AI Drug Discovery Expert',
        'zh-CN': 'AI 药物研发专家',
      },
      description_i18n: {
        'en-US': expect.stringContaining('generative models'),
        'zh-CN': expect.stringContaining('虚拟筛选'),
      },
      enabled_skills: ['alphafold2'],
      deletable: false,
    });
    expect(toXScienceAssistant(agents[1], 1)).toMatchObject({
      id: 'xscience:user-reviewer',
      source: 'user',
      name: '证据审阅专家',
      enabled: false,
      deletable: true,
      agent: { source: 'custom' },
    });
    expect(toXScienceAssistant(agents[2], 2)).toMatchObject({
      name: 'General Research Assistant',
      name_i18n: {
        'en-US': 'General Research Assistant',
        'zh-CN': '通用科研助手',
      },
    });
  });

  it('retains backend identity when no builtin localization exists', () => {
    const assistant = toXScienceAssistant(
      {
        name: 'PARTNER_EXPERT',
        displayName: 'Partner Expert',
        description: 'Partner-provided biomedical expert.',
        source: 'bundled',
        healthy: true,
        enabled: true,
        userHidden: false,
        skillsLocked: false,
        unrestricted: false,
        supportsPlanMode: true,
        skillNames: [],
      },
      0
    );

    expect(assistant).toMatchObject({
      id: 'xscience:partner-expert',
      agent_id: 'PARTNER_EXPERT',
      name: 'Partner Expert',
      name_i18n: { 'en-US': 'Partner Expert', 'zh-CN': 'Partner Expert' },
      source: 'builtin',
    });
  });

  it('bounds an unavailable expert catalog request instead of leaving the launch page loading forever', async () => {
    vi.useFakeTimers();
    try {
      const fetchImpl = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener(
            'abort',
            () => reject(new DOMException('X-Science request timed out', 'TimeoutError')),
            { once: true }
          );
        });
      }) as unknown as typeof fetch;

      const request = loadXScienceAgents({ fetchImpl });
      const rejection = expect(request).rejects.toMatchObject({ name: 'TimeoutError' });
      await vi.advanceTimersByTimeAsync(15_000);

      await rejection;
      expect(fetchImpl).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
