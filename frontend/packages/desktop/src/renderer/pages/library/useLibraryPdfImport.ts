import { useEffect, useRef, useState } from 'react';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { decodeLiteratureReceipt, type LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import type {
  LiteratureMutationController,
  LiteratureMutationReceipt,
  LiteratureMutationView,
} from '@/renderer/services/runtime/LiteratureMutationController';
import { isUploadIntent } from '@/renderer/services/runtime/literatureUploadIntent';
import type { LibraryReadView } from './useLibraryWorkbench';

export type LibraryImportProjects = LibraryReadView<XScienceProject[]>;
type Draft = { file?: File; title: string; projectId: string };
type Failure = 'invalidFile' | 'submitFailed' | 'invalidReceipt' | 'insufficientStorage' | null;
type UiCall = { kind: 'write' | 'discard'; promise?: Promise<LiteratureMutationReceipt> | Promise<void> };
type UiScope = {
  account: string;
  controller: LiteratureMutationController;
  mounted: boolean;
  call?: UiCall;
  displayedOperationId?: string;
  shownOperationId?: string;
};
type LocalView = {
  owner: UiScope;
  draft: Draft;
  reselected?: { operationId: string; file: File };
  failure: Failure;
  success?: LiteratureReceipt;
  focusVersion: number;
  writing: boolean;
  discarding: boolean;
};
const empty = (owner: UiScope): LocalView => ({
  owner,
  draft: { title: '', projectId: '' },
  failure: null,
  focusVersion: 0,
  writing: false,
  discarding: false,
});
const receiptFrom = (value: LiteratureMutationReceipt | undefined): LiteratureReceipt | undefined => {
  if (value?.schema !== 'x-science.literature-receipt.v1') return undefined;
  try {
    const receipt = decodeLiteratureReceipt(value, value.operation_id);
    return receipt.attachment_id && receipt.version_id ? receipt : undefined;
  } catch {
    return undefined;
  }
};

/** View-only draft and exact returned-promise latch. The existing controller owns every write. */
export function useLibraryPdfImport({
  account,
  controller,
  view,
  projects,
  open,
  setOpen,
  blockedByEditor = false,
}: {
  account: string;
  controller: LiteratureMutationController;
  view: LiteratureMutationView;
  projects: LibraryImportProjects;
  open: boolean;
  setOpen: (open: boolean) => void;
  blockedByEditor?: boolean;
}) {
  const scopeRef = useRef<UiScope>({ account, controller, mounted: true });
  if (scopeRef.current.account !== account || scopeRef.current.controller !== controller)
    scopeRef.current = { account, controller, mounted: true };
  const scope = scopeRef.current;
  const [state, setState] = useState(() => empty(scope));
  const local = state.owner === scope ? state : empty(scope);
  const entryRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const pending = view.pending && isUploadIntent(view.pending) ? view.pending : undefined;
  if (pending) scope.displayedOperationId = pending.operation_id;
  const current = () => scopeRef.current === scope && scope.mounted && account === getRendererAccountScopeToken();
  const update = (patch: Partial<LocalView> | ((value: LocalView) => Partial<LocalView>)) => {
    if (current())
      setState((previous) => {
        if (!current()) return previous;
        const value = previous.owner === scope ? previous : empty(scope);
        return { ...value, ...(typeof patch === 'function' ? patch(value) : patch) };
      });
  };
  const finish = (call: UiCall) => {
    if (scope.call !== call) return;
    scope.call = undefined;
    update({ writing: false, discarding: false });
  };
  useEffect(() => {
    scope.mounted = true;
    return () => {
      scope.mounted = false;
      const call = scope.call;
      if (call?.kind === 'write' && call.promise)
        scope.controller.cancelPdfImport(call.promise as Promise<LiteratureMutationReceipt>);
    };
  }, [scope]);
  useEffect(() => {
    if (pending && scope.shownOperationId !== pending.operation_id && !blockedByEditor && current()) {
      scope.shownOperationId = pending.operation_id;
      setOpen(true);
    }
  }, [pending?.operation_id, scope, blockedByEditor, setOpen]);

  const globalReceipt =
    view.receipt?.operation_id === scope.displayedOperationId ? receiptFrom(view.receipt) : undefined;
  const success = !pending ? (local.success ?? globalReceipt) : undefined;
  const pdfActivity = view.mutating && (Boolean(pending) || Boolean(view.uploadState));
  const busy = pdfActivity || local.writing;
  const otherPending = Boolean(view.pending && !pending);
  const selectedProject =
    projects.status === 'ready' && projects.value?.some((project) => project.projectId === local.draft.projectId);
  const canImport =
    current() &&
    !scope.call &&
    !view.pending &&
    !view.mutating &&
    view.error !== 'storage' &&
    !blockedByEditor &&
    Boolean(selectedProject && local.draft.file && local.draft.title.trim() && local.draft.title.trim().length <= 4096);
  const selectedOriginal =
    pending && local.reselected?.operationId === pending.operation_id ? local.reselected.file : undefined;

  const launch = (start: () => Promise<LiteratureMutationReceipt>) => {
    if (!current() || scope.call || (blockedByEditor && !pending)) return;
    const call: UiCall = { kind: 'write' };
    scope.call = call; // Synchronous admission latch, before invoking a method or awaiting anything.
    update({ writing: true, failure: null, success: undefined });
    try {
      const promise = start();
      call.promise = promise;
      if (!current()) scope.controller.cancelPdfImport(promise);
      const observed = promise.then(
        (value) => {
          if (!current() || scope.call !== call) return;
          const receipt = receiptFrom(value);
          if (!receipt) update({ failure: 'invalidReceipt' });
          else {
            scope.displayedOperationId = receipt.operation_id;
            update({ success: receipt, failure: null });
          }
        },
        (error: unknown) => {
          if (!current() || scope.call !== call) return;
          if (error instanceof DOMException && error.name === 'AbortError') return;
          update({
            failure:
              isBackendHttpError(error) && error.status === 507 && error.code === 'ATTACHMENT_INIT_WRITE_REJECTED'
                ? 'insufficientStorage'
                : 'submitFailed',
            ...(controller.getSnapshot().uploadState === 'file-mismatch' ? { reselected: undefined } : {}),
          });
        }
      );
      void observed.then(
        () => finish(call),
        () => finish(call)
      );
    } catch {
      update({ failure: 'submitFailed' });
      finish(call);
    }
  };
  const openPanel = () => {
    if (!current() || blockedByEditor) return;
    setOpen(true);
    update((value) => ({ focusVersion: value.focusVersion + 1 }));
  };
  const chooseFile = (file: File) => {
    if (!current() || busy || scope.call || otherPending || blockedByEditor) return;
    const valid = file.size > 0 && Boolean(file.name.trim()); // accept is a chooser hint; the server validates PDF content.
    setOpen(true);
    if (pending)
      update((value) => ({
        reselected: valid ? { operationId: pending.operation_id, file } : undefined,
        failure: valid ? null : 'invalidFile',
        focusVersion: value.focusVersion + 1,
      }));
    else {
      scope.displayedOperationId = undefined;
      update((value) => ({
        draft: {
          ...value.draft,
          file: valid ? file : undefined,
          title: valid ? file.name.replace(/\.pdf$/i, '').trim() || file.name : '',
        },
        success: undefined,
        failure: valid ? null : 'invalidFile',
        focusVersion: value.focusVersion + 1,
      }));
    }
  };
  const close = () => {
    if (!current() || busy) return;
    setOpen(false);
    entryRef.current?.focus();
  };
  const completeSuccess = (receipt: LiteratureReceipt) => {
    if (!current() || !open || scope.call || busy || pending || otherPending || success !== receipt) return false;
    const latest = controller.getSnapshot();
    const committed = receiptFrom(latest.receipt);
    if (
      latest.pending ||
      latest.mutating ||
      latest.error === 'storage' ||
      scope.displayedOperationId !== receipt.operation_id ||
      committed?.operation_id !== receipt.operation_id ||
      committed.item.id !== receipt.item.id ||
      committed.attachment_id !== receipt.attachment_id ||
      committed.version_id !== receipt.version_id
    )
      return false;
    // End only this completed UI interaction. Keep the owner's committed receipt
    // and every pending/recovery record intact; navigation is the Page's action.
    close();
    return true;
  };
  const discard = () => {
    if (!current() || scope.call || busy || !pending || !view.canDiscardRejectedUpload) return;
    const call: UiCall = { kind: 'discard' };
    scope.call = call;
    update({ discarding: true, failure: null });
    const promise = controller.discardRejectedPdfImport();
    call.promise = promise;
    const observed = promise.then(
      () => {
        if (!current() || scope.call !== call) return;
        scope.displayedOperationId = undefined;
        update({ draft: { title: '', projectId: '' }, reselected: undefined, success: undefined });
      },
      () => {
        if (current() && scope.call === call) update({ failure: 'submitFailed' });
      }
    );
    void observed.then(
      () => finish(call),
      () => finish(call)
    );
  };
  return {
    account,
    controller,
    view,
    open: open && current(),
    pending,
    otherPending,
    projects,
    draft: local.draft,
    success,
    failure: local.failure,
    focusVersion: local.focusVersion,
    busy,
    discarding: local.discarding,
    canImport,
    selectedOriginal,
    blockedByEditor,
    entryRef,
    inputRef,
    openPanel,
    chooseFile,
    close,
    completeSuccess,
    discard,
    noticeOwned: Boolean(pending || (open && (local.writing || local.failure || success))),
    setTitle: (title: string) => {
      if (!busy && !pending && current()) update((value) => ({ draft: { ...value.draft, title } }));
    },
    setProject: (projectId: string) => {
      if (!busy && !pending && current()) update((value) => ({ draft: { ...value.draft, projectId } }));
    },
    submit: () => {
      if (canImport && local.draft.file)
        launch(() =>
          controller.importPdf({ projectId: local.draft.projectId, title: local.draft.title, file: local.draft.file! })
        );
    },
    retry: () => {
      if (pending && !busy) launch(() => controller.retry(selectedOriginal));
    },
    cancel: () => {
      if (!current() || !busy) return;
      const call = scope.call;
      if (call?.kind === 'write' && call.promise)
        controller.cancelPdfImport(call.promise as Promise<LiteratureMutationReceipt>);
      else controller.cancelPdfImport(); // Only an explicit user click can cancel a subscriber's current PDF flight.
    },
  };
}
export type LibraryPdfImportModel = ReturnType<typeof useLibraryPdfImport>;
