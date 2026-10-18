import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ArrowUpRight, Check, ChevronRight, File, Folder, X } from 'lucide-react';
import { createHostGrantFileReference } from '@/common/adapter/hostGrantFileReference';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { readHostFolder, type HostFolderEntry, type XScienceHostGrant } from '@/renderer/services/runtime/hostFiles';
import styles from './hostFiles.module.css';

type TreeProps = {
  grant: XScienceHostGrant;
  relative?: string;
  name?: string;
  depth?: number;
  busy: boolean;
  canReference: boolean;
  added: ReadonlySet<string>;
  onReference: (file: { path: string; name: string }) => void;
  onRemove?: () => void;
};

export function HostFolderTree({
  grant,
  relative = '',
  name = grant.mountName,
  depth = 0,
  onRemove,
  ...actions
}: TreeProps) {
  const { t } = useTranslation();
  const owner = useRef(getRendererAccountScopeToken());
  const [expanded, setExpanded] = useState(false);
  const [visited, setVisited] = useState(false);
  const canExpand = !relative || Boolean(createHostGrantFileReference(grant.id, relative));
  const expand = (next: boolean) => {
    if (!canExpand || actions.busy || owner.current !== getRendererAccountScopeToken()) return;
    setExpanded(next);
    if (next) setVisited(true);
  };
  const accessLabel = t(
    `conversation.hostFiles.${grant.mode === 'rw' ? 'readWrite' : grant.mode === 'ro' ? 'readOnly' : 'unknownMode'}`
  );
  return (
    <div>
      <div className={styles.row} style={{ paddingInlineStart: 6 + depth * 18 }}>
        <button
          type='button'
          className={styles.folder}
          aria-expanded={expanded}
          disabled={!canExpand || actions.busy}
          title={!canExpand ? t('conversation.hostFiles.unsupportedName') : name}
          aria-label={t(`conversation.hostFiles.${expanded ? 'collapse' : 'expand'}`, { name })}
          onClick={() => expand(!expanded)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowRight' || (event.key === 'ArrowLeft' && expanded)) {
              event.preventDefault();
              event.stopPropagation();
              expand(event.key === 'ArrowRight');
            }
          }}
        >
          <ChevronRight
            size={14}
            strokeWidth={2}
            aria-hidden='true'
            className={expanded ? styles.expanded : undefined}
          />
          <Folder size={16} strokeWidth={1.8} aria-hidden='true' />
          <span className={styles.name}>{name}</span>
          {!canExpand && <span className={styles.unsupported}>{t('conversation.hostFiles.unsupportedName')}</span>}
        </button>
        {!relative && (
          <>
            <span className={`${styles.badge} ${grant.mode === 'rw' ? styles.writable : ''}`} title={accessLabel}>
              {grant.mode === 'ro' || grant.mode === 'rw' ? grant.mode : '?'}
            </span>
            <button
              type='button'
              className={styles.remove}
              disabled={actions.busy}
              aria-label={t('conversation.hostFiles.revokeFor', { name: grant.mountName })}
              title={t('conversation.hostFiles.revokeFor', { name: grant.mountName })}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                onRemove?.();
              }}
            >
              <X size={14} strokeWidth={2} aria-hidden='true' />
            </button>
          </>
        )}
      </div>
      {visited && (
        <div hidden={!expanded}>
          <HostFolderContents grant={grant} relative={relative} depth={depth + 1} {...actions} />
        </div>
      )}
    </div>
  );
}

function HostFolderContents({ grant, relative = '', depth = 1, ...actions }: TreeProps) {
  const { t } = useTranslation();
  const owner = useRef(getRendererAccountScopeToken());
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<{ status: 'loading' | 'loaded' | 'error'; entries: HostFolderEntry[] }>({
    status: 'loading',
    entries: [],
  });
  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ ...current, status: 'loading' }));
    if (owner.current !== getRendererAccountScopeToken()) return () => controller.abort();
    void readHostFolder(grant, relative, controller.signal).then(
      (entries) => {
        if (!controller.signal.aborted && owner.current === getRendererAccountScopeToken())
          setState({ status: 'loaded', entries });
      },
      () => {
        if (!controller.signal.aborted && owner.current === getRendererAccountScopeToken())
          setState((current) => ({ ...current, status: 'error' }));
      }
    );
    return () => controller.abort();
  }, [grant, relative, retry]);
  const indent = { paddingInlineStart: 6 + depth * 18 };
  if (state.status === 'loading')
    return (
      <div role='status' className={styles.note} style={indent}>
        {t('conversation.hostFiles.loading')}
      </div>
    );
  if (state.status === 'error')
    return (
      <div role='alert' className={styles.note} style={indent}>
        {t('conversation.hostFiles.directoryFailed')}{' '}
        <button type='button' disabled={actions.busy} onClick={() => setRetry((current) => current + 1)}>
          {t('common.retry')}
        </button>
      </div>
    );
  if (state.entries.length === 0)
    return (
      <div className={styles.note} style={indent}>
        {t('conversation.hostFiles.emptyDirectory')}
      </div>
    );
  return (
    <>
      {state.entries.map((entry) => {
        const path = relative ? `${relative}/${entry.name}` : entry.name;
        if (entry.isDirectory)
          return (
            <HostFolderTree key={path} grant={grant} relative={path} name={entry.name} depth={depth} {...actions} />
          );
        const uri = createHostGrantFileReference(grant.id, path);
        const added = Boolean(uri && actions.added.has(uri));
        return (
          <div key={path} className={`${styles.row} ${styles.file}`} style={indent} title={path}>
            <File size={16} strokeWidth={1.8} aria-hidden='true' />
            <span className={styles.filename}>{entry.name}</span>
            {!uri && <span className={styles.unsupported}>{t('conversation.hostFiles.unsupportedName')}</span>}
            <button
              type='button'
              className={`${styles.send} ${added ? styles.added : ''}`}
              disabled={!actions.canReference || !uri || added}
              title={t(`conversation.hostFiles.${added ? 'added' : 'addSnapshot'}`)}
              aria-label={t(`conversation.hostFiles.${added ? 'referenced' : 'reference'}`, { name: entry.name })}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                if (!uri || !actions.canReference || added || owner.current !== getRendererAccountScopeToken()) return;
                actions.onReference({ path: uri, name: entry.name });
              }}
            >
              {added ? (
                <Check size={16} strokeWidth={2.2} aria-hidden='true' />
              ) : (
                <ArrowUpRight size={16} strokeWidth={1.8} aria-hidden='true' />
              )}
            </button>
          </div>
        );
      })}
    </>
  );
}
