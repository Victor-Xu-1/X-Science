import React, { useId } from 'react';
import { useTranslation } from 'react-i18next';
import type { NotebookSessionNavigation } from '@/renderer/services/notebook/manualNotebookTypes';
import { notebookTargetKey } from '@/renderer/services/notebook/NotebookSessionStore';
import { useLiveNotebook } from './useLiveNotebook';
import { NotebookCodeCell } from './NotebookCodeCell';
import { NotebookRequests } from './NotebookRequests';
import { useNotebookSplit } from './useNotebookSplit';
import styles from './notebook.module.css';

export default function LiveNotebookPreview({ navigation }: { navigation: NotebookSessionNavigation }) {
  const { t } = useTranslation();
  const inputId = useId();
  const { controller, snapshot } = useLiveNotebook(navigation);
  const split = useNotebookSplit();
  const selected = controller.selected();
  const pending = controller.unresolved(selected ?? undefined);
  const canRun =
    snapshot.status === 'ready' &&
    Boolean(selected) &&
    !snapshot.commandBusy &&
    !pending &&
    snapshot.scanComplete &&
    !snapshot.scanning &&
    snapshot.error !== 'pendingFailed' &&
    !snapshot.busyKernelIds.includes(selected?.kernelId ?? '');
  const run = () => {
    if (canRun && selected && snapshot.draft.trim()) void controller.run(notebookTargetKey(selected), snapshot.draft);
  };
  const cells = snapshot.history.filter(
    (record) =>
      record.kind === 'cell' &&
      record.frameId === navigation.frameId &&
      (!selected || record.kernelId === selected.kernelId)
  );
  return (
    <section
      className={styles.pane}
      aria-label={t('conversation.manualNotebook.title')}
      data-testid='live-notebook-preview'
    >
      <header className={styles.header}>
        <span>{t('conversation.manualNotebook.title')}</span>
        <code title={navigation.frameId}>{navigation.frameId.slice(0, 8)}</code>
        <button
          type='button'
          onClick={() => {
            void controller.refresh();
          }}
        >
          {t('common.refresh')}
        </button>
      </header>
      {snapshot.status === 'loading' && (
        <p role='status' className={styles.notice}>
          {t('common.loading')}
        </p>
      )}
      {snapshot.status === 'scopeChanged' && (
        <p role='alert' className={styles.notice}>
          {t('conversation.manualNotebook.scopeChanged')}
        </p>
      )}
      {snapshot.error && (
        <p role='alert' className={`${styles.notice} ${styles.error}`}>
          {t(`conversation.manualNotebook.${snapshot.error}`)}
        </p>
      )}
      <div className={styles.targets} role='tablist' aria-label={t('conversation.manualNotebook.targets')}>
        {snapshot.targets.map((target) => (
          <button
            type='button'
            role='tab'
            aria-selected={snapshot.selectedKey === notebookTargetKey(target)}
            key={notebookTargetKey(target)}
            onClick={() => controller.select(notebookTargetKey(target))}
          >
            <span>{target.language === 'r' ? 'R' : 'Python'}</span>
            <span className={styles.environment}>{target.environment}</span>
            <code>{target.kernelId.slice(0, 8)}</code>
          </button>
        ))}
      </div>
      <div className={styles.history}>
        {snapshot.historyError && (
          <p role='alert'>
            {t('conversation.manualNotebook.historyFailed')}
            <button
              type='button'
              onClick={() => {
                void controller.refresh();
              }}
            >
              {t('common.retry')}
            </button>
          </p>
        )}
        {snapshot.historyBefore && (
          <button
            type='button'
            disabled={snapshot.historyLoading}
            onClick={() => {
              void controller.loadOlder();
            }}
          >
            {t('conversation.history.sessionNotebook.loadOlder')}
          </button>
        )}
        {cells.map((record) => record.kind === 'cell' && <NotebookCodeCell key={record.id} cell={record} />)}
        {!snapshot.historyLoading && !snapshot.historyError && cells.length === 0 && (
          <p className={styles.notice}>{t('conversation.manualNotebook.noCells')}</p>
        )}
      </div>
      <NotebookRequests controller={controller} snapshot={snapshot} />
      {selected ? (
        <>
          <div
            role='separator'
            tabIndex={0}
            aria-orientation='horizontal'
            aria-valuemin={15}
            aria-valuemax={55}
            aria-valuenow={split.percent}
            aria-label={t('conversation.manualNotebook.resize')}
            className={styles.separator}
            onPointerDown={split.onPointerDown}
            onKeyDown={(event) => {
              if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
                event.preventDefault();
                split.adjust(event.key === 'ArrowUp' ? 5 : -5);
              }
            }}
          >
            <span>
              {t('conversation.manualNotebook.sharedKernel', { language: selected.language === 'r' ? 'R' : 'Python' })}
            </span>
            <span>
              {t(
                pending
                  ? 'conversation.manualNotebook.pending'
                  : snapshot.busyKernelIds.includes(selected.kernelId)
                    ? 'conversation.manualNotebook.busy'
                    : 'conversation.manualNotebook.ready'
              )}
            </span>
          </div>
          <form
            className={styles.terminal}
            style={{ flexBasis: `${split.percent}%` }}
            onSubmit={(event) => {
              event.preventDefault();
              run();
            }}
          >
            <label className={styles.inputLabel} htmlFor={inputId}>
              {t('conversation.manualNotebook.code')}
            </label>
            <div className={styles.inputRow}>
              <span aria-hidden='true'>{selected.language === 'r' ? '>' : '>>>'}</span>
              <textarea
                id={inputId}
                aria-label={t('conversation.manualNotebook.code')}
                value={snapshot.draft}
                disabled={!canRun}
                spellCheck={false}
                autoComplete='off'
                autoCapitalize='off'
                placeholder={t('conversation.manualNotebook.placeholder')}
                onChange={(event) => controller.setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                    event.preventDefault();
                    run();
                  }
                }}
              />
            </div>
            <div className={styles.footer}>
              <span>{t('conversation.manualNotebook.inputHint')}</span>
              <button type='submit' disabled={!canRun || !snapshot.draft.trim()}>
                {t('conversation.manualNotebook.run')}
              </button>
            </div>
          </form>
        </>
      ) : (
        <footer className={styles.notice}>{t('conversation.manualNotebook.readOnly')}</footer>
      )}
    </section>
  );
}
