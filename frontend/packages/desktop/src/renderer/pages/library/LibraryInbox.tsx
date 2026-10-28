import React, { useEffect, useRef, useState } from 'react';
import { ArrowLeft, Check, Inbox, RefreshCw, RotateCcw, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type {
  LiteratureCandidate,
  LiteratureCandidateCommand,
  LiteratureCandidateReceipt,
} from '@/common/adapter/literatureCandidates';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { loadLiteratureCandidates } from '@/renderer/services/runtime/literatureCandidates';
import { libraryAuthors, libraryMetadataText } from './libraryBibliographicText';
import './libraryInbox.css';

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
  const change = (kind: 'candidate-accept' | 'candidate-dismiss' | 'candidate-restore', candidateId: string) => {
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
    <section
      className='library-workbench__collection-content library-inbox'
      aria-label={t('conversation.libraryInbox.title')}
    >
      <header className='library-inbox__header'>
        <div>
          <h2>{t('conversation.libraryInbox.title')}</h2>
          <p>{t('conversation.libraryInbox.description')}</p>
        </div>
        <button type='button' className='library-workbench__button' onClick={close}>
          <ArrowLeft size={14} aria-hidden='true' />
          {t('conversation.libraryInbox.back')}
        </button>
      </header>
      <div className='library-inbox__toolbar' role='group' aria-label={t('conversation.libraryInbox.title')}>
        {(['pending', 'dismissed'] as const).map((value) => (
          <button
            key={value}
            type='button'
            className='library-inbox__filter'
            aria-pressed={state === value}
            onClick={() => setState(value)}
          >
            {t(`conversation.libraryInbox.${value}`)}
          </button>
        ))}
        <button
          type='button'
          className='library-workbench__button library-inbox__refresh'
          onClick={() => setRevision((n) => n + 1)}
        >
          <RefreshCw size={14} aria-hidden='true' />
          {t('common.refresh')}
        </button>
      </div>
      {visible.status === 'loading' ? (
        <div className='library-inbox__state' role='status'>
          <RefreshCw size={28} aria-hidden='true' className='library-workbench__spin' />
          <p>{t('common.loading')}</p>
        </div>
      ) : visible.status === 'error' ? (
        <div className='library-inbox__state' role='alert'>
          {t('conversation.libraryInbox.failed')}{' '}
          <button type='button' className='library-workbench__button' onClick={() => setRevision((n) => n + 1)}>
            {t('common.retry')}
          </button>
        </div>
      ) : visible.candidates.length === 0 ? (
        <div className='library-inbox__state'>
          <Inbox size={32} aria-hidden='true' />
          <p>{t('conversation.libraryInbox.empty')}</p>
        </div>
      ) : (
        <ul className='library-inbox__list'>
          {visible.candidates.map((candidate) => (
            <li key={candidate.id} className='library-inbox__card'>
              <div className='library-inbox__bibliography'>
                <h3>{candidate.title}</h3>
                {libraryAuthors(candidate) && <p className='library-inbox__authors'>{libraryAuthors(candidate)}</p>}
                <p className='library-inbox__source' title={candidate.external_id}>
                  {[
                    libraryMetadataText(candidate.metadata.year),
                    libraryMetadataText(candidate.metadata.container_title),
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                  <span>
                    {t('conversation.libraryInbox.foundVia', {
                      provider: candidate.provider === 'semantic-scholar' ? 'Semantic Scholar' : candidate.provider,
                    })}
                  </span>
                </p>
                {libraryMetadataText(candidate.metadata.abstract) && (
                  <p className='library-inbox__abstract'>{libraryMetadataText(candidate.metadata.abstract)}</p>
                )}
              </div>
              <div className='library-inbox__decisions'>
                {state === 'pending' && (
                  <button
                    type='button'
                    className='library-workbench__button'
                    disabled={busy}
                    onClick={() => change('candidate-dismiss', candidate.id)}
                  >
                    <X size={14} aria-hidden='true' />
                    {t('conversation.libraryInbox.dismiss')}
                  </button>
                )}
                <button
                  type='button'
                  className='library-workbench__button library-inbox__accept'
                  disabled={busy}
                  onClick={() => change(state === 'dismissed' ? 'candidate-restore' : 'candidate-accept', candidate.id)}
                >
                  {state === 'dismissed' ? (
                    <RotateCcw size={14} aria-hidden='true' />
                  ) : (
                    <Check size={14} aria-hidden='true' />
                  )}
                  {t(state === 'dismissed' ? 'conversation.libraryInbox.restore' : 'conversation.libraryInbox.accept')}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {visible.status === 'ready' && visible.nextCursor && (
        <button
          type='button'
          className='library-workbench__button library-inbox__more'
          disabled={moreBusy}
          onClick={() => void more()}
        >
          {t(moreError ? 'common.retry' : 'conversation.libraryInbox.more')}
        </button>
      )}
      {moreError && <p role='alert'>{t('conversation.libraryInbox.failed')}</p>}
      {receipt &&
        receipt !== receiptBoundary.current.before &&
        receipt.operation_id === receiptBoundary.current.operation &&
        receipt.candidate.state === 'accepted' &&
        receipt.candidate.item_id && (
          <button
            type='button'
            className='library-workbench__button library-inbox__open'
            onClick={() => openItem(receipt.candidate.item_id!)}
          >
            {t('conversation.libraryInbox.openAccepted')}
          </button>
        )}
    </section>
  );
}
