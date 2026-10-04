import { StructureElement, type Structure } from 'molstar/lib/mol-model/structure';
import { StructureQuery } from 'molstar/lib/mol-model/structure/query/query';
import { StructureSelectionQueries } from 'molstar/lib/mol-plugin-state/helpers/structure-selection-query';
import type { PluginContext } from 'molstar/lib/mol-plugin/context';
import { queryStructureLigands, selectStructureLigandLoci, splitStructureLigands } from './structureLigandSelection';

/** Filters existing Mol* components; no translated coordinates or duplicate representation graph. */
export function createStructureLigandController(
  plugin: PluginContext,
  getStructure: () => Structure | undefined,
  getFormat: () => string,
  onSelection: (ids: readonly string[], atoms: number) => void,
  onNativeModelChange?: () => Promise<void>,
  onModelError?: (error: unknown) => void
) {
  let root: Structure | undefined;
  let selectedIds: string[] | null = null;
  const components = new Map<string, StructureElement.Loci>();
  let disposed = false;
  let modelChangePending = false;

  const instances = () => {
    const structure = getStructure();
    if (!structure) return [];
    if (structure !== root) {
      root = structure;
      selectedIds = null;
      components.clear();
    }
    const protein = StructureQuery.loci(StructureSelectionQueries.protein.query, structure);
    const entries = splitStructureLigands(
      queryStructureLigands(structure, getFormat()),
      !StructureElement.Loci.isEmpty(protein)
    );
    selectedIds ??= entries.length ? [entries[0].id] : [];
    return entries;
  };

  const selectedLoci = () => {
    const entries = instances();
    return root ? selectStructureLigandLoci(root, entries, selectedIds ?? []) : undefined;
  };

  const apply = async () => {
    if (disposed) throw new Error('MOLSTAR_STRUCTURE_DISPOSED');
    const selected = selectedLoci();
    if (!root || !selected) return;
    const allLigands = queryStructureLigands(root, getFormat());
    // Reused components whose preset restored an expression/static query
    // receive a fresh baseline. Our own bundle updates never replace it.
    for (const structure of plugin.managers.structure.hierarchy.current.structures) {
      for (const component of structure.components) {
        const data = component.cell.obj?.data;
        if (
          !data ||
          (components.has(component.cell.transform.ref) && component.cell.params?.values.type.name === 'bundle')
        )
          continue;
        const baseline = StructureElement.Loci.remap(StructureElement.Loci.all(data), root);
        if (!StructureElement.Loci.isEmpty(StructureElement.Loci.intersect(baseline, allLigands))) {
          components.set(component.cell.transform.ref, baseline);
        }
      }
    }
    const update = plugin.state.data.build();
    let changed = false;
    for (const [ref, baseline] of components) {
      const cell = plugin.state.data.cells.get(ref);
      if (!cell) {
        components.delete(ref);
        continue;
      }
      const context = StructureElement.Loci.subtract(baseline, allLigands);
      const visible = StructureElement.Loci.union(context, StructureElement.Loci.intersect(baseline, selected));
      const bundle = StructureElement.Bundle.fromLoci(visible);
      update.to(ref).update((old) => {
        old.type = { name: 'bundle', params: bundle };
      });
      changed = true;
    }
    if (changed) await update.commit({ revertOnError: true });
    onSelection(selectedIds ?? [], StructureElement.Loci.size(selected));
    plugin.canvas3d?.syncVisibility();
    plugin.canvas3d?.requestDraw();
  };

  const modelChanges = plugin.state.data.events.changed.subscribe(() => {
    const current = getStructure();
    if (disposed || modelChangePending || !root || !current || current.model === root.model || !onNativeModelChange)
      return;
    modelChangePending = true;
    void Promise.resolve()
      .then(async () => {
        if (!disposed && root && getStructure() !== root) await onNativeModelChange();
      })
      .catch((error) => {
        if (!disposed) onModelError?.(error);
      })
      .finally(() => {
        modelChangePending = false;
      });
  });

  return {
    apply,
    selectedLoci,
    displayedLoci: (proteinVisible = true, ligandVisible = true) => {
      const selected = selectedLoci();
      if (!root || !selected) return undefined;
      let context = StructureElement.Loci.subtract(
        StructureElement.Loci.all(root),
        queryStructureLigands(root, getFormat())
      );
      if (!proteinVisible)
        context = StructureElement.Loci.subtract(
          context,
          StructureQuery.loci(StructureSelectionQueries.protein.query, root)
        );
      return ligandVisible ? StructureElement.Loci.union(context, selected) : context;
    },
    depictionLoci: () => {
      const entries = instances();
      const id = selectedIds?.at(-1);
      return root ? selectStructureLigandLoci(root, entries, id ? [id] : []) : undefined;
    },
    selectedIds: () => {
      instances();
      return [...(selectedIds ?? [])];
    },
    reset: () => {
      root = undefined;
      selectedIds = null;
      components.clear();
    },
    dispose: () => {
      disposed = true;
      modelChanges.unsubscribe();
      components.clear();
    },
    select: async (ids: readonly string[]) => {
      if (disposed) throw new Error('MOLSTAR_STRUCTURE_DISPOSED');
      const valid = new Set(instances().map((instance) => instance.id));
      if (new Set(ids).size !== ids.length || ids.some((id) => !valid.has(id))) {
        throw new Error('MOLSTAR_STRUCTURE_LIGAND_SELECTION_INVALID');
      }
      const previous = selectedIds;
      selectedIds = [...ids];
      try {
        await apply();
      } catch (error) {
        selectedIds = previous;
        throw error;
      }
    },
  };
}
