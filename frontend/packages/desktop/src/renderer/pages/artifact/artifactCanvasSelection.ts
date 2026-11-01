import type { CreateXScienceArtifactAnnotationInput } from '@/renderer/services/xScienceAnnotations';
import type { XScienceArtifactTextSelection } from './artifactTextSelection';

export interface XScienceArtifactPointSelection {
  type: 'point';
  text: string;
  x: number;
  y: number;
  xPercent: number;
  yPercent: number;
  pageNumber: number | null;
}

export interface XScienceArtifactHtmlElementSelection {
  type: 'html_element';
  text: string;
  x: number;
  y: number;
  xPercent: number;
  yPercent: number;
  elementSelector: string;
  elementDescriptor: string;
}

export type XScienceArtifactCanvasSelection =
  | XScienceArtifactTextSelection
  | XScienceArtifactPointSelection
  | XScienceArtifactHtmlElementSelection;

/** Source anchors own a draft; screen coordinates only place its toolbar. */
export function artifactCanvasSelectionIdentity(selection: XScienceArtifactCanvasSelection): string {
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

/** One canvas producer supplies the same normalized anchor to both PDF surfaces. */
export function annotationInputFromCanvasSelection(
  selection: XScienceArtifactCanvasSelection,
  text: string
): CreateXScienceArtifactAnnotationInput {
  return {
    type: selection.type,
    text,
    selectionText: selection.type === 'point' ? null : selection.text,
    selectionPrefix: selection.type === 'text_selection' ? selection.selectionPrefix : null,
    startLine: selection.type === 'text_selection' ? selection.startLine : null,
    startColumn: selection.type === 'text_selection' ? selection.startColumn : null,
    endLine: selection.type === 'text_selection' ? selection.endLine : null,
    endColumn: selection.type === 'text_selection' ? selection.endColumn : null,
    xPercent: selection.type === 'text_selection' ? null : selection.xPercent,
    yPercent: selection.type === 'text_selection' ? null : selection.yPercent,
    pageNumber: selection.type === 'html_element' ? null : selection.pageNumber,
    elementSelector: selection.type === 'html_element' ? selection.elementSelector : null,
    elementDescriptor: selection.type === 'html_element' ? selection.elementDescriptor : null,
  };
}
