import React, { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type { LibraryAttachmentTarget } from './libraryAttachmentTarget';
import { LibraryAttachmentPreview } from './LibraryAttachmentPreview';

/** Inline preview chrome; the browser retains target, account and close ownership. */
export function LibraryAttachmentPane({
  opening,
  current,
  close,
}: {
  opening: { scope: string; target: LibraryAttachmentTarget };
  current?: LibraryAttachmentTarget;
  close: () => void;
}) {
  const { t } = useTranslation();
  const panel = useRef<HTMLElement>(null);
  const { scope, target } = opening;

  // The existing open action owns this identity, including a repeat Preview click.
  useEffect(() => {
    const element = panel.current;
    element?.focus({ preventScroll: true });
    element?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }, [opening]);

  return (
    <section
      ref={panel}
      className='library-workbench__attachment-preview'
      aria-label={t('conversation.libraryWorkbench.attachmentPreview')}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.stopPropagation();
          close();
        }
      }}
    >
      <div className='library-workbench__detail-heading'>
        <strong>
          {target.version.filename} ·{' '}
          {t('conversation.libraryWorkbench.attachmentVersion', { number: target.version.version_number })}
        </strong>
        <button type='button' onClick={close}>
          {t('common.close')}
        </button>
      </div>
      {current?.isPdf ? (
        <div className='library-workbench__attachment-preview-canvas'>
          <LibraryAttachmentPreview
            key={JSON.stringify([scope, current.key])}
            filename={current.version.filename}
            contentUrl={current.contentUrl}
          />
        </div>
      ) : (
        <p role='alert'>{t('conversation.libraryWorkbench.attachmentUnavailable')}</p>
      )}
    </section>
  );
}
