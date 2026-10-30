import { useCallback, useEffect, useRef, useState } from 'react';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureCommand, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { projectLibraryScalars } from '@/renderer/components/chat/SendBox/reading/libraryBibliographicDraft';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { LiteratureMutationView } from '@/renderer/services/runtime/LiteratureMutationController';
import { isLibraryReadFresh, type LibraryReadPhase } from './useLibraryWorkbench';

export type LibraryManualCreateProps = {
  account: string;
  projectId: string;
  collection?: { id: string; revision: number };
  collectionRead?: LibraryReadPhase;
  refresh: () => void;
  view: LiteratureMutationView;
  submit: (command: LiteratureCommand) => Promise<LiteratureReceipt>;
  retry: () => Promise<unknown>;
  close: () => void;
  created: (receipt: LiteratureReceipt) => void;
  openTrash: () => void;
};

/** Local draft/feedback only. The Page's account controller owns every write and retry. */
export function useLibraryManualCreate({
  account,
  projectId,
  collection,
  collectionRead,
  refresh,
  view,
  submit,
  retry,
  close,
  created,
}: LibraryManualCreateProps) {
  const [basic, setBasic] = useState({ title: '', authors: '', year: '', doi: '' });
  const [scalars, setScalars] = useState(() => projectLibraryScalars({}));
  const [failure, setFailure] = useState<'trash' | 'rejected' | undefined>();
  const [discard, setDiscard] = useState(false);
  const destination = useRef({ projectId, collectionId: collection?.id }).current;
  const alive = useRef(true);
  const operation = useRef<string | undefined>(undefined);
  const latch = useRef(false);
  const settled = useRef<string | undefined>(undefined);
  const current = () => alive.current && account === getRendererAccountScopeToken();
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const pending = Boolean(view.pending && view.pending.operation_id === operation.current);
  const destinationUnavailable = Boolean(
    destination.collectionId &&
    (!collectionRead || !isLibraryReadFresh(collectionRead) || collection?.id !== destination.collectionId)
  );
  const locked = Boolean(view.mutating || view.pending || view.error === 'storage' || destinationUnavailable);
  const dirty = Object.values(basic).some(Boolean) || Object.values(scalars).some(Boolean);
  const requestClose = () => {
    if (!current() || view.mutating) return;
    // Closing an uncertain view never discards the original durable operation.
    if (pending) {
      close();
      return;
    }
    if (dirty) setDiscard(true);
    else close();
  };
  const accept = useCallback(
    (receipt: LiteratureReceipt) => {
      if (
        !alive.current ||
        account !== getRendererAccountScopeToken() ||
        receipt.operation_id !== operation.current ||
        settled.current === receipt.operation_id
      )
        return;
      settled.current = receipt.operation_id;
      created(receipt);
    },
    [account, created]
  );
  useEffect(() => {
    if (view.receipt?.schema === 'x-science.literature-receipt.v1') accept(view.receipt);
  }, [view.receipt, accept]);
  const run = async (again = false) => {
    if (!current() || latch.current || view.mutating || (!again && locked)) return;
    if (!again && (!basic.title.trim() || (basic.year && !/^\d{4}$/.test(basic.year)))) return;
    latch.current = true;
    setFailure(undefined);
    try {
      if (again) {
        const receipt = await retry();
        if (
          receipt &&
          typeof receipt === 'object' &&
          'schema' in receipt &&
          receipt.schema === 'x-science.literature-receipt.v1'
        )
          accept(receipt as LiteratureReceipt);
      } else {
        operation.current = crypto.randomUUID();
        const metadata: Record<string, unknown> = Object.fromEntries(
          Object.entries(scalars)
            .filter(([, value]) => value.trim())
            .map(([key, value]) => [key, value.trim()])
        );
        if (basic.authors.trim())
          metadata.authors = basic.authors
            .split(';')
            .map((name) => name.trim())
            .filter(Boolean);
        if (basic.year) metadata.year = basic.year;
        if (basic.doi.trim()) metadata.doi = basic.doi.trim();
        accept(
          await submit({
            kind: 'manual-create',
            operation_id: operation.current,
            title: basic.title.trim(),
            metadata,
            ...(destination.projectId ? { project_id: destination.projectId } : {}),
            ...(destination.collectionId && collection
              ? {
                  collection_id: destination.collectionId,
                  expected_collection_revision: collection.revision,
                }
              : {}),
          })
        );
      }
    } catch (error) {
      if (current()) {
        setFailure(isBackendHttpError(error) && error.code === 'LITERATURE_TRASH_CONFLICT' ? 'trash' : 'rejected');
        // Definite rejection has no pending write. The next explicit Create may
        // use a refreshed revision; an uncertain retry still delegates unchanged.
        if (isBackendHttpError(error) && error.code === 'LITERATURE_CONFLICT' && destination.collectionId) refresh();
      }
    } finally {
      latch.current = false;
    }
  };
  const scalarProps = {
    metadata: {},
    draft: scalars,
    disabled: locked,
    change: (key: keyof typeof scalars, value: string) => setScalars((previous) => ({ ...previous, [key]: value })),
  };
  return { basic, setBasic, scalarProps, failure, discard, setDiscard, locked, pending, requestClose, run };
}
