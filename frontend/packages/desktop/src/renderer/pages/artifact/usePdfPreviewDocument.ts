import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { PRODUCT_NAME } from '@/common/config/productIdentity';
import {
  logScientificPreviewError,
  resolveScientificPreviewError,
  type ScientificPreviewError,
} from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewError';
import { loadPdfDocumentBytes } from './pdfDocumentSource';

type Owner = Readonly<{ source: string; retry: number }>;
type Read = {
  owner: Owner;
  file: Uint8Array | null;
  numPages: number;
  loading: boolean;
  error: ScientificPreviewError | null;
};

/** One byte request and parsed-page outcome owner; react-pdf still owns its task/pages. */
export function usePdfPreviewDocument(source: string) {
  const [retry, setRetry] = useState(0);
  const owner = useMemo<Owner>(() => ({ source, retry }), [source, retry]);
  const committed = useRef<Owner | null>(null);
  const [read, setRead] = useState<Read | null>(null);
  useLayoutEffect(() => {
    committed.current = owner;
    return () => {
      if (committed.current === owner) committed.current = null;
    };
  }, [owner]);
  const isCurrent = useCallback(() => committed.current === owner, [owner]);
  const failed = useCallback(
    (reason: unknown, fallback: 'request-failed' | 'parse-failed' = 'parse-failed') => {
      if (!isCurrent()) return;
      logScientificPreviewError(`[${PRODUCT_NAME} PdfArtifactViewer] Failed to load PDF`, reason, fallback);
      const error = resolveScientificPreviewError(reason, fallback);
      setRead((current) => (current?.owner === owner ? { ...current, error, loading: false } : current));
    },
    [isCurrent, owner]
  );

  useEffect(() => {
    const controller = new AbortController();
    setRead({ owner, file: null, numPages: 0, loading: true, error: null });
    void loadPdfDocumentBytes(source, controller.signal)
      .then((file) => {
        if (controller.signal.aborted || !isCurrent()) return;
        setRead((current) => (current?.owner === owner ? { ...current, file } : current));
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) failed(reason, 'request-failed');
      });
    return () => controller.abort();
  }, [source, owner, isCurrent, failed]);

  const parsed = useCallback(
    ({ numPages }: { numPages: number }) => {
      if (!isCurrent()) return;
      if (!Number.isSafeInteger(numPages) || numPages < 1) {
        failed(new Error('Invalid parsed PDF page count'));
        return;
      }
      setRead((current) => {
        if (current?.owner !== owner || !current.file || current.error || current.numPages === numPages) return current;
        return { ...current, numPages, loading: false };
      });
    },
    [isCurrent, failed, owner]
  );
  const current = read?.owner === owner ? read : null;
  return {
    owner,
    key: JSON.stringify([source, retry]),
    file: current?.file ?? null,
    numPages: current?.numPages ?? 0,
    loading: current?.loading ?? true,
    error: current?.error ?? null,
    isCurrent,
    parsed,
    failed,
    retry: useCallback(() => setRetry((value) => value + 1), []),
  };
}
