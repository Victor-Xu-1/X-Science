import { BackendHttpError } from '@/common/adapter/httpBridge';
import { withTransientHistoryRetry } from '@/renderer/services/xScienceHistoryRetry';

export type XScienceLineageMessage = {
  id: string;
  kind: 'text' | 'thinking' | 'tool' | 'status';
  position: 'left' | 'right' | 'center';
  content: string;
  createdAt: number | null;
};

export type XScienceLineageMessagesResult = {
  items: XScienceLineageMessage[];
  hasMoreBefore: boolean;
};

export type XScienceLineageMessagesOptions = {
  fetchImpl?: typeof fetch;
  signal?: AbortSignal;
  limit?: number;
};

export async function loadXScienceLineageMessages(
  frameId: string,
  options: XScienceLineageMessagesOptions = {}
): Promise<XScienceLineageMessagesResult> {
  return withTransientHistoryRetry(() => loadXScienceLineageMessagesOnce(frameId, options), options.signal);
}

async function loadXScienceLineageMessagesOnce(
  frameId: string,
  options: XScienceLineageMessagesOptions = {}
): Promise<XScienceLineageMessagesResult> {
  const fetchImpl = options.fetchImpl ?? fetch;
  const query = new URLSearchParams({ limit: String(options.limit ?? 200) });
  const encodedFrameId = encodeURIComponent(frameId);
  const path = '/api/conversations/' + encodedFrameId + '/messages?' + query;
  const response = await fetchImpl(path, {
    headers: { accept: 'application/json' },
    signal: options.signal,
  });

  if (!response.ok) {
    const body = await readLineageErrorBody(response);
    throw new BackendHttpError({ method: 'GET', path, status: response.status, body });
  }

  const payload: unknown = await response.json();
  if (!isRecord(payload) || !Array.isArray(payload.items)) {
    throw new Error('X-Science lineage messages response is invalid');
  }

  return {
    items: payload.items.flatMap(normalizeLineageMessage),
    hasMoreBefore: payload.has_more_before === true,
  };
}

async function readLineageErrorBody(response: Response): Promise<unknown> {
  try {
    const contentType = response.headers.get('content-type')?.toLowerCase() ?? '';
    if (contentType.includes('application/json')) {
      return await response.json();
    }
    return await response.text();
  } catch {
    return '';
  }
}

function normalizeLineageMessage(value: unknown): XScienceLineageMessage[] {
  if (!isRecord(value)) return [];
  const id = stringValue(value.id) || stringValue(value.msg_id);
  if (!id) return [];
  const type = stringValue(value.type);
  const content = isRecord(value.content) ? value.content : null;
  const position = value.position === 'right' || value.position === 'center' ? value.position : 'left';
  const createdAt = typeof value.created_at === 'number' && Number.isFinite(value.created_at) ? value.created_at : null;

  if (type === 'text') {
    const text = content ? stringValue(content.content) : stringValue(value.content);
    return text ? [{ id, kind: 'text', position, content: text, createdAt }] : [];
  }
  if (type === 'thinking') {
    const text = content ? stringValue(content.content) : '';
    return text ? [{ id, kind: 'thinking', position: 'left', content: text, createdAt }] : [];
  }
  if (type === 'tool_call' || type === 'tool_group' || type === 'acp_tool_call') {
    const name = content ? stringValue(content.description) || stringValue(content.name) : '';
    const status = content ? stringValue(content.status) : '';
    return [
      {
        id,
        kind: 'tool',
        position: 'left',
        content: [name, status].filter(Boolean).join(' · '),
        createdAt,
      },
    ];
  }
  if (type === 'agent_status' || type === 'tips' || type === 'plan') {
    const text = content
      ? stringValue(content.content) || stringValue(content.message) || stringValue(content.status)
      : '';
    return text ? [{ id, kind: 'status', position: 'center', content: text, createdAt }] : [];
  }
  return [];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}
