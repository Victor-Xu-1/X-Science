import { requestXScienceJson, type XScienceGatewayOptions } from '../xScienceHttp';
import {
  normalizeConversationStreamingSnapshot,
  type ConversationStreamingSnapshot,
} from './conversationStreamingModel';

export type ConversationStreamingOptions = XScienceGatewayOptions & {
  signal?: AbortSignal;
};

export async function loadConversationStreamingSnapshot(
  rootFrameId: string,
  options: ConversationStreamingOptions = {}
): Promise<ConversationStreamingSnapshot> {
  const payload = await requestXScienceJson<unknown>(
    `/api/frames/${encodeURIComponent(rootFrameId)}/streaming-batch`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: '{}',
      signal: options.signal,
    },
    options
  );
  return normalizeConversationStreamingSnapshot(payload);
}
