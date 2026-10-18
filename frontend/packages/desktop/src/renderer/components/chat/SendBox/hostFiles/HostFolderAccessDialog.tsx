import React, { useRef, useState } from 'react';
import { Button, Input, Select } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import { chooseAndGrantHostFolder, grantHostFolder, type HostFolderMode } from '@/renderer/services/runtime/hostFiles';
import styles from './hostFiles.module.css';

export function HostFolderAccessDialog({
  busy,
  changeAccess,
  onDone,
  onCancel,
}: {
  busy: boolean;
  changeAccess: (operation: () => Promise<boolean | void>) => Promise<boolean>;
  onDone: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [path, setPath] = useState('');
  const [mode, setMode] = useState<HostFolderMode>('ro');
  const submitted = useRef(false);
  const run = async (choose: boolean) => {
    if (submitted.current || busy) return;
    submitted.current = true;
    try {
      const succeeded = await changeAccess(() =>
        choose ? chooseAndGrantHostFolder(mode) : grantHostFolder(path.trim(), mode)
      );
      if (succeeded) onDone();
    } finally {
      submitted.current = false;
    }
  };
  return (
    <section aria-label={t('conversation.hostFiles.grant')}>
      <p role='note' className={styles.warning}>
        {t('conversation.hostFiles.confinementWarning')}
      </p>
      <label className={styles.field}>
        {t('conversation.hostFiles.folderPath')}
        <Input aria-label={t('conversation.hostFiles.folderPath')} value={path} onChange={setPath} disabled={busy} />
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
      <div className={styles.actions}>
        <Button onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
        <Button
          onClick={() => {
            void run(true);
          }}
          disabled={busy}
        >
          {t('conversation.hostFiles.chooseAndGrant')}
        </Button>
        <Button
          type='primary'
          onClick={() => {
            void run(false);
          }}
          disabled={busy || !path.trim()}
          loading={busy}
        >
          {t('conversation.hostFiles.grant')}
        </Button>
      </div>
    </section>
  );
}
