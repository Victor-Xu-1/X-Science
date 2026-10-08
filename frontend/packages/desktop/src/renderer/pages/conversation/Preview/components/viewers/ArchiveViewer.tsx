/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ArrowLeft, FileText, FileZip, FolderOpen, Refresh } from '@icon-park/react';
import React, { useId, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { usePreviewContext } from '../../context/PreviewContext';
import type { ArchiveEntry } from './archivePreviewClient';
import { useArchivePreviewRead } from './useArchivePreviewRead';

type ArchiveViewerProps = {
  filename: string;
  contentUrl?: string;
};

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** index;
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`;
}

const ArchiveViewerSession: React.FC<ArchiveViewerProps> = ({ filename, contentUrl }) => {
  const { t } = useTranslation();
  const { openPreview } = usePreviewContext();
  const [containers, setContainers] = useState<string[]>([]);
  const [directory, setDirectory] = useState('');
  const titleId = useId();
  const titleRef = useRef<HTMLHeadingElement>(null);
  const navigationFocus = useRef(false);
  const { listing, loading, error, canReload, entries, reload, openEntry } = useArchivePreviewRead({
    contentUrl,
    containers,
    directory,
    onOpen: openPreview,
  });
  useLayoutEffect(() => {
    if (!navigationFocus.current) return;
    navigationFocus.current = false;
    if (document.activeElement === document.body) titleRef.current?.focus({ preventScroll: true });
  }, [directory, containers, loading]);

  const visibleEntries = useMemo(() => {
    if (!listing) return [];
    const prefix = directory ? `${directory}/` : '';
    return listing.entries
      .filter((entry) => entry.path.startsWith(prefix))
      .filter((entry) => !entry.path.slice(prefix.length).includes('/'))
      .toSorted(
        (left, right) => Number(right.directory) - Number(left.directory) || left.name.localeCompare(right.name)
      );
  }, [directory, listing]);

  const activateEntry = (entry: ArchiveEntry) => {
    if (entry.directory) {
      navigationFocus.current = true;
      setDirectory(entry.path);
      return;
    }
    if (entry.archive) {
      navigationFocus.current = true;
      setContainers((current) => [...current, entry.path]);
      setDirectory('');
      return;
    }
    void openEntry(entry);
  };

  const goBack = () => {
    navigationFocus.current = true;
    if (directory) {
      const parent = directory.split('/').slice(0, -1).join('/');
      setDirectory(parent);
      return;
    }
    if (containers.length > 0) setContainers((current) => current.slice(0, -1));
  };

  const currentName = containers.at(-1)?.split('/').at(-1) ?? filename;
  const canGoBack = Boolean(directory || containers.length);

  return (
    <section
      className='preview-content-scroll flex-1 overflow-auto bg-1 px-18px py-16px'
      aria-labelledby={titleId}
      data-testid='archive-viewer'
    >
      <div className='mx-auto max-w-920px'>
        <div className='mb-14px flex items-center gap-10px border-b border-border-base pb-12px'>
          <button
            type='button'
            className='h-30px w-30px shrink-0 rounded-7px border-0 bg-transparent text-t-secondary hover:bg-fill-2 disabled:opacity-30'
            disabled={!canGoBack}
            aria-label={t('preview.archive.back')}
            onClick={goBack}
          >
            <ArrowLeft size={15} />
          </button>
          <FileZip size={18} className='shrink-0 text-t-secondary' />
          <div className='min-w-0 flex-1'>
            <h2
              id={titleId}
              ref={titleRef}
              tabIndex={-1}
              title={currentName}
              className='m-0 truncate text-14px font-600 text-t-primary'
            >
              {currentName}
            </h2>
            <div className='truncate text-12px text-t-tertiary'>
              {[filename, ...containers, directory].filter(Boolean).join(' / ')}
            </div>
          </div>
          <button
            type='button'
            className='h-30px w-30px rounded-7px border-0 bg-transparent text-t-secondary hover:bg-fill-2'
            aria-label={t('preview.archive.refresh')}
            disabled={loading || !canReload}
            onClick={() => void reload()}
          >
            <Refresh size={15} />
          </button>
        </div>

        {loading ? (
          <div role='status' className='py-56px text-center text-13px text-t-secondary'>
            {t('preview.archive.loading')}
          </div>
        ) : error ? (
          <div role='alert' className='rounded-8px bg-fill-1 px-14px py-12px text-13px text-t-secondary'>
            {t(`preview.archive.${error}`)}
          </div>
        ) : visibleEntries.length === 0 ? (
          <div className='py-56px text-center text-13px text-t-tertiary'>{t('preview.archive.empty')}</div>
        ) : (
          <div className='overflow-hidden rounded-10px border border-border-base bg-1'>
            {visibleEntries.map((entry) => (
              <div key={entry.path} className='border-0 border-b border-solid border-border-base last:border-b-0'>
                <button
                  type='button'
                  className='flex w-full items-center gap-11px border-0 bg-transparent px-13px py-10px text-left hover:bg-fill-1 disabled:cursor-wait'
                  disabled={entries.get(entry.path) === 'pending'}
                  onClick={() => activateEntry(entry)}
                >
                  {entry.directory ? (
                    <FolderOpen size={17} className='shrink-0 text-t-secondary' />
                  ) : entry.archive ? (
                    <FileZip size={17} className='shrink-0 text-t-secondary' />
                  ) : (
                    <FileText size={17} className='shrink-0 text-t-tertiary' />
                  )}
                  <span className='min-w-0 flex-1 truncate text-13px text-t-primary' title={entry.name}>
                    {entry.name}
                  </span>
                  {!entry.directory && (
                    <span className='shrink-0 text-12px text-t-tertiary'>{formatBytes(entry.size)}</span>
                  )}
                  {entries.get(entry.path) === 'pending' && (
                    <span role='status' className='shrink-0 text-12px text-t-secondary'>
                      {t('preview.archive.readingEntry')}
                    </span>
                  )}
                </button>
                {entries.get(entry.path) === 'failed' && (
                  <div className='flex flex-wrap items-center gap-8px px-13px pb-10px text-12px text-t-secondary'>
                    <span role='alert'>{t('preview.archive.entryFailedNamed', { name: entry.name })}</span>
                    <button
                      type='button'
                      className='min-h-28px rounded-6px border-0 bg-fill-2 px-8px text-t-primary'
                      aria-label={t('preview.archive.retryEntryNamed', { name: entry.name })}
                      onClick={() => {
                        titleRef.current?.focus({ preventScroll: true });
                        void openEntry(entry);
                      }}
                    >
                      {t('common.retry')}
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </section>
  );
};

const ArchiveViewer: React.FC<ArchiveViewerProps> = (props) => (
  <ArchiveViewerSession key={props.contentUrl ?? ''} {...props} />
);

export default ArchiveViewer;
