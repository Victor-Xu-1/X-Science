// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { parsePDB } from 'molstar/lib/mol-io/reader/pdb/parser';
import { trajectoryFromPDB } from 'molstar/lib/mol-model-formats/structure/pdb';
import { Structure } from 'molstar/lib/mol-model/structure';
import { summarizeStructureComposition } from '@/renderer/pages/conversation/Preview/components/viewers/structureComposition';
import { createStructureLigandController } from '@/renderer/pages/conversation/Preview/components/viewers/structureLigandController';
import { PluginContext } from 'molstar/lib/mol-plugin/context';
import { DefaultPluginSpec } from 'molstar/lib/mol-plugin/spec';
import { StructureElement } from 'molstar/lib/mol-model/structure';
import { MULTI_LIGAND_PDB as source, MULTI_LIGAND_CIF, MULTI_LIGAND_MODELS } from '../../fixtures/structureLigands';
import { CIF } from 'molstar/lib/mol-io/reader/cif';
import { trajectoryFromMmCIF } from 'molstar/lib/mol-model-formats/structure/mmcif';
import { PresetStructureRepresentations } from 'molstar/lib/mol-plugin-state/builder/structure/representation-preset';
import { StateTransforms } from 'molstar/lib/mol-plugin-state/transforms';
import { waitFor } from '@testing-library/react';

export async function parseLigandSelectionFixture(text = source) {
  const parsed = await parsePDB(text, 'ligand-selection').run();
  if (parsed.isError) throw new Error(parsed.message);
  const trajectory = await trajectoryFromPDB(parsed.result).run();
  return Structure.ofModel(trajectory.representative);
}

describe('one-receptor multi-ligand parsed composition', () => {
  it('parses the same three independently named instances from real mmCIF', async () => {
    const parsed = await CIF.parse(MULTI_LIGAND_CIF).run();
    if (parsed.isError) throw new Error(parsed.message);
    const trajectory = await trajectoryFromMmCIF(parsed.result.blocks[0]).run();
    const composition = summarizeStructureComposition(Structure.ofModel(trajectory.representative), 'mmcif');
    expect(composition.objects.filter((object) => object.kind === 'ligand')).toHaveLength(3);
    expect(composition.objects.find((object) => object.kind === 'protein')?.atomCount).toBe(8);
  });

  it('keeps alternate conformers separate while including their shared atoms in each selection', async () => {
    const text = source
      .split('\n')
      .filter(
        (line) => !line.startsWith('HETATM') || (line.slice(22, 26).trim() === '101' && line.slice(21, 22) === 'A')
      )
      .map((line) =>
        line.startsWith('HETATM') && line.slice(12, 16).trim() === 'O1'
          ? line.slice(0, 16) + 'A' + line.slice(17)
          : line
      )
      .join('\n');
    const alternative = 'HETATM   15  O1 BUNL A 101       3.200   2.000   3.000  0.50 20.00           O';
    const composition = summarizeStructureComposition(
      await parseLigandSelectionFixture(text.replace('END', alternative + '\nEND')),
      'pdb'
    );
    const ligands = composition.objects.filter((object) => object.kind === 'ligand');
    expect(ligands).toHaveLength(2);
    expect(ligands.map((object) => object.atomCount)).toEqual([2, 2]);
    expect(ligands.map((object) => object.label)).toEqual(['UNL · A:101 · A', 'UNL · A:101 · B']);
  });

  it('keeps three overlapping UNL instances independently selectable by chain and residue', async () => {
    const structure = await parseLigandSelectionFixture();
    const composition = summarizeStructureComposition(structure, 'pdb');
    const ligands = composition.objects.filter((object) => object.kind === 'ligand');
    expect(ligands).toHaveLength(3);
    expect(new Set(ligands.map((ligand) => ligand.id)).size).toBe(3);
    expect(ligands.map((ligand) => ligand.atomCount)).toEqual([2, 2, 2]);
    expect(composition.objects.find((object) => object.kind === 'protein')?.atomCount).toBe(8);
    expect(composition.atomCount).toBe(14);
  });

  it('filters actual Mol* component bytes to one selected ligand without changing the fixed receptor', async () => {
    const plugin = new PluginContext(DefaultPluginSpec());
    await plugin.init();
    try {
      const raw = await plugin.builders.data.rawData({ data: source, label: 'three-ligands.pdb' });
      const trajectory = await plugin.builders.structure.parseTrajectory(raw, 'pdb');
      const preset = await plugin.builders.structure.hierarchy.applyPreset(trajectory, 'default');
      expect(preset).toBeDefined();
      const getRoot = () => plugin.managers.structure.hierarchy.current.structures[0]?.cell.obj?.data;
      const composition = summarizeStructureComposition(getRoot(), 'pdb');
      const ligands = composition.objects.filter((object) => object.kind === 'ligand');
      const controller = createStructureLigandController(
        plugin,
        getRoot,
        () => 'pdb',
        () => {}
      );
      const visibleCounts = () =>
        plugin.managers.structure.hierarchy.current.structures[0].components
          .map((component) => summarizeStructureComposition(component.cell.obj?.data, 'pdb'))
          .reduce(
            (count, value) => ({
              protein: count.protein + (value.objects.find((object) => object.kind === 'protein')?.atomCount ?? 0),
              ligand:
                count.ligand +
                value.objects
                  .filter((object) => object.kind === 'ligand')
                  .reduce((sum, object) => sum + object.atomCount, 0),
            }),
            { protein: 0, ligand: 0 }
          );
      const rootBefore = getRoot();
      await controller.apply();
      expect(controller.selectedIds()).toEqual([ligands[0].id]);
      expect(visibleCounts()).toEqual({ protein: 8, ligand: 2 });
      expect(StructureElement.Loci.size(controller.displayedLoci()!)).toBe(10);
      await controller.select([ligands[2].id]);
      expect(StructureElement.Loci.size(controller.selectedLoci()!)).toBe(2);
      expect(visibleCounts()).toEqual({ protein: 8, ligand: 2 });
      await controller.select([]);
      expect(visibleCounts()).toEqual({ protein: 8, ligand: 0 });
      expect(StructureElement.Loci.size(controller.displayedLoci()!)).toBe(8);
      await controller.select([ligands[1].id]);
      expect(visibleCounts()).toEqual({ protein: 8, ligand: 2 });
      await expect(controller.select(['foreign-instance'])).rejects.toThrow('SELECTION_INVALID');
      expect(controller.selectedIds()).toEqual([ligands[1].id]);
      expect(getRoot()).toBe(rootBefore);
      expect(summarizeStructureComposition(getRoot(), 'pdb').atomCount).toBe(14);
    } finally {
      plugin.dispose();
    }
  });

  it('restores selectable components after a native trajectory model switch', async () => {
    const plugin = new PluginContext(DefaultPluginSpec());
    await plugin.init();
    let changed = 0;
    let modelError: unknown;
    try {
      const raw = await plugin.builders.data.rawData({ data: MULTI_LIGAND_MODELS, label: 'models.pdb' });
      const trajectory = await plugin.builders.structure.parseTrajectory(raw, 'pdb');
      await plugin.builders.structure.hierarchy.applyPreset(trajectory, 'default');
      const getRoot = () => plugin.managers.structure.hierarchy.current.structures[0]?.cell.obj?.data;
      const controller = createStructureLigandController(
        plugin,
        getRoot,
        () => 'pdb',
        () => {},
        async () => {
          await plugin.managers.structure.component.applyPreset(
            [...plugin.managers.structure.hierarchy.current.structures],
            PresetStructureRepresentations.auto
          );
          await controller.apply();
          changed++;
        },
        (error) => {
          modelError = error;
        }
      );
      await controller.apply();
      const model = plugin.state.data.selectQ((query) =>
        query.ofTransformer(StateTransforms.Model.ModelFromTrajectory)
      )[0];
      await plugin.state.data.build().to(model).update({ modelIndex: 1 }).commit();
      await waitFor(() => expect(changed).toBe(1));
      expect(modelError).toBeUndefined();
      expect(getRoot()?.model.modelNum).toBe(2);
      expect(controller.selectedIds()).toHaveLength(1);
      expect(StructureElement.Loci.size(controller.displayedLoci()!)).toBe(10);
      const visibleLigandAtoms = plugin.managers.structure.hierarchy.current.structures[0].components.reduce(
        (sum, component) =>
          sum +
          summarizeStructureComposition(component.cell.obj?.data, 'pdb')
            .objects.filter((object) => object.kind === 'ligand')
            .reduce((count, object) => count + object.atomCount, 0),
        0
      );
      expect(visibleLigandAtoms).toBe(2);
      controller.dispose();
    } finally {
      plugin.dispose();
    }
  });
});
