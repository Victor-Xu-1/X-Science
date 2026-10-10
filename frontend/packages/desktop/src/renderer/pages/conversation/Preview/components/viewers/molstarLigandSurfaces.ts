import { StructureElement, type Structure } from 'molstar/lib/mol-model/structure';
import type { PluginContext } from 'molstar/lib/mol-plugin/context';
import type { StructureRef } from 'molstar/lib/mol-plugin-state/manager/structure/hierarchy-state';
import { StructureSelectionQuery } from 'molstar/lib/mol-plugin-state/helpers/structure-selection-query';
import type { MolstarElectrostaticVolumes } from './molstarStructureEngine';
import type { MolstarElectrostaticVolume } from './molstarElectrostaticTheme';
import { resolveStructureViewLigandInstances } from './structureLigandSelection';

export interface MolstarLigandSurfacePlan {
  key: string;
  label: string;
  loci: StructureElement.Loci;
  volume: MolstarElectrostaticVolume;
}

/** Every displayed instance owns its field; a singleton alias can never color a comparison union. */
export function resolveNativeLigandSurfacePlans(
  structure: Structure,
  selected: StructureElement.Loci,
  format: string,
  volumes: MolstarElectrostaticVolumes
): MolstarLigandSurfacePlan[] {
  const instances = resolveStructureViewLigandInstances(structure, selected, format);
  return instances.map((instance) => {
    const volume =
      volumes.ligands?.[instance.key] ??
      (instances.length === 1 && !Object.keys(volumes.ligands ?? {}).length ? volumes.ligand : undefined);
    if (!volume) throw new Error(`MOLSTAR_ELECTROSTATIC_LIGAND_VOLUME_MISSING:${instance.key}`);
    return { key: instance.key, label: instance.label, loci: instance.loci, volume };
  });
}

/** Shared component construction for ordinary presets and staged pocket/docking graphs. */
export async function createLigandSurfaceComponents(
  plugin: PluginContext,
  structureCell: StructureRef['cell'],
  plans: readonly MolstarLigandSurfacePlan[],
  prefix: string
) {
  type Component = NonNullable<
    Awaited<ReturnType<PluginContext['builders']['structure']['tryCreateComponentFromSelection']>>
  >;
  // Mol* component commits share one state tree; serialize writes, not Promise.all.
  return plans.reduce<Promise<Component[]>>(async (pending, plan, index) => {
    const components = await pending;
    if (StructureElement.Loci.isEmpty(plan.loci)) throw new Error(`MOLSTAR_LIGAND_MISSING:${plan.key}`);
    const expression = StructureElement.Bundle.toExpression(StructureElement.Bundle.fromLoci(plan.loci));
    const label = `Ligand surface ${plan.label}`;
    const component = await plugin.builders.structure.tryCreateComponentFromSelection(
      structureCell,
      StructureSelectionQuery(label, expression),
      `${prefix}-${index}`,
      { label }
    );
    if (!component?.obj?.data) throw new Error(`MOLSTAR_LIGAND_MISSING:${plan.key}`);
    components.push(component);
    return components;
  }, Promise.resolve([]));
}
