import {
  decodeLiteratureStudy,
  LiteratureStudyScopeError,
  type LiteratureStudyGenerate,
  type LiteratureStudyView,
} from '@/common/adapter/literatureStudySnapshot';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';

export class LiteratureStudyRequestError extends Error {
  constructor(
    readonly code: string,
    readonly scopeChanged: boolean
  ) {
    super(code);
  }
}
async function performLiteratureStudy(
  item: LiteratureItem,
  language: string,
  signal: AbortSignal,
  command?: LiteratureStudyGenerate
): Promise<LiteratureStudyView> {
  const url = `/api/literature/items/${encodeURIComponent(item.id)}/study-snapshot`;
  const response = await fetch(command ? url : `${url}?language=${encodeURIComponent(language)}`, {
    method: command ? 'POST' : 'GET',
    credentials: 'include',
    cache: 'no-store',
    signal,
    headers: { Accept: 'application/json', ...(command ? { 'Content-Type': 'application/json' } : {}) },
    ...(command ? { body: JSON.stringify(command) } : {}),
  });
  if (!response.ok) {
    let code = 'LITERATURE_STUDY_FAILED';
    try {
      const body: unknown = await response.json();
      if (body && typeof body === 'object' && 'code' in body && typeof body.code === 'string') code = body.code;
    } catch {
      /* Preserve generic error for malformed failures. */
    }
    throw new LiteratureStudyRequestError(
      code,
      [401, 403, 404].includes(response.status) || code === 'LITERATURE_STUDY_SOURCE_CHANGED'
    );
  }
  try {
    const view = decodeLiteratureStudy(await response.json(), item);
    if (
      command &&
      (view.source_key !== command.expected_source_key ||
        !view.snapshot ||
        view.revision !== command.expected_revision + 1)
    )
      throw new Error('Study response does not prove this publication');
    return view;
  } catch (error) {
    if (error instanceof LiteratureStudyScopeError)
      throw new LiteratureStudyRequestError('LITERATURE_STUDY_SOURCE_CHANGED', true);
    throw error;
  }
}

/** Bound the complete response/decoder, even when a transport ignores abort. */
export async function requestLiteratureStudy(
  item: LiteratureItem,
  language: string,
  signal: AbortSignal,
  command?: LiteratureStudyGenerate
): Promise<LiteratureStudyView> {
  if (signal.aborted) throw new DOMException('Study overview request aborted', 'AbortError');
  const controller = new AbortController();
  let deadline: ReturnType<typeof setTimeout> | undefined;
  let stop!: () => void;
  const stopped = new Promise<never>((_, reject) => {
    stop = () => {
      controller.abort();
      reject(new DOMException('Study overview request aborted', 'AbortError'));
    };
    signal.addEventListener('abort', stop, { once: true });
    if (signal.aborted) stop();
  });
  const timedOut = new Promise<never>((_, reject) => {
    deadline = setTimeout(
      () => {
        reject(new LiteratureStudyRequestError('LITERATURE_STUDY_CANCELLED', false));
        controller.abort();
      },
      command ? 190_000 : 10_000
    );
  });
  try {
    return await Promise.race([performLiteratureStudy(item, language, controller.signal, command), stopped, timedOut]);
  } finally {
    clearTimeout(deadline);
    signal.removeEventListener('abort', stop);
  }
}
