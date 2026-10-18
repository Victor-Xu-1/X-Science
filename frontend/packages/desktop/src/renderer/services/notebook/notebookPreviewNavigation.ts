import type { PreviewContextValue } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { getRendererAccountScopeToken } from '../rendererAccountScope';
import type { XScienceKernel } from '../xScienceNotebook';
import type { NotebookSessionNavigation } from './manualNotebookTypes';
import { validIdentity } from './manualNotebookContract';

// This marks an explicit renderer navigation, not execution authority. Fresh server scope/targets
// and an explicit Run are still required. JSON/artifact metadata cannot create this marker.
const openings = new WeakMap<object, string>();
let nextOpening = 0;
export function isNotebookSessionNavigation(value: unknown): value is NotebookSessionNavigation {
  return (
    Boolean(value) && typeof value === 'object' && openings.get(value as object) === getRendererAccountScopeToken()
  );
}
export function openNotebookPreview(
  preview: Pick<PreviewContextValue, 'openPreview'>,
  kernel: Pick<XScienceKernel, 'frameId' | 'rootFrameId' | 'kernelId'>,
  title: string
) {
  if (![kernel.frameId, kernel.rootFrameId, kernel.kernelId].every(validIdentity)) return;
  const navigation: NotebookSessionNavigation = Object.freeze({
    frameId: kernel.frameId,
    rootFrameId: kernel.rootFrameId,
    preferredKernelId: kernel.kernelId,
    focusRequest: String(++nextOpening),
  });
  openings.set(navigation, getRendererAccountScopeToken());
  preview.openPreview(
    '',
    'notebook',
    { title, editable: false, notebookSession: navigation },
    { presentation: 'single' }
  );
}
export function sameNotebookPreview(a?: NotebookSessionNavigation, b?: NotebookSessionNavigation): boolean | null {
  if (!a && !b) return null;
  return (
    isNotebookSessionNavigation(a) &&
    isNotebookSessionNavigation(b) &&
    a.frameId === b.frameId &&
    a.rootFrameId === b.rootFrameId
  );
}
