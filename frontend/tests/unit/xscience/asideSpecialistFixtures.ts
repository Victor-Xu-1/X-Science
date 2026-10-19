import type { SpecialistInputReference } from '@/common/adapter/specialistBinding';

export const asideSourceSpecialist: SpecialistInputReference = {
  scope: {
    frame_id: 'parent-1',
    frame_incarnation_id: 'parent-inc',
    root_frame_id: 'parent-1',
    root_incarnation_id: 'parent-inc',
  },
  branch: { id: 'br_12345678', generation: 3 },
  revision: 4,
  snapshot_id: 'snapshot:frozen',
};
export const asideChildSpecialist = (frameId = 'child-1'): SpecialistInputReference => ({
  scope: {
    frame_id: frameId,
    frame_incarnation_id: 'child-inc',
    root_frame_id: frameId,
    root_incarnation_id: 'child-inc',
  },
  branch: { id: 'br_87654321', generation: 1 },
  revision: 1,
  snapshot_id: asideSourceSpecialist.snapshot_id,
});
