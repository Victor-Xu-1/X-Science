import { describe, expect, it } from 'vitest';
import { decodeLiteratureCommand, type LiteratureCommand } from '@/common/adapter/literatureLibrary';
import {
  LITERATURE_METADATA_PATCH_BYTES,
  LITERATURE_METADATA_SNAPSHOT_BYTES,
  literatureMetadataEncodedBytes,
} from '@/common/adapter/literatureMetadataCapacity';
import {
  decodePendingRecord,
  encodePendingRecord,
  samePending,
} from '@/renderer/services/runtime/literatureUploadIntent';

const operationId = '00000000-0000-4000-8000-000000000605';
const owner = 'capacity-owner';

describe('Library future metadata command and pending receiver', () => {
  it('retains the complete note and ordinary metadata through every full command and pending replay', () => {
    const metadata = { abstract: 'a'.repeat(16000), personal_note: '\u0001'.repeat(10000) };
    const bytes = literatureMetadataEncodedBytes(JSON.stringify(metadata));
    expect(bytes).toBeGreaterThan(65536);
    expect(bytes).toBeLessThan(LITERATURE_METADATA_SNAPSHOT_BYTES);
    const commands: LiteratureCommand[] = [
      { kind: 'manual-create', operation_id: operationId, title: 'Synthetic metadata', metadata },
      {
        kind: 'import',
        operation_id: operationId,
        artifact_id: 'source',
        version_id: 'source-version',
        title: 'Synthetic metadata',
        metadata,
      },
      {
        kind: 'edit',
        operation_id: operationId,
        item_id: 'paper',
        expected_revision: 7,
        metadata,
      },
    ];
    for (const command of commands) {
      const decoded = decodeLiteratureCommand(command);
      expect(decoded).toStrictEqual(command);
      const pending = decodePendingRecord(JSON.parse(encodePendingRecord(owner, decoded)), owner);
      expect(samePending(pending, command)).toBe(true);
      expect(pending).toStrictEqual(command);
    }
  });

  it('accepts a bounded note update plus removal of an old ordinary field without reconstructing it', () => {
    const command: LiteratureCommand = {
      kind: 'edit-metadata',
      operation_id: operationId,
      item_id: 'paper',
      expected_revision: 7,
      metadata_patch: { set: { personal_note: '\u0001'.repeat(10000) }, remove: ['k'.repeat(16000)] },
    };
    expect(new TextEncoder().encode(JSON.stringify(command.metadata_patch)).length).toBeGreaterThan(65536);
    const decoded = decodeLiteratureCommand(command);
    expect(decoded).toStrictEqual(command);
    expect(decodePendingRecord(JSON.parse(encodePendingRecord(owner, decoded)), owner)).toStrictEqual(command);
  });

  it('measures finite metadata bytes with canonical escaping and rejects oversized snapshots and actions', () => {
    expect(literatureMetadataEncodedBytes(JSON.stringify({ personal_note: '<>&\u2028\u2029' }))).toBe(50);
    expect(() =>
      decodeLiteratureCommand({
        kind: 'manual-create',
        operation_id: operationId,
        title: 'Synthetic metadata',
        metadata: { personal_note: '<'.repeat(LITERATURE_METADATA_SNAPSHOT_BYTES / 6) },
      })
    ).toThrow('Invalid literature');
    expect(() =>
      decodeLiteratureCommand({
        kind: 'edit-metadata',
        operation_id: operationId,
        item_id: 'paper',
        expected_revision: 7,
        metadata_patch: { set: { personal_note: 'x'.repeat(LITERATURE_METADATA_PATCH_BYTES) } },
      })
    ).toThrow('Invalid literature');
  });
});
