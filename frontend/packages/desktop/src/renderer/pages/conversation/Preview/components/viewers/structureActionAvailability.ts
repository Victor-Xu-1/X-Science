import type { MolstarViewRepresentation } from './molstarStructureEngine';

export type StructureAction = Exclude<MolstarViewRepresentation, 'electrostatic'> | 'pocket' | 'diagram' | 'minimize';
export type StructureActionReason =
  | 'loading'
  | 'busy'
  | 'unavailable'
  | 'receptor'
  | 'ligand'
  | 'chemistry'
  | 'task'
  | 'diagram'
  | 'selection';

export interface StructureActionContext {
  canInteract: boolean;
  loading: boolean;
  busy: boolean;
  hasProtein: boolean;
  hasLigand: boolean;
  hasLigandChemistry: boolean;
  hasTaskContext: boolean;
  hasDiagramInput: boolean;
  hasSelectedLigand: boolean;
}

/** UI prerequisites mirror the actual scene and existing frame-scoped services. */
export function structureActionUnavailableReason(
  action: StructureAction,
  context: StructureActionContext
): StructureActionReason | undefined {
  if (!context.canInteract) return context.loading ? 'loading' : context.busy ? 'busy' : 'unavailable';
  if (['pocket', 'surface', 'pocket-surface', 'diagram'].includes(action) && !context.hasProtein) return 'receptor';
  if (['pocket', 'pocket-surface', 'ligand-surface', 'diagram'].includes(action) && !context.hasLigand) return 'ligand';
  if (action === 'ligand-surface' && !context.hasLigandChemistry) return 'chemistry';
  if (
    ['surface', 'pocket-surface', 'ligand-surface', 'diagram', 'minimize'].includes(action) &&
    !context.hasTaskContext
  )
    return 'task';
  if (action === 'diagram' && !context.hasDiagramInput) return context.hasLigandChemistry ? 'diagram' : 'chemistry';
  if (action === 'minimize' && !context.hasSelectedLigand) return 'selection';
  return undefined;
}
