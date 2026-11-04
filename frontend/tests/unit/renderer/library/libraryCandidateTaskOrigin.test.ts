import { describe, expect, it } from 'vitest';
import {
  decodeLiteratureCandidate,
  decodeLiteratureCandidateCommand,
  type LiteratureTaskOrigin,
} from '@/common/adapter/literatureCandidates';

const origin: LiteratureTaskOrigin = {
  kind: 'agent',
  frame_id: 'task-child',
  frame_incarnation_id: 'task-child-run',
  root_frame_id: 'task-root',
  root_frame_incarnation_id: 'task-root-run',
  project_id: 'alpha',
  project_available: true,
  recorded_at: '2026-11-04T02:03:04.123456789Z',
};
const candidate = {
  id: 'candidate-one',
  title: 'Task discovery',
  metadata: {},
  provider: 'crossref',
  external_id: '10.1000/task-discovery',
  state: 'pending',
};

describe('Library task-origin read contract', () => {
  it('preserves trusted server identities and leaves legacy bibliographic metadata untrusted', () => {
    expect(decodeLiteratureCandidate({ ...candidate, task_origins: [origin] }).task_origins).toEqual([origin]);
    expect(decodeLiteratureCandidate(candidate)).not.toHaveProperty('task_origins');
    expect(decodeLiteratureCandidate({ ...candidate, metadata: { task_origins: [origin] } })).not.toHaveProperty(
      'task_origins'
    );
    const { project_id: _project, project_available: _available, ...withoutProject } = origin;
    expect(
      decodeLiteratureCandidate({
        ...candidate,
        task_origins: [withoutProject],
        state: 'accepted',
        item_id: 'item-one',
      }).task_origins
    ).toEqual([withoutProject]);
    expect(decodeLiteratureCandidate({ ...candidate, task_origins: [] }).task_origins).toEqual([]);
    expect(
      decodeLiteratureCandidate({ ...candidate, task_origins: [{ ...origin, frame_id: '字'.repeat(170) + 'xx' }] })
        .task_origins?.[0].frame_id
    ).toHaveLength(172);
  });

  it.each(
    [
      null,
      {},
      [null],
      [{ ...origin, kind: 'user' }],
      ...(['frame_id', 'frame_incarnation_id', 'root_frame_id', 'root_frame_incarnation_id'] as const).map((key) => [
        { ...origin, [key]: undefined },
      ]),
      [{ ...origin, frame_id: ' task-child' }],
      [{ ...origin, frame_incarnation_id: '字'.repeat(171) }],
      [{ ...origin, project_id: '' }],
      [{ ...origin, project_id: null }],
      [{ ...origin, project_available: undefined }],
      [{ ...origin, project_available: 'true' }],
      [{ ...origin, project_id: undefined }],
      [{ ...origin, recorded_at: '2026-11-04T02:03:04+00:00' }],
      [{ ...origin, recorded_at: '2026-02-30T02:03:04Z' }],
      [{ ...origin, recorded_at: '2026-11-04T25:03:04Z' }],
      [{ ...origin, recorded_at: '2026-11-04T02:03:04.1234567890Z' }],
      [{ ...origin, recorded_at: undefined }],
    ].map((task_origins) => ({ task_origins }))
  )('rejects malformed origin readback %#', ({ task_origins }) => {
    expect(() => decodeLiteratureCandidate({ ...candidate, task_origins })).toThrow(
      'Invalid Literature candidate contract'
    );
  });

  it('does not turn readback provenance into stage or decision authorization', () => {
    const operation_id = '00000000-0000-4000-8000-000000000745';
    const stage = {
      kind: 'candidate-stage',
      operation_id,
      provider: candidate.provider,
      external_id: candidate.external_id,
      title: candidate.title,
      metadata: candidate.metadata,
    };
    expect(() => decodeLiteratureCandidateCommand({ ...stage, task_origins: [origin] })).toThrow();
    for (const kind of ['candidate-accept', 'candidate-dismiss', 'candidate-restore'])
      expect(() =>
        decodeLiteratureCandidateCommand({ kind, operation_id, candidate_id: candidate.id, task_origins: [origin] })
      ).toThrow();
    expect(decodeLiteratureCandidateCommand(stage)).not.toHaveProperty('task_origins');
  });
});
