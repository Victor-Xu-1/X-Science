import { httpRequest } from '@/common/adapter/httpBridge';
import {
  prepareResourceTagCommand,
  decodeResourceTagReceipt,
  decodeResourceTagSnapshot,
  type ResourceTagCommand,
  type ResourceTagReceipt,
  type ResourceTagSnapshot,
} from '@/common/adapter/resourceTags';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

export type ResourceTagRequestOptions = { signal?: AbortSignal; accountToken?: string };
const requireScope = (account: string, signal?: AbortSignal) => {
  if (account !== getRendererAccountScopeToken()) throw new Error('Resource tag account changed');
  signal?.throwIfAborted();
};
async function request(
  method: string,
  path: string,
  body: unknown,
  options: ResourceTagRequestOptions,
  timeoutMs: number
): Promise<unknown> {
  const { signal, accountToken } = options;
  const account = accountToken ?? getRendererAccountScopeToken();
  requireScope(account, signal);
  try {
    const raw = await httpRequest<unknown>(method, path, body, { signal, timeoutMs });
    requireScope(account, signal);
    return raw;
  } catch (error) {
    requireScope(account, signal);
    throw error;
  }
}
/** A canonical current read. Mutation receipt snapshots must not silently replace it after recovery. */
export async function loadResourceTags(options: ResourceTagRequestOptions = {}): Promise<ResourceTagSnapshot> {
  return decodeResourceTagSnapshot(await request('GET', '/api/tags', undefined, options, 15_000));
}
/** One explicit caller-owned operation. No automatic retry, key allocation, store or snapshot projection. */
export async function runResourceTagCommand(
  input: ResourceTagCommand,
  options: ResourceTagRequestOptions = {}
): Promise<ResourceTagReceipt> {
  const { command, method, path, body } = prepareResourceTagCommand(input);
  return decodeResourceTagReceipt(await request(method, path, body, options, 30_000), command);
}
