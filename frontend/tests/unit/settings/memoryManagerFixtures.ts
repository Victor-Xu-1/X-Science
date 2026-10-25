import { vi } from 'vitest';

export const memoryContext = {
  entities: [
    {
      entity_key: 'profile',
      label: '关于你',
      project_id: null,
      rows: [
        {
          id: 'mem_profile',
          body: '关注肿瘤免疫研究',
          categoryId: null,
          origin: 'user',
          evidence: 'stated',
          createdAt: '2026-07-10T00:00:00.000Z',
          updatedAt: '2026-07-10T00:00:00.000Z',
        },
      ],
    },
    {
      entity_key: 'project:proj_a',
      label: 'CRBN 项目',
      project_id: 'proj_a',
      rows: [
        {
          id: 'mem_project',
          body: '项目采用 CRBN 数据集',
          categoryId: 'memcat_1',
          origin: 'assistant',
          evidence: 'inferred',
          createdAt: '2026-07-10T01:00:00.000Z',
          updatedAt: '2026-07-10T01:00:00.000Z',
        },
      ],
    },
  ],
  sessions: [
    {
      frame_id: 'frame_1',
      project_id: 'proj_a',
      label: 'CRBN 结构分析',
      row_count: 1,
      newest_at: '2026-07-10T02:00:00.000Z',
    },
  ],
  categories: [
    {
      id: 'memcat_1',
      name: '研究偏好',
      guidance: '用于研究规划',
      auto_recall: true,
      row_count: 1,
    },
  ],
  memory_enabled: false,
  total_user_rows: 2,
};

export const projectMemoryContext = {
  ...memoryContext,
  entities: [memoryContext.entities[1]],
  total_user_rows: 1,
};

export const projectCatalog = {
  projects: [
    {
      project_id: 'proj_a',
      name: 'CRBN 项目',
      description: 'CRBN 结构分析与设计规则',
      context: null,
      conversation_count: 1,
      artifact_count: 0,
      created_at: '2026-07-10T00:00:00.000Z',
      updated_at: '2026-07-10T00:00:00.000Z',
      last_active_at: '2026-07-10T00:00:00.000Z',
    },
  ],
};

export function createFetchMock(options: { failEnable?: boolean } = {}) {
  const observed: Array<{ url: string; method: string; body?: unknown }> = [];
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
    const method = init?.method ?? 'GET';
    observed.push({
      url,
      method,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
    });
    if (url === '/api/projects' && method === 'GET') return Response.json(projectCatalog);
    if (url === '/api/memory/context' && method === 'GET') return Response.json(memoryContext);
    if (url === '/api/memory/context?project_id=proj_a' && method === 'GET') {
      return Response.json(projectMemoryContext);
    }
    if (url === '/api/memory/enabled' && method === 'GET') return Response.json({ enabled: false });
    if (url === '/api/memory/enabled' && method === 'PUT' && options.failEnable) {
      return Response.json({ error: 'memory unavailable' }, { status: 503 });
    }
    if (url === '/api/memory/sessions/frame_1' && method === 'GET') {
      return Response.json({
        rows: [
          {
            id: 'mem_session',
            body: '会话使用 CRBN 结构数据',
            origin: 'assistant',
            evidence: 'inferred',
            createdAt: '2026-07-10T02:00:00.000Z',
            updatedAt: '2026-07-10T02:00:00.000Z',
          },
        ],
      });
    }
    if (url === '/api/memories' && method === 'POST') {
      return Response.json({
        id: 'mem_created',
        body: '新的研究偏好',
        origin: 'user',
        evidence: 'stated',
        createdAt: '2026-07-10T03:00:00.000Z',
        updatedAt: '2026-07-10T03:00:00.000Z',
      });
    }
    if (url === '/api/memory/categories' && method === 'POST') {
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : {};
      return Response.json(
        {
          id: 'memcat_created',
          name: body.name,
          guidance: body.guidance,
          auto_recall: body.auto_recall,
          row_count: 0,
        },
        { status: 201 }
      );
    }
    return new Response(null, { status: 204 });
  });
  return { fetchMock, observed };
}
