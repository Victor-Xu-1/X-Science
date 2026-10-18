import { PRODUCT_NAME } from '@/common/config/productIdentity';
import MarkdownView from '@/renderer/components/Markdown';
import { useLayoutContext } from '@/renderer/hooks/context/LayoutContext';
import { loadXScienceLineageMessages, type XScienceLineageMessage } from '@/renderer/services/xScienceLineageMessages';
import type { XScienceDelegateFrame } from '@/renderer/services/xScienceDelegateLineage';
import { Button, Drawer, Empty, Spin } from '@arco-design/web-react';
import { ArrowRight, Redo } from '@icon-park/react';
import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { DelegatedStopPresentation } from './useDelegatedStop';
import DelegatedStopActions from './DelegatedStopActions';

type XScienceLineageMessagesDrawerProps = {
  child: XScienceDelegateFrame | null;
  onClose: () => void;
  onOpenFull: (frameId: string) => void;
  stop?: DelegatedStopPresentation;
};

const XScienceLineageMessagesDrawer: React.FC<XScienceLineageMessagesDrawerProps> = ({
  child,
  onClose,
  onOpenFull,
  stop,
}) => {
  const layout = useLayoutContext();
  const { t } = useTranslation();
  const [messages, setMessages] = useState<XScienceLineageMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    if (!child) return;
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    setMessages([]);
    void loadXScienceLineageMessages(child.frameId, {
      signal: controller.signal,
    })
      .then((result) => {
        if (!controller.signal.aborted) setMessages(result.items);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) {
          console.error(`[${PRODUCT_NAME} LineageMessagesDrawer] Failed to load child messages`, reason);
          setError(t('conversation.xscienceRuntime.lineage.loadFailed'));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [child, revision, t]);

  return (
    <Drawer
      title={
        child
          ? t('conversation.xscienceRuntime.lineage.childAgentTitle', {
              index: child.ordinal,
              label: child.label,
            })
          : t('conversation.xscienceRuntime.lineage.title')
      }
      visible={Boolean(child)}
      width={layout?.isMobile ? '100%' : 720}
      footer={null}
      unmountOnExit
      onCancel={onClose}
    >
      {child ? (
        <div className='flex h-full min-h-0 flex-col'>
          <div className='mb-10px flex items-center gap-8px border-b border-solid border-[var(--color-border-2)] pb-10px'>
            <span className='min-w-0 flex-1 truncate text-12px text-t-secondary'>
              {child.statusDescription ||
                child.taskSummary ||
                t('conversation.xscienceRuntime.lineage.messageCount', {
                  count: child.messageCount,
                })}
            </span>
            <Button
              size='small'
              icon={<ArrowRight theme='outline' size={14} />}
              onClick={() => onOpenFull(child.frameId)}
            >
              {t('conversation.xscienceRuntime.lineage.openFullTask')}
            </Button>
          </div>
          {stop && <DelegatedStopActions stop={stop} childFrameId={child.frameId} />}
          <div
            className='min-h-0 flex-1 overflow-y-auto px-2px pb-12px'
            aria-label={t('conversation.xscienceRuntime.lineage.transcript')}
          >
            {loading ? (
              <div className='h-220px flex-center'>
                <Spin tip={t('conversation.xscienceRuntime.lineage.loading')} />
              </div>
            ) : error ? (
              <div className='h-220px flex flex-col items-center justify-center gap-10px text-12px text-danger-6'>
                <span>{error}</span>
                <Button
                  size='small'
                  icon={<Redo theme='outline' size={14} />}
                  onClick={() => setRevision((v) => v + 1)}
                >
                  {t('conversation.xscienceRuntime.lineage.reload')}
                </Button>
              </div>
            ) : messages.length === 0 ? (
              <Empty description={t('conversation.xscienceRuntime.lineage.empty')} />
            ) : (
              <div className='flex flex-col gap-12px'>
                {messages.map((message) => (
                  <LineageMessageRow key={message.id} message={message} />
                ))}
              </div>
            )}
          </div>
        </div>
      ) : null}
    </Drawer>
  );
};

const LineageMessageRow: React.FC<{ message: XScienceLineageMessage }> = ({ message }) => {
  const { t } = useTranslation();
  const user = message.position === 'right';
  const label = user
    ? t('conversation.xscienceRuntime.lineage.labels.user')
    : message.kind === 'thinking'
      ? t('conversation.xscienceRuntime.lineage.labels.thinking')
      : message.kind === 'tool'
        ? t('conversation.xscienceRuntime.lineage.labels.tool')
        : message.kind === 'status'
          ? t('conversation.xscienceRuntime.lineage.labels.status')
          : t('conversation.xscienceRuntime.lineage.labels.assistant');
  if (message.kind === 'tool' || message.kind === 'status') {
    return (
      <div className='border-l-2 border-solid border-[var(--color-border-3)] bg-fill-1 px-10px py-8px text-12px text-t-secondary'>
        <span className='mr-7px font-[600] text-t-primary'>{label}</span>
        {message.content || t('conversation.xscienceRuntime.lineage.toolCall')}
      </div>
    );
  }
  return (
    <article className={`flex min-w-0 flex-col ${user ? 'items-end' : 'items-start'}`}>
      <div className='mb-3px text-10px font-[600] text-t-tertiary'>{label}</div>
      <div
        className={`max-w-full break-words text-13px leading-21px text-t-primary ${
          user ? 'rounded-8px rounded-tr-0 bg-fill-2 px-10px py-8px' : 'w-full'
        } ${
          message.kind === 'thinking'
            ? 'border-l-2 border-solid border-[var(--color-border-3)] pl-10px text-t-secondary'
            : ''
        }`}
      >
        {user ? (
          <div className='whitespace-pre-wrap'>{message.content}</div>
        ) : (
          <MarkdownView>{message.content}</MarkdownView>
        )}
      </div>
    </article>
  );
};

export default XScienceLineageMessagesDrawer;
