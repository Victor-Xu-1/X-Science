/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { StructureElement } from 'molstar/lib/mol-model/structure';
import { Vec3 } from 'molstar/lib/mol-math/linear-algebra';

/** Native residue labels remain complete; compact callouts leave atoms readable. */
export const POCKET_RESIDUE_LABEL_TYPE_PARAMS = {
  level: 'residue',
  background: true,
  backgroundOpacity: 0.92,
  backgroundMargin: 0.1,
  borderWidth: 0,
  tether: true,
  tetherLength: 0.45,
  offsetZ: 1.5,
  attachment: 'top-center',
  sizeFactor: 0.5,
} as const;

/** Expand around the original ligand centre without replacing its principal axes. */
export function resolvePocketReadingFocusRadius(
  ligand: StructureElement.Loci,
  contacts: StructureElement.Loci,
  minimum: number
): number {
  const mappedContacts = StructureElement.Loci.remap(contacts, ligand.structure);
  if (StructureElement.Loci.isEmpty(ligand) || StructureElement.Loci.isEmpty(mappedContacts)) return minimum;
  const ligandBounds = StructureElement.Loci.getBoundingSphere(ligand);
  const contactBounds = StructureElement.Loci.getBoundingSphere(mappedContacts);
  return Math.max(minimum, Vec3.distance(ligandBounds.center, contactBounds.center) + contactBounds.radius);
}
