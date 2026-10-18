import type { TChatConversation } from '@/common/config/storage';
import type { XScienceExecutionRecord } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { Alert, Button, Empty, Spin } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import React from 'react';
import { useTranslation } from 'react-i18next';

type SessionNotebookModalProps = {
  conversation: TChatConversation | null;
  records: XScienceExecutionRecord[];
  loading: boolean;
  error: string | null;
  hasOlder: boolean;
  onRetry: () => void;
  onLoadOlder: () => void;
  onClose: () => void;
};

const SessionNotebookModal: React.FC<SessionNotebookModalProps> = ({
  conversation,
  records,
  loading,
  error,
  hasOlder,
  onRetry,
  onLoadOlder,
  onClose,
}) => {
  const { t } = useTranslation();
  return (
    <Modal
      visible={conversation !== null}
      title={t('conversation.history.sessionNotebook.title')}
      footer={null}
      onCancel={onClose}
      style={{ width: 'min(860px, calc(100vw - 32px))' }}
      alignCenter
      unmountOnExit
      getPopupContainer={() => document.body}
    >
      <div className='max-h-[70vh] overflow-y-auto'>
        {error && (
          <div className='mb-12px flex flex-col items-start gap-8px' role='alert'>
            <Alert type='error' content={error} />
            <Button size='small' disabled={loading} onClick={onRetry}>
              {t('common.retry')}
            </Button>
          </div>
        )}
        {loading && records.length === 0 ? (
          <div className='h-120px flex-center'>
            <Spin tip={t('common.loading')} />
          </div>
        ) : records.length === 0 && !error && !hasOlder ? (
          <Empty description={t('conversation.history.sessionNotebook.empty')} />
        ) : (
          <div className='flex flex-col gap-12px'>
            {records.map((record, index) =>
              record.kind === 'cell' ? (
                <section
                  key={record.id}
                  className='border border-solid border-[var(--color-border-2)] bg-1'
                  aria-label={t('conversation.history.sessionNotebook.cellNumber', {
                    number: record.cellIndex + 1,
                  })}
                >
                  <div className='px-12px py-8px flex items-center justify-between border-b border-solid border-[var(--color-border-2)] text-12px text-t-secondary'>
                    <span>{record.language}</span>
                    <span>{record.exitStatus}</span>
                  </div>
                  <pre className='m-0 p-12px overflow-x-auto whitespace-pre-wrap text-12px leading-18px text-t-primary font-mono'>
                    {record.source}
                  </pre>
                  {(record.stdout || record.stderr) && (
                    <pre className='m-0 px-12px py-10px overflow-x-auto whitespace-pre-wrap border-t border-solid border-[var(--color-border-2)] bg-fill-1 text-12px leading-18px text-t-secondary font-mono'>
                      {record.stdout}
                      {record.stderr}
                    </pre>
                  )}
                </section>
              ) : (
                <div key={record.id} className='px-12px py-9px bg-fill-1 text-12px text-t-secondary'>
                  {index + 1}. {record.label}
                  {record.detail ? ` · ${record.detail}` : ''}
                </div>
              )
            )}
          </div>
        )}
        {!error && (hasOlder || (loading && records.length > 0)) && (
          <div className='mt-12px flex justify-center'>
            <Button size='small' disabled={loading} loading={loading} onClick={onLoadOlder}>
              {t(loading ? 'conversation.history.loadingMore' : 'conversation.history.sessionNotebook.loadOlder')}
            </Button>
          </div>
        )}
      </div>
    </Modal>
  );
};

export default SessionNotebookModal;
