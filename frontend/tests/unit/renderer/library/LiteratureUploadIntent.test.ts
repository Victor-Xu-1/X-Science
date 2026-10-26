import { describe, expect, it } from 'vitest';
import {
  catalogueUploadFailureCertainty,
  createUploadIntent,
  currentUploadPending,
  decodePendingRecord,
  decodeUploadIntent,
  encodePendingRecord,
  matchingSettlement,
  sameUploadOrigin,
  withArtifactReceipt,
  withInitReceipt,
} from '@/renderer/services/runtime/literatureUploadIntent';

const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const hash = 'a'.repeat(64);
const intent = (n = 1) =>
  createUploadIntent({
    projectId: 'owned-project',
    title: 'Title',
    filename: 'reference.pdf',
    size: 3,
    contentType: 'application/pdf',
    hash,
    initOperationId: id(n + 1),
    catalogueOperationId: id(n),
  });
const ack = { upload_id: id(3), chunk_size: 2, total_chunks: 2 };
const artifact = {
  artifactId: 'artifact-original',
  versionId: 'source-version-original',
  filename: 'reference.pdf',
  sizeBytes: 3,
  checksum: hash,
};

describe('Library PDF durable intent', () => {
  it('preserves legacy commands and rejects unknown or mixed durable records', () => {
    const command = {
      kind: 'import',
      operation_id: id(1),
      artifact_id: 'artifact',
      version_id: 'source-version',
      title: 'Title',
    };
    expect(decodePendingRecord({ ownerId: 'owner', command }, 'owner')).toEqual(command);
    const encoded = JSON.parse(encodePendingRecord('owner', intent()));
    expect(encoded).not.toHaveProperty('command');
    expect(decodePendingRecord(encoded, 'owner')).toEqual(intent());
    for (const value of [
      { ownerId: 'owner', upload: intent(), command },
      { ownerId: 'other', upload: intent() },
      { ownerId: 'owner', upload: { ...intent(), schema: 'future' } },
      { ownerId: 'owner', other: intent() },
    ])
      expect(() => decodePendingRecord(value, 'owner')).toThrow();
  });

  it('freezes raw admission and distinct UUIDs before phase advancement', () => {
    const pending = intent();
    expect(pending.init.chunk_size).toBe(0);
    expect(pending.init.operation_id).not.toBe(pending.operation_id);
    expect(Object.isFrozen(pending)).toBe(true);
    expect(Object.isFrozen(pending.init)).toBe(true);
    for (const value of [
      { ...pending, init: { ...pending.init, operation_id: pending.operation_id } },
      { ...pending, file_sha256: 'metadata-is-not-content' },
      { ...pending, title: ' Title ' },
      { ...pending, init: { ...pending.init, ephemeral: true } },
    ])
      expect(() => decodeUploadIntent(value)).toThrow();
  });

  it('uses the canonical init decoder and validates frozen chunk coordinates', () => {
    expect(withInitReceipt(intent(), ack)).toMatchObject({ phase: 'upload', init_ack: ack });
    for (const bad of [
      { ...ack, chunk_size: 0 },
      { ...ack, total_chunks: 1 },
      { ...ack, upload_id: '' },
    ])
      expect(() => withInitReceipt(intent(), bad)).toThrow();
  });

  it('persists only the exact original typed import after immutable artifact ACK', () => {
    const pending = withArtifactReceipt(withInitReceipt(intent(), ack), artifact);
    expect(pending).toMatchObject({
      phase: 'catalogue',
      catalogue_rejected: false,
      artifact,
      command: {
        kind: 'import',
        operation_id: id(1),
        artifact_id: artifact.artifactId,
        version_id: artifact.versionId,
        project_id: 'owned-project',
        title: 'Title',
      },
    });
    if (pending.phase !== 'catalogue') throw new Error('Expected catalogue phase');
    expect(() => decodeUploadIntent({ ...pending, command: { ...pending.command, operation_id: id(5) } })).toThrow();
    expect(() => decodeUploadIntent({ ...pending, artifact: { ...artifact, sizeBytes: 4 } })).toThrow();
    expect(() =>
      withArtifactReceipt(withInitReceipt(intent(), ack), { ...artifact, checksum: 'b'.repeat(64) })
    ).toThrow();
    expect(() => decodeUploadIntent({ ...pending, catalogue_rejected: 'true' })).toThrow();
    expect(decodePendingRecord(JSON.parse(encodePendingRecord('owner', pending)), 'owner')).toEqual(pending);
  });

  it('allows only monotonic same-origin progress and preserves the next operation', () => {
    const original = intent();
    const advanced = withInitReceipt(original, ack);
    expect(sameUploadOrigin(original, advanced)).toBe(true);
    expect(currentUploadPending(advanced, original)).toEqual(advanced);
    expect(() => currentUploadPending(original, advanced)).toThrow();
    expect(() => currentUploadPending(intent(5), original)).toThrow();
    expect(() => currentUploadPending(null, original)).toThrow();
    expect(matchingSettlement(intent(5), original, true)).toEqual({ remove: false, pending: intent(5) });
  });

  it('never treats catalogue conflicts or untyped outcomes as discard proof', () => {
    expect(catalogueUploadFailureCertainty(400, 'LITERATURE_INVALID')).toBe('rejected');
    expect(catalogueUploadFailureCertainty(404, 'LITERATURE_UNAVAILABLE')).toBe('rejected');
    expect(catalogueUploadFailureCertainty(409, 'LITERATURE_CONFLICT')).toBe('conflict');
    for (const [status, code] of [
      [400, ''],
      [400, 'LITERATURE_INTERNAL'],
      [500, 'LITERATURE_INVALID'],
      [503, 'OUTCOME_UNKNOWN'],
    ] as const)
      expect(catalogueUploadFailureCertainty(status, code)).toBe('unknown');
  });
});
