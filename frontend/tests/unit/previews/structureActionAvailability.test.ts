import { describe, expect, it } from 'vitest';
import {
  structureActionUnavailableReason as reason,
  type StructureAction,
  type StructureActionContext,
} from '@/renderer/pages/conversation/Preview/components/viewers/structureActionAvailability';

const ready: StructureActionContext = Object.freeze({
  canInteract: true,
  loading: false,
  busy: false,
  hasProtein: true,
  hasLigand: true,
  hasLigandChemistry: true,
  hasTaskContext: true,
  hasDiagramInput: true,
  hasSelectedLigand: true,
});
const actions: StructureAction[] = [
  'initial',
  'ball-and-stick',
  'line',
  'pocket',
  'surface',
  'pocket-surface',
  'ligand-surface',
  'diagram',
  'minimize',
];

describe('structure action prerequisites', () => {
  it.each(actions)('permits %s with complete inputs', (action) => expect(reason(action, ready)).toBeUndefined());
  it.each(['surface', 'pocket-surface', 'ligand-surface', 'diagram', 'minimize'] as const)(
    'requires actual task context for %s',
    (action) => expect(reason(action, { ...ready, hasTaskContext: false })).toBe('task')
  );
  it.each(['initial', 'ball-and-stick', 'line', 'pocket'] as const)(
    'keeps local %s usable without task context',
    (action) => expect(reason(action, { ...ready, hasTaskContext: false })).toBeUndefined()
  );
  it.each(['pocket', 'surface', 'pocket-surface', 'diagram'] as const)(
    'explains the absent receptor for %s',
    (action) => expect(reason(action, { ...ready, hasProtein: false })).toBe('receptor')
  );
  it.each(['pocket', 'pocket-surface', 'ligand-surface', 'diagram'] as const)(
    'explains no displayed ligand for %s',
    (action) => expect(reason(action, { ...ready, hasLigand: false })).toBe('ligand')
  );
  it('distinguishes verified chemistry, pose mapping and explicit 3D selection', () => {
    expect(reason('ligand-surface', { ...ready, hasLigandChemistry: false })).toBe('chemistry');
    expect(reason('diagram', { ...ready, hasDiagramInput: false })).toBe('diagram');
    expect(reason('diagram', { ...ready, hasDiagramInput: false, hasLigandChemistry: false })).toBe('chemistry');
    expect(reason('diagram', { ...ready, hasLigandChemistry: false })).toBeUndefined();
    expect(reason('minimize', { ...ready, hasSelectedLigand: false })).toBe('selection');
    expect(reason('minimize', { ...ready, hasLigand: false })).toBeUndefined();
  });
  it('reports actual readiness before data prerequisites without mutating inputs', () => {
    expect(reason('pocket', { ...ready, canInteract: false, loading: true, hasProtein: false })).toBe('loading');
    expect(reason('pocket', { ...ready, canInteract: false, busy: true })).toBe('busy');
    expect(reason('line', { ...ready, canInteract: false })).toBe('unavailable');
    expect(ready.hasTaskContext).toBe(true);
  });
});
