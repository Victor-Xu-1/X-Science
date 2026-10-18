import React, { useEffect, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { XScienceExecutionCell } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import styles from './notebook.module.css';

export function NotebookCodeCell({ cell }: { cell: XScienceExecutionCell }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearTimeout(timer.current);
    };
  }, []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(cell.source);
      if (!mounted.current) return;
      setCopied(true);
      setFailed(false);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    } catch {
      if (mounted.current) setFailed(true);
    }
  };
  return (
    <article
      className={styles.cell}
      data-testid='live-notebook-cell'
      aria-label={t('conversation.manualNotebook.cell', { number: cell.cellIndex })}
    >
      <header className={styles.cellHeader}>
        <span className={styles.mono}>[{cell.cellIndex}]</span>
        <span>{cell.language}</span>
        <span>{cell.environment}</span>
        {cell.origin === 'user' && <span className={styles.user}>{t('conversation.manualNotebook.you')}</span>}
        <span className={cell.exitStatus === 'ok' ? '' : styles.error}>{cell.exitStatus}</span>
      </header>
      <div className={styles.source}>
        <button
          type='button'
          className={styles.copy}
          disabled={!navigator.clipboard?.writeText}
          onClick={() => {
            void copy();
          }}
          aria-label={t(copied ? 'conversation.manualNotebook.copied' : 'conversation.manualNotebook.copy')}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />}
        </button>
        <pre>
          <code>
            {cell.source.split('\n').map((line, index) => (
              <span
                className={`${styles.sourceLine} ${cell.errorLine === index + 1 ? styles.errorLine : ''}`}
                key={index}
              >
                <span aria-hidden='true' className={styles.lineNumber}>
                  {index + 1}
                </span>
                <span>{line || ' '}</span>
              </span>
            ))}
          </code>
        </pre>
      </div>
      {cell.stdout && (
        <pre className={styles.output} aria-label={t('conversation.manualNotebook.stdout')}>
          {cell.stdout}
        </pre>
      )}
      {cell.stderr && (
        <pre className={`${styles.output} ${styles.error}`} aria-label={t('conversation.manualNotebook.stderr')}>
          {cell.stderr}
        </pre>
      )}
      {failed && (
        <p role='alert' className={styles.error}>
          {t('conversation.manualNotebook.copyFailed')}
        </p>
      )}
    </article>
  );
}
