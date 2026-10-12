export type XScienceFileImportFailureKind =
  | 'permission'
  | 'not_found'
  | 'not_a_directory'
  | 'not_a_file'
  | 'too_large'
  | 'outside_roots'
  | 'connection'
  | 'unknown';

const knownKinds = new Set<XScienceFileImportFailureKind>([
  'permission',
  'not_found',
  'not_a_directory',
  'not_a_file',
  'too_large',
  'outside_roots',
  'connection',
  'unknown',
]);

export class XScienceFileImportError extends Error {
  readonly status: number;
  readonly kind: XScienceFileImportFailureKind;

  constructor(status: number, kind: XScienceFileImportFailureKind) {
    super(`X-Science file import failed (${status}) [${kind}]`);
    this.name = 'XScienceFileImportError';
    this.status = status;
    this.kind = kind;
  }
}

export const isXScienceFileImportError = (error: unknown): error is XScienceFileImportError =>
  error instanceof XScienceFileImportError ||
  Boolean(
    error &&
    typeof error === 'object' &&
    'name' in error &&
    error.name === 'XScienceFileImportError' &&
    'status' in error &&
    typeof error.status === 'number' &&
    'kind' in error &&
    typeof error.kind === 'string' &&
    knownKinds.has(error.kind as XScienceFileImportFailureKind)
  );

export async function readXScienceFileImportResponse(response: Response): Promise<unknown> {
  const text = await response.text();
  let payload: unknown;
  if (text) {
    try {
      payload = JSON.parse(text) as unknown;
    } catch {
      payload = null;
    }
  }
  if (!response.ok) {
    throw new XScienceFileImportError(response.status, importFailureKind(response.status, payload));
  }
  return payload;
}

function importFailureKind(status: number, payload: unknown): XScienceFileImportFailureKind {
  if (payload && typeof payload === 'object' && !Array.isArray(payload)) {
    const record = payload as Record<string, unknown>;
    const candidate = record.remoteKind ?? record.kind ?? record.code;
    if (typeof candidate === 'string' && knownKinds.has(candidate as XScienceFileImportFailureKind)) {
      return candidate as XScienceFileImportFailureKind;
    }
  }
  if (status === 401 || status === 403) return 'permission';
  if (status === 404) return 'not_found';
  if (status === 413) return 'too_large';
  if (status === 502 || status === 503 || status === 504) return 'connection';
  return 'unknown';
}
