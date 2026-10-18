import React, { useRef, useState } from 'react';
import { Button, Input, Select } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import {
  chooseAndGrantHostFolder,
  grantHostFolder,
  revokeHostFolder,
  type HostFolderMode,
  type XScienceHostGrant,
} from '@/renderer/services/runtime/hostFiles';
import styles from './hostFiles.module.css';

export type HostFolderAccessAction = { kind: 'grant' } | { kind: 'change' | 'revoke'; grant: XScienceHostGrant };

export function HostFolderAccessDialog({
  action,
  busy,
  changeAccess,
  onDone,
  onCancel,
}: {
  action: HostFolderAccessAction;
  busy: boolean;
  changeAccess: (operation: () => Promise<boolean | void>) => Promise<boolean>;
  onDone: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [path, setPath] = useState(action.kind === 'grant' ? '' : action.grant.hostPath);
  const [mode, setMode] = useState<HostFolderMode>(action.kind !== 'grant' && action.grant.mode === 'rw' ? 'rw' : 'ro');
  const submitted = useRef(false);
  const run = async (choose: boolean) => {
    if (submitted.current || busy) return;
    submitted.current = true;
    try {
      const succeeded = await changeAccess(() =>
        action.kind === 'revoke'
          ? revokeHostFolder(action.grant.hostPath)
          : choose
            ? chooseAndGrantHostFolder(mode)
            : grantHostFolder(path.trim(), mode, action.kind === 'change')
      );
      if (succeeded) onDone();
    } finally {
      submitted.current = false;
    }
  };
  return (
    <section className={styles.access} aria-label={t(`conversation.hostFiles.${action.kind}`)}>
      <h3>{t(`conversation.hostFiles.${action.kind}`)}</h3>
      <p role='note' className={styles.warning}>
        {t('conversation.hostFiles.confinementWarning')}
      </p>
      {action.kind === 'revoke' ? (
        <p>{t('conversation.hostFiles.revokeExplanation', { name: action.grant.mountName })}</p>
      ) : (
        <>
          <label className={styles.field}>
            {t('conversation.hostFiles.folderPath')}
            <Input
              aria-label={t('conversation.hostFiles.folderPath')}
              value={path}
              onChange={setPath}
              disabled={busy || action.kind === 'change'}
            />
          </label>
          <div className={styles.field}>
            <span>{t('conversation.hostFiles.accessMode')}</span>
            <Select
              aria-label={t('conversation.hostFiles.accessMode')}
              value={mode}
              onChange={setMode}
              disabled={busy}
              options={[
                { value: 'ro', label: t('conversation.hostFiles.readOnly') },
                { value: 'rw', label: t('conversation.hostFiles.readWrite') },
              ]}
            />
          </div>
          <p className={styles.note}>{t('conversation.hostFiles.operatorHint')}</p>
        </>
      )}
      <div className={styles.actions}>
        <Button onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
        {action.kind === 'grant' && (
          <Button
            onClick={() => {
              void run(true);
            }}
            disabled={busy}
          >
            {t('conversation.hostFiles.chooseAndGrant')}
          </Button>
        )}
        <Button
          type='primary'
          onClick={() => {
            void run(false);
          }}
          disabled={busy || (action.kind !== 'revoke' && !path.trim())}
          loading={busy}
        >
          {t(`conversation.hostFiles.${action.kind}`)}
        </Button>
      </div>
    </section>
  );
}
