import React, { useState } from 'react';
import { Button } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { HostFolderTree } from './HostFolderTree';
import { HostFolderAccessDialog, type HostFolderAccessAction } from './HostFolderAccessDialog';
import { useHostFiles } from './useHostFiles';
import styles from './hostFiles.module.css';

export default function ComposerHostFiles({
  onReference,
  onClose,
}: {
  onReference: (file: { path: string; name: string }) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const folders = useHostFiles();
  const [action, setAction] = useState<HostFolderAccessAction | null>(null);
  return (
    <Modal
      visible
      title={t('conversation.attachMenu.yourFiles')}
      footer={null}
      onCancel={onClose}
      maskClosable={!folders.busy}
      escToExit={!folders.busy}
      closable={!folders.busy}
      style={{ width: 'min(520px, calc(100vw - 24px))' }}
    >
      <div className={styles.panel}>
        <p className={styles.note}>{t('conversation.hostFiles.snapshotHint')}</p>
        <div className={styles.actions}>
          <Button
            size='small'
            onClick={() => {
              void folders.refresh();
            }}
            disabled={folders.busy || folders.status === 'loading'}
          >
            {t('conversation.hostFiles.refresh')}
          </Button>
          <Button size='small' onClick={() => setAction({ kind: 'grant' })} disabled={folders.busy}>
            {t('conversation.hostFiles.grant')}
          </Button>
        </div>
        {folders.mutationError && <p role='alert'>{t(`conversation.hostFiles.${folders.mutationError}`)}</p>}
        {folders.refreshRequired && <p role='status'>{t('conversation.hostFiles.refreshRequired')}</p>}
        {folders.status === 'error' && (
          <div role='alert'>
            {t('conversation.hostFiles.loadFailed')}{' '}
            <button
              type='button'
              disabled={folders.busy}
              onClick={() => {
                void folders.refresh();
              }}
            >
              {t('common.retry')}
            </button>
          </div>
        )}
        {folders.status === 'loading' && <p role='status'>{t('conversation.hostFiles.loading')}</p>}
        {folders.status === 'loaded' && folders.grants.length === 0 && <p>{t('conversation.hostFiles.empty')}</p>}
        <div className={styles.roots}>
          {folders.grants.map((grant) => (
            <div key={`${grant.id}:${folders.revision}`} className={styles.root}>
              <div className={styles.accessRow}>
                <span className={styles.badge}>
                  {t(
                    `conversation.hostFiles.${grant.mode === 'rw' ? 'readWrite' : grant.mode === 'ro' ? 'readOnly' : 'unknownMode'}`
                  )}
                </span>
                <button
                  type='button'
                  disabled={folders.busy}
                  aria-label={t('conversation.hostFiles.changeFor', { name: grant.mountName })}
                  onClick={() => setAction({ kind: 'change', grant })}
                >
                  {t('conversation.hostFiles.change')}
                </button>
                <button
                  type='button'
                  disabled={folders.busy}
                  aria-label={t('conversation.hostFiles.revokeFor', { name: grant.mountName })}
                  onClick={() => setAction({ kind: 'revoke', grant })}
                >
                  {t('conversation.hostFiles.revoke')}
                </button>
              </div>
              <HostFolderTree
                grant={grant}
                canReference={
                  folders.status === 'loaded' && !folders.busy && (grant.mode === 'ro' || grant.mode === 'rw')
                }
                onReference={onReference}
              />
            </div>
          ))}
        </div>
        {action && (
          <HostFolderAccessDialog
            key={action.kind === 'grant' ? 'grant' : `${action.kind}:${action.grant.id}`}
            action={action}
            busy={folders.busy}
            changeAccess={folders.changeAccess}
            onDone={() => setAction(null)}
            onCancel={() => setAction(null)}
          />
        )}
      </div>
    </Modal>
  );
}
