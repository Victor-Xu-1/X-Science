import { parseSdf } from 'molstar/lib/mol-io/reader/sdf/parser';
import { formatChemicalPoseInput, type ChemicalPoseFormat } from './structureChemicalPoseInput';

export const normalizeChemicalElement = (element: string): string => {
  const symbol = element.trim().toUpperCase();
  if (!symbol) throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_SOURCE_MAPPING_INVALID');
  return symbol === 'D' || symbol === 'T' ? 'H' : symbol;
};

type ChemicalInput = { content: string; format: ChemicalPoseFormat; atomElements: readonly string[] };
type ChemicalReply = { content: string; format: string; filename: string };

/** The provider owns new coordinates, not replacement authority over source-owned chemical declarations. */
export async function restoreChemicalPoseResult(input: ChemicalInput, reply: ChemicalReply): Promise<ChemicalReply> {
  if (reply.format !== 'sdf') throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_RESULT_INVALID');
  const parsed = await parseSdf(reply.content).run();
  if (parsed.isError === true || parsed.result.compounds.length !== 1)
    throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_RESULT_INVALID');
  const atoms = parsed.result.compounds[0].molFile.atoms;
  if (atoms.count !== input.atomElements.length) throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_RESULT_INVALID');
  const coordinates = Array.from({ length: atoms.count }, (_, sourceIndex) => {
    if (normalizeChemicalElement(atoms.type_symbol.value(sourceIndex)) !== input.atomElements[sourceIndex])
      throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_RESULT_INVALID');
    return { sourceIndex, x: atoms.x.value(sourceIndex), y: atoms.y.value(sourceIndex), z: atoms.z.value(sourceIndex) };
  });
  return {
    content: formatChemicalPoseInput({ content: input.content, format: input.format, modelIndex: 0 }, coordinates),
    filename: reply.filename.replace(/\.[^.]+$/, `.${input.format}`),
    format: input.format,
  };
}
