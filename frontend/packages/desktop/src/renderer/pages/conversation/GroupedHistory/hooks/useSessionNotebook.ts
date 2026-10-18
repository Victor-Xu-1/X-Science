import type { TChatConversation } from '@/common/config/storage';
import type { XScienceExecutionRecord } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

type NotebookRuntime = Pick<typeof import('../conversationActionRuntime'), 'loadXScienceExecutionLogPage'>;
type AccountScope = { ownerId: string };
type NotebookState = {
  target: TChatConversation | null;
  records: XScienceExecutionRecord[];
  loading: boolean;
  error: string | null;
  nextBefore: string | null;
};
type Opening = {
  account: AccountScope;
  state: NotebookState;
  controller: AbortController | null;
  failedBefore: string | null | undefined;
};
const emptyState = (): NotebookState => ({ target: null, records: [], loading: false, error: null, nextBefore: null });

// This is a read-only projection of the existing execution log, never a kernel
// or execution owner. Every opening (including A -> B -> A) has its own token.
export function useSessionNotebook(ownerId: string, loadRuntime: () => Promise<NotebookRuntime>) {
  const { t } = useTranslation();
  const accountRef = useRef<AccountScope>({ ownerId });
  if (accountRef.current.ownerId !== ownerId) accountRef.current = { ownerId };
  const account = accountRef.current;
  const openingRef = useRef<Opening | null>(null);
  const mountedRef = useRef(true);
  const [snapshot, setSnapshot] = useState({ account, state: emptyState() });
  const state = snapshot.account === account ? snapshot.state : emptyState();

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      openingRef.current?.controller?.abort();
      openingRef.current = null;
    };
  }, []);
  useEffect(
    () => () => {
      if (openingRef.current?.account === account) {
        openingRef.current.controller?.abort();
        openingRef.current = null;
      }
    },
    [account]
  );
  const isCurrent = useCallback(
    (opening: Opening) =>
      mountedRef.current && accountRef.current === opening.account && openingRef.current === opening,
    []
  );
  const publish = useCallback((opening: Opening, patch: Partial<NotebookState>) => {
    opening.state = { ...opening.state, ...patch };
    setSnapshot({ account: opening.account, state: opening.state });
  }, []);
  const readPage = useCallback(
    async (opening: Opening, before: string | null) => {
      if (!isCurrent(opening) || opening.controller || !opening.state.target) return;
      const controller = new AbortController();
      opening.controller = controller;
      opening.failedBefore = undefined;
      publish(opening, { loading: true, error: null });
      const ownsRead = () => isCurrent(opening) && opening.controller === controller && !controller.signal.aborted;
      try {
        const runtime = await loadRuntime();
        if (!ownsRead()) return;
        const page = await runtime.loadXScienceExecutionLogPage(opening.state.target.id, before, {
          signal: controller.signal,
        });
        if (!ownsRead()) return;
        if (page.nextBefore !== null && page.nextBefore === before)
          throw new Error('execution_log_cursor_not_advanced');
        const seen = new Set(opening.state.records.map((record) => record.id));
        const records =
          before === null
            ? page.records
            : [...page.records.filter((record) => !seen.has(record.id)), ...opening.state.records];
        publish(opening, { records, nextBefore: page.nextBefore });
      } catch (error) {
        if (!ownsRead()) return;
        opening.failedBefore = before;
        console.warn('[SessionNotebook] Failed to load execution records:', redactErrorText(String(error)));
        publish(opening, { error: t('conversation.history.sessionNotebook.loadFailed') });
      } finally {
        if (ownsRead()) {
          opening.controller = null;
          publish(opening, { loading: false });
        }
      }
    },
    [isCurrent, loadRuntime, publish, t]
  );
  const handleViewNotebook = useCallback(
    async (conversation: TChatConversation) => {
      if (!mountedRef.current || !account.ownerId || accountRef.current !== account) return;
      openingRef.current?.controller?.abort();
      const opening: Opening = {
        account,
        state: { ...emptyState(), target: conversation },
        controller: null,
        failedBefore: undefined,
      };
      openingRef.current = opening;
      await readPage(opening, null);
    },
    [account, readPage]
  );
  const handleNotebookClose = useCallback(() => {
    openingRef.current?.controller?.abort();
    openingRef.current = null;
    setSnapshot({ account: accountRef.current, state: emptyState() });
  }, []);
  const handleNotebookRetry = useCallback(async () => {
    const opening = openingRef.current;
    if (opening && opening.failedBefore !== undefined) await readPage(opening, opening.failedBefore);
  }, [readPage]);
  const handleNotebookLoadOlder = useCallback(async () => {
    const opening = openingRef.current;
    if (opening && !opening.state.error && opening.state.nextBefore) await readPage(opening, opening.state.nextBefore);
  }, [readPage]);

  return {
    notebookTarget: state.target,
    notebookRecords: state.records,
    notebookLoading: state.loading,
    notebookError: state.error,
    notebookHasOlder: state.nextBefore !== null,
    handleViewNotebook,
    handleNotebookClose,
    handleNotebookRetry,
    handleNotebookLoadOlder,
  };
}
