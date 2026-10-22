import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureCommand,
  decodeLiteraturePage,
  decodeLiteratureReceipt,
  literatureValueIdentity,
  type LiteratureCommand,
  type LiteratureItem,
  type LiteratureReceipt,
} from '@/common/adapter/literatureLibrary';
import {
  pdfReadingSourceKey,
  samePdfReadingScope,
  type LiteraturePdfReadingSource,
  type PdfReadingContext,
} from '@/common/adapter/pdfReadingContext';
import { getRendererAccountScopeToken } from '../rendererAccountScope';
import { inspectPdfReadingCandidates, type PdfReadingOption } from './pdfReadingContext';

const requireAccount = (token: string) => {
  if (token !== getRendererAccountScopeToken()) throw new Error('Literature Library account changed');
};
export async function loadLiteraturePage(
  options: {
    query?: string;
    projectId?: string;
    cursor?: string;
    signal?: AbortSignal;
  } = {}
) {
  const account = getRendererAccountScopeToken();
  const query = new URLSearchParams({ limit: '20' });
  if (options.query?.trim()) query.set('query', options.query.trim());
  if (options.projectId) query.set('project_id', options.projectId);
  if (options.cursor) query.set('cursor', options.cursor);
  const raw = await httpRequest<unknown>('GET', `/api/literature/items?${query}`, undefined, {
    signal: options.signal,
    timeoutMs: 15_000,
  });
  requireAccount(account);
  return decodeLiteraturePage(raw);
}
export async function runLiteratureCommand(input: LiteratureCommand): Promise<LiteratureReceipt> {
  const account = getRendererAccountScopeToken();
  const command = decodeLiteratureCommand(input);
  const { kind, ...body } = command;
  const path =
    kind === 'import'
      ? '/api/literature/import-artifact-version'
      : `/api/literature/items/${encodeURIComponent(command.item_id!)}` +
        (kind === 'membership' ? `/projects/${encodeURIComponent(command.project_id!)}` : '');
  // Routing coordinates are not a competing payload identity.
  if ('item_id' in body) delete (body as Partial<typeof body>).item_id;
  if (kind === 'membership' && 'project_id' in body) delete (body as Partial<typeof body>).project_id;
  const raw = await httpRequest<unknown>(kind === 'import' ? 'POST' : kind === 'edit' ? 'PATCH' : 'PUT', path, body, {
    timeoutMs: 30_000,
  });
  requireAccount(account);
  const receipt = decodeLiteratureReceipt(raw, command.operation_id);
  if (command.kind !== 'import' && receipt.item.id !== command.item_id) throw new Error('Literature item changed');
  if (command.kind === 'import' && (!receipt.attachment_id || !receipt.version_id))
    throw new Error('Literature import not acknowledged');
  if (command.kind === 'membership' && receipt.item.project_ids.includes(command.project_id) !== command.included)
    throw new Error('Literature membership not acknowledged');
  if (
    command.kind === 'edit' &&
    (receipt.item.metadata_revision < command.expected_revision ||
      receipt.item.metadata_revision > command.expected_revision + 1 ||
      (command.title !== undefined && receipt.item.title !== command.title) ||
      (command.metadata !== undefined &&
        literatureValueIdentity(receipt.item.metadata) !== literatureValueIdentity(command.metadata)) ||
      (command.deleted !== undefined && receipt.item.deleted !== command.deleted))
  )
    throw new Error('Literature edit not acknowledged');
  return receipt;
}
export function literaturePdfOption(item: LiteratureItem): (PdfReadingOption & { item: LiteratureItem }) | undefined {
  if (item.deleted) return undefined;
  const candidates = item.attachments
    .flatMap((attachment) => attachment.versions.map((version) => ({ attachment, version })))
    .filter(
      ({ version }) =>
        version.available &&
        version.page_count > 1 &&
        version.size_bytes <= 50 * 1024 * 1024 &&
        (version.mime_type.split(';')[0].toLowerCase() === 'application/pdf' ||
          version.filename.toLowerCase().endsWith('.pdf'))
    )
    .toSorted(
      (left, right) =>
        Number(right.attachment.kind === 'fullText') - Number(left.attachment.kind === 'fullText') ||
        right.version.version_number - left.version.version_number
    );
  const chosen = candidates[0];
  if (!chosen) return undefined;
  const source: LiteraturePdfReadingSource = {
    source_kind: 'literature-attachment-version',
    item_id: item.id,
    attachment_id: chosen.attachment.id,
    version_id: chosen.version.id,
  };
  return { source, name: item.title || chosen.version.filename, pageCount: chosen.version.page_count, item };
}
export async function loadLibraryReadingOptions(
  context: PdfReadingContext,
  options: {
    query?: string;
    projectId?: string;
    cursor?: string;
    signal: AbortSignal;
  }
) {
  const page = await loadLiteraturePage(options);
  const proposed = page.items.flatMap((item) => {
    const option = literaturePdfOption(item);
    return option ? [option] : [];
  });
  const inspected = proposed.length
    ? await inspectPdfReadingCandidates(
        context.scope.frame_id,
        proposed.map((option) => option.source),
        options.signal
      )
    : null;
  if (inspected && !samePdfReadingScope(inspected.scope, context.scope))
    throw new Error('Library reading scope changed');
  const eligible = new Set(
    inspected?.candidates
      .filter((candidate) => candidate.eligible)
      .map((candidate) => pdfReadingSourceKey(candidate.source))
  );
  return {
    options: proposed.filter((option) => eligible.has(pdfReadingSourceKey(option.source))),
    unavailable: inspected?.candidates.filter((candidate) => !candidate.eligible).length ?? 0,
    nextCursor: page.next_cursor,
  };
}
