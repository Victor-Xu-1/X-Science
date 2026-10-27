import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { requestJson, throwIfAttachmentAborted } from './onboardingAttachmentTransport';

export type OnboardingAttachmentInitRequest = Readonly<{
  operation_id: string;
  project_id: string;
  filename: string;
  total_size: number;
  content_type: string;
  chunk_size: number;
  folder_id?: string;
  ephemeral?: boolean;
}>;

export type OnboardingAttachmentInitReceipt = Readonly<{
  upload_id: string;
  chunk_size: number;
  total_chunks: number;
}>;

const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const positiveInteger = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0;

export function decodeOnboardingAttachmentInitReceipt(value: unknown): OnboardingAttachmentInitReceipt {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Attachment admission acknowledgement is invalid');
  }
  const receipt = value as Record<string, unknown>;
  if (
    typeof receipt.upload_id !== 'string' ||
    !canonicalUUID.test(receipt.upload_id) ||
    !positiveInteger(receipt.chunk_size) ||
    !positiveInteger(receipt.total_chunks)
  ) {
    throw new Error('Attachment admission acknowledgement is invalid');
  }
  return { upload_id: receipt.upload_id, chunk_size: receipt.chunk_size, total_chunks: receipt.total_chunks };
}

function requireInitRequest(raw: OnboardingAttachmentInitRequest): void {
  if (
    !raw ||
    typeof raw.operation_id !== 'string' ||
    !canonicalUUID.test(raw.operation_id) ||
    typeof raw.project_id !== 'string' ||
    !raw.project_id.trim() ||
    typeof raw.filename !== 'string' ||
    !raw.filename.trim() ||
    !positiveInteger(raw.total_size) ||
    typeof raw.content_type !== 'string' ||
    !Number.isSafeInteger(raw.chunk_size) ||
    raw.chunk_size < 0 ||
    (raw.folder_id !== undefined && typeof raw.folder_id !== 'string') ||
    (raw.ephemeral !== undefined && typeof raw.ephemeral !== 'boolean')
  ) {
    throw new Error('Attachment admission request is invalid');
  }
}

/** One caller-owned admission request; retries belong to its durable intent owner. */
export async function initializeXScienceProjectAttachment(
  raw: OnboardingAttachmentInitRequest,
  options: { signal?: AbortSignal; fetchImpl?: typeof fetch } = {}
): Promise<OnboardingAttachmentInitReceipt> {
  throwIfAttachmentAborted(options.signal);
  requireInitRequest(raw);
  const payload = await requestJson('/api/artifacts/upload/init', {
    fetchImpl: options.fetchImpl ?? fetch,
    init: {
      method: 'POST',
      signal: options.signal,
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': raw.operation_id },
      body: JSON.stringify(raw),
    },
  });
  throwIfAttachmentAborted(options.signal);
  return decodeOnboardingAttachmentInitReceipt(payload);
}

/** Only the opted-in endpoint's explicit certainty can release a reserved intent. */
export function classifyXScienceAttachmentInitFailure(error: unknown): 'unknown' | 'rejected' | 'conflict' {
  if (!isBackendHttpError(error)) return 'unknown';
  if (error.code === 'ATTACHMENT_INIT_OUTCOME_UNKNOWN') return 'unknown';
  if (error.code === 'ATTACHMENT_INIT_IDEMPOTENCY_CONFLICT' && error.status === 409) return 'conflict';
  if (
    error.code === 'ATTACHMENT_INIT_WRITE_REJECTED' &&
    (error.status === 400 || error.status === 404 || error.status === 507)
  ) {
    return 'rejected';
  }
  return 'unknown';
}
