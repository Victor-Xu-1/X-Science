import React, { useEffect, useId, useRef, useState } from 'react';
import { Trigger } from '@arco-design/web-react';
import { ChevronRight, Folder, FolderPlus } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { revokeHostFolder } from '@/renderer/services/runtime/hostFiles';
import { HostFolderTree } from './HostFolderTree';
import { HostFolderAccessDialog } from './HostFolderAccessDialog';
import { useHostFiles } from './useHostFiles';
import styles from './hostFiles.module.css';

export default function ComposerHostFiles({
  onReference,
  menuOpen,
  presentation = 'submenu',
}: {
  onReference: (file: { path: string; name: string }) => void;
  menuOpen: boolean;
  presentation?: 'submenu' | 'inline';
}) {
  const { t } = useTranslation();
  const id = useId();
  const owner = useRef(getRendererAccountScopeToken());
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const keyboardOpen = useRef(false);
  const [open, setOpen] = useState(false);
  const [grantOpen, setGrantOpen] = useState(false);
  const [added, setAdded] = useState<ReadonlySet<string>>(() => new Set());
  const visible = menuOpen && (presentation === 'inline' || open);
  const folders = useHostFiles(visible);
  const changeOpen = (next: boolean) => {
    setOpen(next && menuOpen);
    if (!next) setAdded(new Set());
  };
  useEffect(() => {
    if (!menuOpen) {
      setOpen(false);
      setAdded(new Set());
      if (presentation === 'inline') setGrantOpen(false);
    }
  }, [menuOpen, presentation]);
  useEffect(() => {
    if (visible && keyboardOpen.current) {
      keyboardOpen.current = false;
      panel.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    }
  }, [visible]);

  const closeToTrigger = () => {
    changeOpen(false);
    queueMicrotask(() => trigger.current?.focus());
  };
  const error = folders.mutationError && t(`conversation.hostFiles.${folders.mutationError}`);
  const content = (
    <div
      ref={panel}
      id={id}
      role='menu'
      aria-label={t('conversation.attachMenu.yourFiles')}
      className={`${presentation === 'submenu' ? 'app-overlay-menu composer-control-submenu ' : ''}conversation-control-submenu ${styles.panel}`}
      style={
        presentation === 'inline'
          ? { width: '100%' }
          : { width: 'min(300px, calc(100vw - 24px))', maxHeight: 'min(340px, calc(100vh - 24px))' }
      }
      onClick={(event) => event.stopPropagation()}
      onKeyDown={(event) => {
        if (presentation === 'submenu' && (event.key === 'Escape' || event.key === 'ArrowLeft')) {
          event.preventDefault();
          event.stopPropagation();
          closeToTrigger();
        } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
          event.preventDefault();
          event.stopPropagation();
          const buttons = Array.from(
            event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')
          ).filter((button) => !button.closest('[hidden]'));
          const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
          const next =
            event.key === 'Home'
              ? 0
              : event.key === 'End'
                ? buttons.length - 1
                : (current + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
          buttons[next]?.focus();
        }
      }}
    >
      <div className={styles.header}>
        <button
          type='button'
          className={styles.grant}
          disabled={folders.busy || !visible}
          onClick={() => setGrantOpen(true)}
        >
          <FolderPlus size={14} strokeWidth={1.8} aria-hidden='true' />
          {t('conversation.hostFiles.grantShort')}
        </button>
      </div>
      {error && (
        <p role='alert' className={styles.note}>
          {error}
        </p>
      )}
      {folders.refreshRequired && (
        <p role='status' className={styles.note}>
          {t('conversation.hostFiles.refreshRequired')}
        </p>
      )}
      {folders.status === 'error' && (
        <div role='alert' className={styles.note}>
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
      {folders.status === 'loading' && (
        <p role='status' className={styles.note}>
          {t('conversation.hostFiles.loading')}
        </p>
      )}
      {folders.status === 'loaded' && folders.grants.length === 0 && (
        <p className={styles.note}>{t('conversation.hostFiles.empty')}</p>
      )}
      {folders.grants.map((grant) => (
        <HostFolderTree
          key={`${grant.id}:${folders.revision}`}
          grant={grant}
          canReference={
            visible && folders.status === 'loaded' && !folders.busy && (grant.mode === 'ro' || grant.mode === 'rw')
          }
          busy={folders.busy}
          added={added}
          onRemove={() => {
            void folders.changeAccess(() => revokeHostFolder(grant.hostPath));
          }}
          onReference={(file) => {
            if (!visible || owner.current !== getRendererAccountScopeToken()) return;
            onReference(file);
            setAdded((current) => new Set(current).add(file.path));
          }}
        />
      ))}
    </div>
  );
  return (
    <>
      {presentation === 'inline' ? (
        content
      ) : (
        <Trigger
          popup={() => content}
          trigger='hover'
          position='rt'
          popupVisible={visible}
          onVisibleChange={changeOpen}
          mouseEnterDelay={100}
          mouseLeaveDelay={150}
          unmountOnExit={false}
        >
          <button
            ref={trigger}
            type='button'
            role='menuitem'
            aria-haspopup='menu'
            aria-expanded={visible}
            aria-controls={id}
            className={`composer-control-menu__row ${styles.trigger}`}
            onClick={() => changeOpen(true)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
                event.preventDefault();
                event.stopPropagation();
                keyboardOpen.current = true;
                changeOpen(true);
              }
            }}
          >
            <span>
              <Folder size={16} strokeWidth={2} aria-hidden='true' />
            </span>
            <span className={styles.name}>{t('conversation.attachMenu.yourFiles')}</span>
            <ChevronRight size={14} strokeWidth={2} aria-hidden='true' />
          </button>
        </Trigger>
      )}
      {grantOpen && (presentation !== 'inline' || visible) && (
        <Modal
          visible
          title={t('conversation.hostFiles.grant')}
          footer={null}
          onCancel={() => setGrantOpen(false)}
          maskClosable={!folders.busy}
          escToExit={!folders.busy}
          closable={!folders.busy}
          style={{ width: 'min(460px, calc(100vw - 24px))' }}
        >
          {error && <p role='alert'>{error}</p>}
          <HostFolderAccessDialog
            busy={folders.busy}
            changeAccess={folders.changeAccess}
            onDone={() => setGrantOpen(false)}
            onCancel={() => setGrantOpen(false)}
          />
        </Modal>
      )}
    </>
  );
}
