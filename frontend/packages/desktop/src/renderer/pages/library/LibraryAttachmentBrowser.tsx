import React, { useEffect, useRef, useState } from 'react';
import { FileText } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { LibraryAttachmentPane } from './LibraryAttachmentPane';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { downloadFileFromUrl } from '@/renderer/utils/file/download';
import {
  currentLibraryAttachmentTarget,
  resolveLibraryAttachmentTarget,
  type LibraryAttachmentChoice,
  type LibraryAttachmentTarget,
} from './libraryAttachmentTarget';

type Opened = { scope: string; target: LibraryAttachmentTarget };
type DownloadView = Opened & { status: 'pending' | 'error' | 'started' };
type DownloadRequest = Opened & { controller: AbortController };

/** View state only; the original content API and immutable Library Store own the bytes. */
export function LibraryAttachmentBrowser({ item, account }: { item: LiteratureItem; account: string }) {
  const { t } = useTranslation();
  const scope = JSON.stringify([account, item.id]);
  const scopeRef = useRef(scope);
  const itemRef = useRef(item);
  scopeRef.current = scope;
  itemRef.current = item;
  const requestRef = useRef<DownloadRequest | undefined>(undefined);
  const previewTriggers = useRef(new Map<string, HTMLButtonElement>());
  const [choices, setChoices] = useState<Record<string, LibraryAttachmentChoice>>({});
  const [versionsFor, setVersionsFor] = useState<string>();
  const [opened, setOpened] = useState<Opened>();
  const [download, setDownload] = useState<DownloadView>();
  const currentDownload =
    download?.scope === scope && currentLibraryAttachmentTarget(item, download.target) ? download : undefined;
  const currentOpened = opened?.scope === scope ? opened : undefined;
  const visiblePreview = currentOpened && currentLibraryAttachmentTarget(item, currentOpened.target);

  useEffect(
    () => () => {
      if (requestRef.current?.scope === scope) {
        requestRef.current.controller.abort();
        requestRef.current = undefined;
      }
    },
    [scope]
  );

  useEffect(() => {
    const request = requestRef.current;
    if (request?.scope === scope && !currentLibraryAttachmentTarget(item, request.target)) {
      request.controller.abort();
      requestRef.current = undefined;
      setDownload(undefined);
    }
  }, [item, scope]);

  const cancelDownload = () => {
    requestRef.current?.controller.abort();
    requestRef.current = undefined;
    setDownload(undefined);
  };
  const closePreview = () => {
    if (currentOpened) previewTriggers.current.get(currentOpened.target.attachmentId)?.focus();
    setOpened(undefined);
  };
  const startDownload = async (target: LibraryAttachmentTarget) => {
    if (account !== getRendererAccountScopeToken() || !currentLibraryAttachmentTarget(itemRef.current, target)) return;
    cancelDownload();
    const request = { scope, target, controller: new AbortController() };
    requestRef.current = request;
    const isCurrent = () =>
      requestRef.current === request &&
      scopeRef.current === scope &&
      account === getRendererAccountScopeToken() &&
      Boolean(currentLibraryAttachmentTarget(itemRef.current, target));
    setDownload({ scope, target, status: 'pending' });
    const deadline = setTimeout(() => {
      request.controller.abort();
      if (isCurrent()) setDownload({ scope, target, status: 'error' });
    }, 30_000);
    try {
      await downloadFileFromUrl(target.contentUrl, target.version.filename, {
        signal: request.controller.signal,
        isCurrent,
      });
      if (isCurrent()) setDownload({ scope, target, status: 'started' });
    } catch {
      if (isCurrent()) setDownload({ scope, target, status: 'error' });
    } finally {
      clearTimeout(deadline);
      if (requestRef.current === request) requestRef.current = undefined;
    }
  };

  if (account !== getRendererAccountScopeToken()) return null;
  return (
    <section aria-label={t('conversation.libraryWorkbench.attachments')}>
      <div className='library-workbench__section-heading'>
        <h3>{t('conversation.libraryWorkbench.attachments')}</h3>
        <span>{item.attachments.length}</span>
      </div>
      {item.attachments.length === 0 ? (
        <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noAttachments')}</p>
      ) : (
        <ul className='library-workbench__attachments'>
          {item.attachments.map((attachment) => {
            const choice = choices[attachment.id];
            const target = resolveLibraryAttachmentTarget(item, attachment.id, choice);
            const explicitMissing =
              choice?.kind === 'version' && !attachment.versions.some((value) => value.id === choice.id);
            return (
              <li key={attachment.id}>
                <FileText size={16} aria-hidden='true' />
                <div className='library-workbench__attachment-copy'>
                  <strong>{target?.version.filename || attachment.kind}</strong>
                  <span>
                    {target
                      ? t(
                          choice?.kind === 'version'
                            ? 'conversation.libraryWorkbench.attachmentVersion'
                            : 'conversation.libraryWorkbench.latestVersion',
                          { number: target.version.version_number }
                        )
                      : t('conversation.libraryWorkbench.unavailable')}
                    {' · '}
                    {target?.version.available
                      ? t('conversation.libraryWorkbench.available')
                      : t('conversation.libraryWorkbench.unavailable')}
                  </span>
                  <div className='library-workbench__attachment-actions'>
                    <button
                      ref={(node) => {
                        if (node) previewTriggers.current.set(attachment.id, node);
                        else previewTriggers.current.delete(attachment.id);
                      }}
                      type='button'
                      disabled={!target?.version.available || !target?.isPdf}
                      aria-label={
                        t('conversation.libraryWorkbench.openAttachment') +
                        ' · ' +
                        (target?.version.filename || attachment.kind)
                      }
                      onClick={() => target && setOpened({ scope, target })}
                    >
                      {t('conversation.libraryWorkbench.openAttachment')}
                    </button>
                    <button
                      type='button'
                      disabled={!target?.version.available || currentDownload?.status === 'pending'}
                      aria-label={t('common.download') + ' · ' + (target?.version.filename || attachment.kind)}
                      onClick={() => target && void startDownload(target)}
                    >
                      {t('common.download')}
                    </button>
                    {attachment.versions.length > 1 && (
                      <button
                        type='button'
                        aria-expanded={versionsFor === attachment.id}
                        onClick={() => setVersionsFor(versionsFor === attachment.id ? undefined : attachment.id)}
                      >
                        {t('conversation.libraryWorkbench.chooseAttachmentVersion')}
                      </button>
                    )}
                  </div>
                  {!target?.version.available && (
                    <p className='library-workbench__muted'>
                      {t('conversation.libraryWorkbench.attachmentUnavailable')}
                    </p>
                  )}
                  {target?.version.available && !target.isPdf && (
                    <p className='library-workbench__muted'>
                      {t('conversation.libraryWorkbench.attachmentDownloadOnly')}
                    </p>
                  )}
                  {versionsFor === attachment.id && (
                    <label className='library-workbench__attachment-versions'>
                      {t('conversation.libraryWorkbench.chooseAttachmentVersion')}
                      <select
                        value={choice?.kind === 'version' ? choice.id : ''}
                        onChange={(event) => {
                          if (currentDownload?.target.attachmentId === attachment.id) cancelDownload();
                          setOpened((current) =>
                            current?.target.attachmentId === attachment.id ? undefined : current
                          );
                          setChoices((current) => ({
                            ...current,
                            [attachment.id]: event.target.value
                              ? { kind: 'version', id: event.target.value }
                              : { kind: 'latest' },
                          }));
                        }}
                      >
                        <option value=''>{t('conversation.libraryWorkbench.latestAttachmentVersion')}</option>
                        {explicitMissing && choice?.kind === 'version' && (
                          <option value={choice.id} disabled>
                            {t('conversation.libraryWorkbench.unavailable')}
                          </option>
                        )}
                        {attachment.versions
                          .toSorted((left, right) => right.version_number - left.version_number)
                          .map((version) => (
                            <option key={version.id} value={version.id}>
                              {t('conversation.libraryWorkbench.attachmentVersion', { number: version.version_number })}{' '}
                              · {version.filename}
                              {version.available ? '' : ' · ' + t('conversation.libraryWorkbench.unavailable')}
                            </option>
                          ))}
                      </select>
                    </label>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {currentDownload && (
        <div role={currentDownload.status === 'error' ? 'alert' : 'status'} className='library-workbench__notice'>
          <span>
            {t(
              currentDownload.status === 'pending'
                ? 'conversation.libraryWorkbench.attachmentDownloading'
                : currentDownload.status === 'error'
                  ? 'conversation.libraryWorkbench.attachmentDownloadFailed'
                  : 'conversation.libraryWorkbench.attachmentDownloadStarted',
              { name: currentDownload.target.version.filename }
            )}
          </span>
          {currentDownload.status === 'error' && (
            <button type='button' onClick={() => void startDownload(currentDownload.target)}>
              {t('common.retry')}
            </button>
          )}
          {currentDownload.status === 'pending' && (
            <button type='button' onClick={cancelDownload}>
              {t('common.cancel')}
            </button>
          )}
        </div>
      )}
      {currentOpened && <LibraryAttachmentPane opening={currentOpened} current={visiblePreview} close={closePreview} />}
    </section>
  );
}
