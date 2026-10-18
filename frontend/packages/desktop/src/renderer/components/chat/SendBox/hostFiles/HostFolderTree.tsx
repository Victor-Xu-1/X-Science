import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, ChevronRight, FileText, Folder } from 'lucide-react';
import { createHostGrantFileReference } from '@/common/adapter/hostGrantFileReference';
import { readHostFolder, type HostFolderEntry, type XScienceHostGrant } from '@/renderer/services/runtime/hostFiles';
import styles from './hostFiles.module.css';

type TreeProps = {
  grant: XScienceHostGrant;
  relative?: string;
  name?: string;
  canReference: boolean;
  onReference: (file: { path: string; name: string }) => void;
};

export function HostFolderTree({ grant, relative = '', name = grant.mountName, canReference, onReference }: TreeProps) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const [visited, setVisited] = useState(false);
  const canExpand = !relative || Boolean(createHostGrantFileReference(grant.id, relative));
  return (
    <div className={styles.branch}>
      <button
        type='button'
        className={styles.folder}
        aria-expanded={expanded}
        disabled={!canExpand}
        title={!canExpand ? t('conversation.hostFiles.unsupportedName') : undefined}
        aria-label={t(`conversation.hostFiles.${expanded ? 'collapse' : 'expand'}`, { name })}
        onClick={() => {
          if (!canExpand) return;
          setExpanded((current) => !current);
          setVisited(true);
        }}
      >
        {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        <Folder size={15} />
        <span className={styles.name} title={name}>
          {name}
        </span>
        {!canExpand && <span className={styles.badge}>{t('conversation.hostFiles.unsupportedName')}</span>}
      </button>
      {visited && (
        <div hidden={!expanded} className={styles.children}>
          <HostFolderContents grant={grant} relative={relative} canReference={canReference} onReference={onReference} />
        </div>
      )}
    </div>
  );
}

function HostFolderContents({ grant, relative = '', canReference, onReference }: TreeProps) {
  const { t } = useTranslation();
  const [retry, setRetry] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [state, setState] = useState<{ status: 'loading' | 'loaded' | 'error'; entries: HostFolderEntry[] }>({
    status: 'loading',
    entries: [],
  });
  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ ...current, status: 'loading' }));
    void readHostFolder(grant, relative, controller.signal).then(
      (entries) => {
        if (!controller.signal.aborted) setState({ status: 'loaded', entries });
      },
      () => {
        if (!controller.signal.aborted) setState((current) => ({ ...current, status: 'error' }));
      }
    );
    return () => controller.abort();
  }, [grant, relative, retry]);
  if (state.status === 'loading')
    return (
      <div role='status' className={styles.note}>
        {t('conversation.hostFiles.loading')}
      </div>
    );
  if (state.status === 'error')
    return (
      <div role='alert' className={styles.note}>
        {t('conversation.hostFiles.directoryFailed')}{' '}
        <button type='button' onClick={() => setRetry((current) => current + 1)}>
          {t('common.retry')}
        </button>
      </div>
    );
  if (state.entries.length === 0)
    return <div className={styles.note}>{t('conversation.hostFiles.emptyDirectory')}</div>;
  return (
    <>
      {state.entries.map((entry) => {
        const path = relative ? `${relative}/${entry.name}` : entry.name;
        if (entry.isDirectory)
          return (
            <HostFolderTree
              key={path}
              grant={grant}
              relative={path}
              name={entry.name}
              canReference={canReference}
              onReference={onReference}
            />
          );
        const uri = createHostGrantFileReference(grant.id, path);
        const added = selected.has(path);
        return (
          <button
            key={path}
            type='button'
            className={styles.file}
            disabled={!canReference || !uri || added}
            title={!uri ? t('conversation.hostFiles.unsupportedName') : undefined}
            aria-label={t(`conversation.hostFiles.${added ? 'referenced' : 'reference'}`, { name: entry.name })}
            onClick={() => {
              if (!uri || !canReference || selected.has(path)) return;
              setSelected((current) => new Set(current).add(path));
              onReference({ path: uri, name: entry.name });
            }}
          >
            <FileText size={15} />
            <span className={styles.name} title={entry.name}>
              {entry.name}
            </span>
            <span className={styles.badge}>
              {!uri
                ? t('conversation.hostFiles.unsupportedName')
                : t(`conversation.hostFiles.${added ? 'added' : 'addSnapshot'}`)}
            </span>
          </button>
        );
      })}
    </>
  );
}
