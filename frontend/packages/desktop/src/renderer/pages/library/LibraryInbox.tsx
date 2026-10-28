import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type {
  LiteratureCandidate,
  LiteratureCandidateCommand,
  LiteratureCandidateReceipt,
} from '@/common/adapter/literatureCandidates';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { loadLiteratureCandidates } from '@/renderer/services/runtime/literatureCandidates';

type State = 'pending' | 'dismissed';
type View = {
  key: string;
  status: 'loading' | 'ready' | 'error';
  candidates: LiteratureCandidate[];
  nextCursor?: string;
};

/** Readback is owned by this view; mutations and retries stay with the Library owner. */
export function LibraryInbox({
  account,
  busy,
  receipt,
  pendingOperationId,
  submit,
  close,
  openItem,
}: {
  account: string;
  busy: boolean;
  receipt?: LiteratureCandidateReceipt;
  pendingOperationId?: string;
  submit: (command: LiteratureCandidateCommand) => Promise<LiteratureCandidateReceipt>;
  close: () => void;
  openItem: (itemId: string) => void;
}) {
  const { t } = useTranslation();
  const [state, setState] = useState<State>('pending');
  const [revision, setRevision] = useState(0);
  const key = JSON.stringify([account, state, revision]);
  const [view, setView] = useState<View>({ key, status: 'loading', candidates: [] });
  const moreAbort = useRef<AbortController | null>(null);
  const submitting = useRef(false);
  // Do not project a previous Inbox session's acknowledgement as a new action.
  const receiptBoundary = useRef({ operation: pendingOperationId, before: receipt, pending: pendingOperationId });
  if (pendingOperationId && pendingOperationId !== receiptBoundary.current.pending) {
    receiptBoundary.current = { operation: pendingOperationId, before: receipt, pending: pendingOperationId };
  } else if (!pendingOperationId) receiptBoundary.current.pending = undefined;
  const [moreBusy, setMoreBusy] = useState(false);
  const [moreError, setMoreError] = useState(false);
  const owner = useRef({ key });
  if (owner.current.key !== key) owner.current = { key };
  useEffect(() => {
    const token = owner.current;
    moreAbort.current?.abort();
    moreAbort.current = null;
    setMoreBusy(false);
    setMoreError(false);
    const abort = new AbortController();
    setView({ key, status: 'loading', candidates: [] });
    void loadLiteratureCandidates(state, abort.signal).then(
      (page) => {
        if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
          setView({ key, status: 'ready', ...page });
      },
      () => {
        if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
          setView({ key, status: 'error', candidates: [] });
      }
    );
    return () => {
      abort.abort();
      moreAbort.current?.abort();
    };
  }, [account, state, key]);
  useEffect(() => {
    if (receipt && account === getRendererAccountScopeToken()) setRevision((n) => n + 1);
  }, [account, receipt]);
  const visible: View =
    account === getRendererAccountScopeToken() && view.key === key
      ? view
      : { key, status: 'loading' as const, candidates: [] };
  const more = async () => {
    if (
      visible.status !== 'ready' ||
      !visible.nextCursor ||
      moreAbort.current ||
      account !== getRendererAccountScopeToken()
    )
      return;
    const token = owner.current,
      abort = new AbortController(),
      cursor = visible.nextCursor;
    moreAbort.current = abort;
    setMoreBusy(true);
    setMoreError(false);
    try {
      const page = await loadLiteratureCandidates(state, abort.signal, cursor);
      if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
        setView((previous) =>
          previous.key !== key
            ? previous
            : {
                ...previous,
                candidates: [
                  ...previous.candidates,
                  ...page.candidates.filter((candidate) => !previous.candidates.some((old) => old.id === candidate.id)),
                ],
                nextCursor: page.nextCursor,
              }
        );
    } catch {
      if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
        setMoreError(true);
    } finally {
      if (moreAbort.current === abort) {
        moreAbort.current = null;
        if (owner.current === token) setMoreBusy(false);
      }
    }
  };
  const change = (kind: 'candidate-accept' | 'candidate-dismiss', candidateId: string) => {
    if (busy || submitting.current || account !== getRendererAccountScopeToken()) return;
    submitting.current = true;
    const operation = crypto.randomUUID();
    receiptBoundary.current = { operation, before: receipt, pending: undefined };
    void submit({ kind, operation_id: operation, candidate_id: candidateId })
      .catch((): undefined => undefined)
      .finally(() => {
        submitting.current = false;
      });
  };
  return (
    <section className='library-workbench__collection-content' aria-label={t('conversation.libraryInbox.title')}>
      <header className='library-workbench__navigation-heading'>
        <h2>{t('conversation.libraryInbox.title')}</h2>
        <button type='button' onClick={close}>
          {t('conversation.libraryInbox.back')}
        </button>
      </header>
      <div
        className='library-workbench__attachment-actions'
        role='group'
        aria-label={t('conversation.libraryInbox.title')}
      >
        {(['pending', 'dismissed'] as const).map((value) => (
          <button key={value} type='button' aria-pressed={state === value} onClick={() => setState(value)}>
            {t(`conversation.libraryInbox.${value}`)}
          </button>
        ))}
        <button type='button' onClick={() => setRevision((n) => n + 1)}>
          {t('common.refresh')}
        </button>
      </div>
      {visible.status === 'loading' ? (
        <p role='status'>{t('common.loading')}</p>
      ) : visible.status === 'error' ? (
        <div role='alert'>
          {t('conversation.libraryInbox.failed')}{' '}
          <button type='button' onClick={() => setRevision((n) => n + 1)}>
            {t('common.retry')}
          </button>
        </div>
      ) : visible.candidates.length === 0 ? (
        <p>{t('conversation.libraryInbox.empty')}</p>
      ) : (
        <ul className='library-workbench__list'>
          {visible.candidates.map((candidate) => (
            <li key={candidate.id}>
              <strong>{candidate.title}</strong>
              <p>
                {candidate.provider} · {candidate.external_id}
              </p>
              <div className='library-workbench__attachment-actions'>
                <button type='button' disabled={busy} onClick={() => change('candidate-accept', candidate.id)}>
                  {t('conversation.libraryInbox.accept')}
                </button>
                {state === 'pending' && (
                  <button type='button' disabled={busy} onClick={() => change('candidate-dismiss', candidate.id)}>
                    {t('conversation.libraryInbox.dismiss')}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {visible.status === 'ready' && visible.nextCursor && (
        <button type='button' disabled={moreBusy} onClick={() => void more()}>
          {t(moreError ? 'common.retry' : 'conversation.libraryInbox.more')}
        </button>
      )}
      {moreError && <p role='alert'>{t('conversation.libraryInbox.failed')}</p>}
      {receipt &&
        receipt !== receiptBoundary.current.before &&
        receipt.operation_id === receiptBoundary.current.operation &&
        receipt.candidate.state === 'accepted' &&
        receipt.candidate.item_id && (
          <button type='button' onClick={() => openItem(receipt.candidate.item_id!)}>
            {t('conversation.libraryInbox.openAccepted')}
          </button>
        )}
    </section>
  );
}
