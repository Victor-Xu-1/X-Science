import type { XScienceExecutionRecord } from './runtimeOperationsModel';
import { Button, Empty, Spin } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import './XScienceExecutionLogContent.css';

const XScienceExecutionLogContent: React.FC<{
  records: XScienceExecutionRecord[];
  loading: boolean;
  error: string | null;
  pageFromLatest: number;
  total: number;
  hasEarlier: boolean;
  hasLater: boolean;
  onEarlier: () => void;
  onLater: () => void;
}> = ({ records, loading, error, pageFromLatest, total, hasEarlier, hasLater, onEarlier, onLater }) => {
  const { t, i18n } = useTranslation();
  if (loading)
    return (
      <div className='h-240px flex-center'>
        <Spin tip={t('conversation.xscienceRuntime.runtimeOperations.loadingExecutionLog')} />
      </div>
    );
  if (error) return <div className='px-12px py-20px text-12px text-danger-6'>{error}</div>;
  if (records.length === 0)
    return <Empty description={t('conversation.xscienceRuntime.runtimeOperations.noExecutionLog')} />;
  const end = Math.max(0, total - pageFromLatest * 200);
  const start = records.length === 0 ? 0 : Math.max(1, end - records.length + 1);
  return (
    <div className='x-science-execution-log'>
      <nav
        className='x-science-execution-log__navigation'
        aria-label={t('conversation.xscienceRuntime.runtimeOperations.executionLogNavigation')}
      >
        <Button
          size='mini'
          className='x-science-execution-log__page-button'
          disabled={!hasEarlier || loading}
          aria-label={t('conversation.xscienceRuntime.runtimeOperations.executionLogEarlier')}
          onClick={onEarlier}
        >
          {t('conversation.xscienceRuntime.runtimeOperations.earlier')}
        </Button>
        <span data-testid='execution-log-range' className='x-science-execution-log__range'>
          {t('conversation.xscienceRuntime.runtimeOperations.executionLogRange', {
            start,
            end,
            total,
          })}
        </span>
        <Button
          size='mini'
          className='x-science-execution-log__page-button'
          disabled={!hasLater || loading}
          aria-label={t('conversation.xscienceRuntime.runtimeOperations.executionLogLater')}
          onClick={onLater}
        >
          {t('conversation.xscienceRuntime.runtimeOperations.later')}
        </Button>
      </nav>
      <div className='x-science-execution-log__records'>
        {records.map((record) =>
          record.kind === 'cell' ? (
            <article
              key={record.id}
              data-testid='execution-log-record'
              role='region'
              aria-label={t('conversation.history.sessionNotebook.cellNumber', {
                number: record.cellIndex + 1,
              })}
              className='x-science-execution-log__record'
            >
              <div className='x-science-execution-log__record-meta'>
                <span className='x-science-execution-log__record-index'>[{record.cellIndex}]</span>
                <span className='x-science-execution-log__record-language'>{record.language}</span>
                <span
                  className={`x-science-execution-log__record-status${
                    record.exitStatus === 'ok' ? '' : ' x-science-execution-log__record-status--error'
                  }`}
                >
                  {record.exitStatus}
                </span>
                <span className='x-science-execution-log__record-environment'>
                  {record.kernelKind === 'operon' ? 'REPL' : record.environment}
                </span>
              </div>
              <pre className='x-science-execution-log__source'>{record.source}</pre>
              {record.stderr || record.stdout ? (
                <pre
                  className={`x-science-execution-log__output${
                    record.stderr ? ' x-science-execution-log__output--error' : ''
                  }`}
                >
                  {record.stderr || record.stdout}
                </pre>
              ) : null}
              {record.filesWritten.length > 0 ? (
                <div className='x-science-execution-log__files'>
                  {t('conversation.xscienceRuntime.runtimeOperations.filesWritten', {
                    files: record.filesWritten.map(fileBasename).join(i18n.language === 'zh-CN' ? '、' : ', '),
                  })}
                </div>
              ) : null}
            </article>
          ) : (
            <div key={record.id} data-testid='execution-log-record' className='x-science-execution-log__event'>
              <span className='x-science-execution-log__event-label'>
                {record.kind === 'save'
                  ? record.actor
                    ? t('conversation.xscienceRuntime.runtimeOperations.executionSavedBy', { actor: record.actor })
                    : t('conversation.xscienceRuntime.runtimeOperations.executionSaved')
                  : record.kind === 'app_tool'
                    ? (record.label ?? t('conversation.xscienceRuntime.runtimeOperations.executionAppTool'))
                    : t('conversation.xscienceRuntime.runtimeOperations.executionUserEdit')}
              </span>
              {record.detail ? <span className='x-science-execution-log__event-detail'>{record.detail}</span> : null}
            </div>
          )
        )}
      </div>
    </div>
  );
};

function fileBasename(path: string): string {
  return path.split(/[\\/]/).findLast(Boolean) ?? path;
}

export default XScienceExecutionLogContent;
