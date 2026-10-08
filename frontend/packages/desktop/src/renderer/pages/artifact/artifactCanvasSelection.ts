import type { SynonBiomedArtifactTextSelection } from './artifactTextSelection';

export interface SynonBiomedArtifactPointSelection {
  type: 'point';
  text: string;
  x: number;
  y: number;
  xPercent: number;
  yPercent: number;
  pageNumber: number | null;
}

export interface SynonBiomedArtifactHtmlElementSelection {
  type: 'html_element';
  text: string;
  x: number;
  y: number;
  xPercent: number;
  yPercent: number;
  elementSelector: string;
  elementDescriptor: string;
}

export type SynonBiomedArtifactCanvasSelection =
  | SynonBiomedArtifactTextSelection
  | SynonBiomedArtifactPointSelection
  | SynonBiomedArtifactHtmlElementSelection;

/** Source anchors own a draft; screen coordinates only place its toolbar. */
export function artifactCanvasSelectionIdentity(selection: SynonBiomedArtifactCanvasSelection): string {
  if (selection.type === 'text_selection') {
    return JSON.stringify([
      selection.type,
      selection.text,
      selection.selectionPrefix,
      selection.startLine,
      selection.startColumn,
      selection.endLine,
      selection.endColumn,
      selection.pageNumber,
    ]);
  }
  if (selection.type === 'point')
    return JSON.stringify([selection.type, selection.xPercent, selection.yPercent, selection.pageNumber]);
  return JSON.stringify([selection.type, selection.text, selection.elementSelector]);
}

export function clampPercent(value: number): number {
  return Math.min(100, Math.max(0, Number(value.toFixed(4))));
}
