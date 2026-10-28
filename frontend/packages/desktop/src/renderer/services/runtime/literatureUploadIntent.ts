import {
  decodeLiteratureCandidateCommand,
  type LiteratureCandidateCommand,
  type LiteratureCandidateReceipt,
} from '@/common/adapter/literatureCandidates';
import {
  decodeLiteratureCommand,
  literatureValueIdentity,
  type LiteratureCommand,
  type LiteratureReceipt,
} from '@/common/adapter/literatureLibrary';
import {
  decodeLiteratureCollectionCommand,
  type LiteratureCollectionCommand,
  type LiteratureCollectionReceipt,
} from '@/common/adapter/literatureCollections';
import {
  decodeLiteraturePublicationCommand,
  type LiteraturePublicationCommand,
  type LiteraturePublicationReceipt,
} from '@/common/adapter/literaturePublicationCheck';
import {
  decodeLiteratureReadingStatusCommand,
  type LiteratureReadingStatusCommand,
  type LiteratureReadingStatusReceipt,
} from '@/common/adapter/literatureReadingStatus';
import {
  decodeOnboardingAttachmentInitReceipt,
  type OnboardingAttachmentInitRequest,
  type OnboardingAttachmentInitReceipt,
  type OnboardingUploadedArtifact,
} from '../onboardingService';
import {
  decodeLiteratureReadingBulk,
  isLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from './literatureReadingBulkIntent';

export type LiteratureMutationCommand =
  | LiteratureCommand
  | LiteratureCollectionCommand
  | LiteraturePublicationCommand
  | LiteratureReadingStatusCommand
  | LiteratureCandidateCommand;
export type LiteratureMutationReceipt =
  | LiteratureReceipt
  | LiteratureCollectionReceipt
  | LiteraturePublicationReceipt
  | LiteratureReadingStatusReceipt
  | LiteratureCandidateReceipt;
export type ReceiptFor<Command extends LiteratureMutationCommand> = Command extends LiteratureCandidateCommand
  ? LiteratureCandidateReceipt
  : Command extends LiteratureCollectionCommand
    ? LiteratureCollectionReceipt
    : Command extends LiteraturePublicationCommand
      ? LiteraturePublicationReceipt
      : Command extends LiteratureReadingStatusCommand
        ? LiteratureReadingStatusReceipt
        : LiteratureReceipt;
export type LibraryPdfRawInit = Readonly<
  Required<
    Pick<
      OnboardingAttachmentInitRequest,
      'operation_id' | 'project_id' | 'filename' | 'total_size' | 'content_type' | 'chunk_size'
    >
  >
> & { readonly chunk_size: 0 };
type UploadBase = Readonly<{
  schema: 'x-science.library-pdf-upload.v1';
  kind: 'pdf-upload';
  operation_id: string;
  init: LibraryPdfRawInit;
  file_sha256: string;
  title: string;
}>;
type CatalogueImport = Extract<LiteratureCommand, { kind: 'import' }>;
export type LiteratureUploadIntent = UploadBase &
  (
    | { readonly phase: 'init' }
    | { readonly phase: 'upload'; readonly init_ack: Readonly<OnboardingAttachmentInitReceipt> }
    | {
        readonly phase: 'catalogue';
        readonly init_ack: Readonly<OnboardingAttachmentInitReceipt>;
        readonly artifact: Readonly<OnboardingUploadedArtifact>;
        readonly command: Readonly<CatalogueImport>;
        readonly catalogue_rejected: boolean;
      }
  );
export type LiteraturePending = LiteratureMutationCommand | LiteratureUploadIntent | LiteratureReadingBulkIntent;

export class OriginalPendingConflict extends Error {
  constructor() {
    super('Resolve the original Library change first');
  }
}
const record = (value: unknown): value is Record<string, unknown> =>
  Boolean(value && typeof value === 'object' && !Array.isArray(value));
const identity = (value: unknown): value is string =>
  typeof value === 'string' && Boolean(value) && value.length <= 4096 && value.trim() === value;
const uuid = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value);
const positive = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
const fail = (): never => {
  throw new Error('Invalid Library upload pending contract');
};
const exactKeys = (value: Record<string, unknown>, keys: string[]) =>
  Reflect.ownKeys(value).length === keys.length &&
  Reflect.ownKeys(value).every((key) => typeof key === 'string' && keys.includes(key));

export const isUploadIntent = (pending: LiteraturePending): pending is LiteratureUploadIntent =>
  pending.kind === 'pdf-upload';
export const isCandidateCommand = (command: LiteratureMutationCommand): command is LiteratureCandidateCommand =>
  command.kind.startsWith('candidate-');
export const isCollectionCommand = (command: LiteratureMutationCommand): command is LiteratureCollectionCommand =>
  command.kind.startsWith('collection-');
export function decodeMutationCommand(value: unknown): LiteratureMutationCommand {
  if (record(value) && typeof value.kind === 'string' && value.kind.startsWith('candidate-'))
    return decodeLiteratureCandidateCommand(value);
  if (record(value) && value.kind === 'check-publication') return decodeLiteraturePublicationCommand(value);
  if (record(value) && value.kind === 'set-reading-status') return decodeLiteratureReadingStatusCommand(value);
  return record(value) && typeof value.kind === 'string' && value.kind.startsWith('collection-')
    ? decodeLiteratureCollectionCommand(value)
    : decodeLiteratureCommand(value);
}
export const samePending = (left: LiteraturePending, right: LiteraturePending): boolean =>
  literatureValueIdentity(left) === literatureValueIdentity(right);
export function uploadOriginIdentity(intent: LiteratureUploadIntent): string {
  return literatureValueIdentity({
    schema: intent.schema,
    kind: intent.kind,
    operation_id: intent.operation_id,
    init: intent.init,
    file_sha256: intent.file_sha256,
    title: intent.title,
  });
}
export function sameUploadOrigin(left: LiteratureUploadIntent, right: LiteratureUploadIntent): boolean {
  return uploadOriginIdentity(left) === uploadOriginIdentity(right);
}
export function currentUploadPending(
  current: LiteraturePending | null,
  expected: LiteratureUploadIntent
): LiteratureUploadIntent {
  const rank = { init: 0, upload: 1, catalogue: 2 };
  if (
    !current ||
    !isUploadIntent(current) ||
    !sameUploadOrigin(current, expected) ||
    rank[current.phase] < rank[expected.phase]
  )
    throw new OriginalPendingConflict();
  return current;
}
export function matchingSettlement(current: LiteraturePending | null, expected: LiteraturePending, clear: boolean) {
  const remove = Boolean(clear && current && samePending(current, expected));
  return { remove, pending: remove ? null : current };
}
export function encodePendingRecord(ownerId: string, pending: LiteraturePending): string {
  return JSON.stringify(
    isUploadIntent(pending)
      ? { ownerId, upload: pending }
      : isLiteratureReadingBulk(pending)
        ? { ownerId, bulk: pending }
        : { ownerId, command: pending }
  );
}
export function decodePendingRecord(value: unknown, ownerId: string): LiteraturePending {
  if (!record(value) || value.ownerId !== ownerId) return fail();
  if (exactKeys(value, ['ownerId', 'command'])) return decodeMutationCommand(value.command);
  if (exactKeys(value, ['ownerId', 'upload'])) return decodeUploadIntent(value.upload);
  if (exactKeys(value, ['ownerId', 'bulk'])) return decodeLiteratureReadingBulk(value.bulk);
  return fail();
}

function decodeRawInit(value: unknown): LibraryPdfRawInit {
  if (
    !record(value) ||
    !exactKeys(value, ['operation_id', 'project_id', 'filename', 'total_size', 'content_type', 'chunk_size']) ||
    !uuid(value.operation_id) ||
    !identity(value.project_id) ||
    typeof value.filename !== 'string' ||
    !value.filename.trim() ||
    value.filename.length > 4096 ||
    !positive(value.total_size) ||
    !identity(value.content_type) ||
    value.chunk_size !== 0
  )
    return fail();
  return Object.freeze({
    operation_id: value.operation_id,
    project_id: value.project_id,
    filename: value.filename,
    total_size: value.total_size,
    content_type: value.content_type,
    chunk_size: 0,
  });
}
function decodeBase(value: Record<string, unknown>): UploadBase {
  if (
    value.schema !== 'x-science.library-pdf-upload.v1' ||
    value.kind !== 'pdf-upload' ||
    !uuid(value.operation_id) ||
    typeof value.title !== 'string' ||
    !value.title ||
    value.title.trim() !== value.title ||
    value.title.length > 4096 ||
    typeof value.file_sha256 !== 'string' ||
    !/^[a-f0-9]{64}$/.test(value.file_sha256)
  )
    return fail();
  const init = decodeRawInit(value.init);
  if (init.operation_id === value.operation_id) return fail();
  return Object.freeze({
    schema: value.schema,
    kind: value.kind,
    operation_id: value.operation_id,
    init,
    file_sha256: value.file_sha256,
    title: value.title,
  });
}
function initReceipt(base: UploadBase, value: unknown): Readonly<OnboardingAttachmentInitReceipt> {
  const ack = decodeOnboardingAttachmentInitReceipt(value);
  if (ack.total_chunks !== Math.ceil(base.init.total_size / ack.chunk_size)) return fail();
  return Object.freeze({ ...ack });
}
function artifactReceipt(base: UploadBase, value: unknown): Readonly<OnboardingUploadedArtifact> {
  if (
    !record(value) ||
    !exactKeys(value, ['artifactId', 'versionId', 'filename', 'sizeBytes', 'checksum']) ||
    !identity(value.artifactId) ||
    !identity(value.versionId) ||
    !identity(value.filename) ||
    value.sizeBytes !== base.init.total_size ||
    typeof value.checksum !== 'string' ||
    !/^[a-f0-9]{64}$/.test(value.checksum) ||
    value.checksum !== base.file_sha256
  )
    return fail();
  return Object.freeze({
    artifactId: value.artifactId,
    versionId: value.versionId,
    filename: value.filename,
    sizeBytes: value.sizeBytes as number,
    checksum: value.checksum,
  });
}
function importCommand(base: UploadBase, artifact: OnboardingUploadedArtifact): Readonly<CatalogueImport> {
  return Object.freeze(
    decodeLiteratureCommand({
      kind: 'import',
      operation_id: base.operation_id,
      artifact_id: artifact.artifactId,
      version_id: artifact.versionId,
      title: base.title,
      project_id: base.init.project_id,
    }) as CatalogueImport
  );
}
export function decodeUploadIntent(value: unknown): LiteratureUploadIntent {
  if (!record(value)) return fail();
  const base = decodeBase(value);
  const keys = ['schema', 'kind', 'operation_id', 'init', 'file_sha256', 'title', 'phase'];
  if (value.phase === 'init' && exactKeys(value, keys)) return Object.freeze({ ...base, phase: 'init' });
  if (value.phase === 'upload' && exactKeys(value, [...keys, 'init_ack']))
    return Object.freeze({ ...base, phase: 'upload', init_ack: initReceipt(base, value.init_ack) });
  if (
    value.phase === 'catalogue' &&
    typeof value.catalogue_rejected === 'boolean' &&
    exactKeys(value, [...keys, 'init_ack', 'artifact', 'command', 'catalogue_rejected'])
  ) {
    const artifact = artifactReceipt(base, value.artifact);
    const command = importCommand(base, artifact);
    if (literatureValueIdentity(value.command) !== literatureValueIdentity(command)) return fail();
    return Object.freeze({
      ...base,
      phase: 'catalogue',
      init_ack: initReceipt(base, value.init_ack),
      artifact,
      command,
      catalogue_rejected: value.catalogue_rejected,
    });
  }
  return fail();
}
export function createUploadIntent(input: {
  projectId: string;
  title: string;
  filename: string;
  size: number;
  contentType: string;
  hash: string;
  initOperationId: string;
  catalogueOperationId: string;
}): LiteratureUploadIntent {
  return decodeUploadIntent({
    schema: 'x-science.library-pdf-upload.v1',
    kind: 'pdf-upload',
    phase: 'init',
    operation_id: input.catalogueOperationId,
    title: input.title.trim(),
    file_sha256: input.hash,
    init: {
      operation_id: input.initOperationId,
      project_id: input.projectId,
      filename: input.filename,
      total_size: input.size,
      content_type: input.contentType,
      chunk_size: 0,
    },
  });
}
export function withInitReceipt(intent: LiteratureUploadIntent, ack: unknown): LiteratureUploadIntent {
  if (intent.phase !== 'init') return fail();
  return decodeUploadIntent({ ...intent, phase: 'upload', init_ack: ack });
}
export function withArtifactReceipt(
  intent: LiteratureUploadIntent,
  artifact: OnboardingUploadedArtifact
): LiteratureUploadIntent {
  if (intent.phase !== 'upload') return fail();
  return decodeUploadIntent({
    ...intent,
    phase: 'catalogue',
    artifact,
    command: importCommand(intent, artifact),
    catalogue_rejected: false,
  });
}
export function withCatalogueRejection(intent: LiteratureUploadIntent, rejected: boolean): LiteratureUploadIntent {
  if (intent.phase !== 'catalogue') return fail();
  return decodeUploadIntent({ ...intent, catalogue_rejected: rejected });
}

/** Consume only the existing catalogue's exact typed outcomes; conflicts never prove discard. */
export function catalogueUploadFailureCertainty(status: number, code: string): 'unknown' | 'rejected' | 'conflict' {
  if (status === 409 && code === 'LITERATURE_CONFLICT') return 'conflict';
  return (status === 400 && code === 'LITERATURE_INVALID') || (status === 404 && code === 'LITERATURE_UNAVAILABLE')
    ? 'rejected'
    : 'unknown';
}
