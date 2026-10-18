import MarkdownView from '@/renderer/components/Markdown';
import { ArrowUp, MessageCircleMore, Plus, Square, X } from 'lucide-react';
import React, { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { useBtwCommand } from './useBtwCommand';
import { shouldDismissBtwOverlay } from './btwEscapePolicy';
import styles from './BtwOverlay.module.css';

type Controller = ReturnType<typeof useBtwCommand>;
type Props = { controller: Controller; parentTaskRunning?: boolean };
const TOOL_STATES: Record<string, string> = {
  Executing: 'running',
  in_progress: 'running',
  running: 'running',
  Success: 'completed',
  completed: 'completed',
  Error: 'failed',
  error: 'failed',
  failed: 'failed',
  Canceled: 'cancelled',
  canceled: 'cancelled',
  cancelled: 'cancelled',
  interrupted: 'cancelled',
  Pending: 'pending',
  pending: 'pending',
  Confirming: 'waiting',
  confirming: 'waiting',
};

function panelBounds() {
  const max = Math.round(Math.min(window.innerHeight * 0.7, 704));
  return { min: Math.min(288, max), max };
}

const BtwOverlay: React.FC<Props> = ({ controller, parentTaskRunning = false }) => {
  const { t } = useTranslation();
  const titleId = useId();
  const panelId = useId();
  const panel = useRef<HTMLElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const followBottom = useRef(true);
  const drag = useRef<{ pointerId: number; y: number; height: number } | null>(null);
  const [height, setHeight] = useState<number>();
  const [size, setSize] = useState({ min: 0, max: 0, now: 0 });
  const resizeTo = (next: number) => {
    const limit = panelBounds();
    setHeight(Math.min(limit.max, Math.max(limit.min, Math.round(next))));
  };
  useLayoutEffect(() => {
    setHeight(undefined);
    followBottom.current = true;
  }, [controller.generation]);
  useLayoutEffect(() => {
    if (!controller.isOpen || !panel.current) return;
    const measure = () => {
      const limit = panelBounds();
      const now = Math.round(panel.current?.getBoundingClientRect().height ?? 0);
      setSize((current) =>
        current.now === now && current.max === limit.max
          ? current
          : { min: Math.min(limit.min, now), max: Math.max(limit.max, now), now }
      );
      if (height !== undefined && height > limit.max) setHeight(limit.max);
    };
    measure();
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure);
    observer?.observe(panel.current);
    window.addEventListener('resize', measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, [controller.isOpen, height]);
  useLayoutEffect(() => {
    if (controller.isOpen && viewport.current && followBottom.current)
      viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [controller.entries, controller.isOpen, controller.error]);
  useLayoutEffect(() => {
    const element = input.current;
    if (!element) return;
    element.style.height = 'auto';
    element.style.height = `${Math.min(112, Math.max(32, element.scrollHeight))}px`;
  }, [controller.draft, controller.isOpen]);
  useEffect(() => {
    if (controller.isOpen && controller.frameId) input.current?.focus();
  }, [controller.frameId, controller.isOpen]);
  useEffect(() => {
    if (!controller.isOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (!shouldDismissBtwOverlay(event, panel.current)) return;
      event.preventDefault();
      void controller.close();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [controller.close, controller.isOpen]);

  if (!controller.isOpen) return null;
  const send = () => {
    if (controller.canSend && controller.draft.trim()) void controller.send(controller.draft);
  };
  const endDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    if (drag.current?.pointerId !== event.pointerId) return;
    if (event.currentTarget.hasPointerCapture?.(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId);
    drag.current = null;
  };
  return (
    <section
      ref={panel}
      className={styles.panel}
      style={height === undefined ? undefined : { height }}
      role='region'
      aria-labelledby={titleId}
      data-testid='side-chat-panel'
    >
      <div
        role='separator'
        tabIndex={0}
        aria-label={t('conversation.sideQuestion.resizePanel')}
        aria-orientation='horizontal'
        aria-controls={panelId}
        aria-valuemin={size.min}
        aria-valuemax={size.max}
        aria-valuenow={size.now}
        aria-valuetext={`${size.now}px`}
        className={styles.resizeHandle}
        onKeyDown={(event) => {
          if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
            event.preventDefault();
            resizeTo(size.now + (event.key === 'ArrowUp' ? 32 : -32));
          }
        }}
        onPointerDown={(event) => {
          if (event.isPrimary === false || (event.pointerType === 'mouse' && event.button !== 0)) return;
          event.currentTarget.setPointerCapture?.(event.pointerId);
          drag.current = {
            pointerId: event.pointerId,
            y: event.clientY,
            height: panel.current?.getBoundingClientRect().height ?? 0,
          };
        }}
        onPointerMove={(event) => {
          if (drag.current?.pointerId === event.pointerId)
            resizeTo(drag.current.height - (event.clientY - drag.current.y));
        }}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onLostPointerCapture={endDrag}
      >
        <span aria-hidden='true' />
      </div>
      <div id={panelId} className={styles.surface}>
        <header className={styles.header} data-testid='side-chat-header'>
          <MessageCircleMore size={16} strokeWidth={2} aria-hidden='true' />
          <span id={titleId}>{t('conversation.sideQuestion.title')}</span>
          <div className={styles.spacer} />
          {controller.frameId && (
            <a href={`#/conversation/${encodeURIComponent(controller.frameId)}`} className={styles.textButton}>
              {t('conversation.sideQuestion.openTask')}
            </a>
          )}
          <button
            type='button'
            className={styles.iconButton}
            aria-label={t('conversation.sideQuestion.close')}
            title={t('conversation.sideQuestion.close')}
            onClick={() => void controller.close()}
          >
            <X size={16} strokeWidth={2} aria-hidden='true' />
          </button>
        </header>
        <div
          ref={viewport}
          className={styles.messages}
          data-testid='side-chat-message-scroll'
          onScroll={(event) => {
            const element = event.currentTarget;
            followBottom.current = element.scrollHeight - element.clientHeight - element.scrollTop < 48;
          }}
        >
          {parentTaskRunning && <p className={styles.note}>{t('conversation.sideQuestion.parentTaskRunning')}</p>}
          {controller.entries.length === 0 && controller.question && (
            <div className={styles.user}>
              <div>{controller.question}</div>
            </div>
          )}
          {controller.entries.map((entry) => {
            if (entry.kind === 'tool') {
              const status = TOOL_STATES[entry.status];
              return (
                <div key={entry.id} className={styles.tool} data-testid='side-chat-tool'>
                  {entry.title || t('conversation.sideQuestion.tool')}
                  {status ? ` · ${t(`conversation.sideQuestion.toolStates.${status}` as Parameters<typeof t>[0])}` : ''}
                </div>
              );
            }
            if (entry.kind === 'message')
              return entry.role === 'user' ? (
                <div key={entry.id} className={styles.user}>
                  <div>{entry.text}</div>
                </div>
              ) : (
                <div key={entry.id} className={styles.assistant}>
                  <MarkdownView>{entry.text}</MarkdownView>
                </div>
              );
            return (
              <div key={entry.id} className={styles.note}>
                {entry.text}
              </div>
            );
          })}
          {controller.isLoading && (
            <p className={styles.note} role='status'>
              {t(controller.starting ? 'conversation.sideQuestion.started' : 'conversation.sideQuestion.loading')}
            </p>
          )}
          {controller.truncated && <p className={styles.note}>{t('conversation.sideQuestion.olderEntries')}</p>}
          {controller.error && (
            <p role='alert' className={styles.error}>
              {controller.error}
            </p>
          )}
          {controller.observationError && (
            <p role='alert' className={styles.error}>
              {t('conversation.sideQuestion.observationFailed')}
            </p>
          )}
          {controller.persistenceError && (
            <p role='alert' className={styles.error}>
              {t('conversation.sideQuestion.persistenceFailed')}
            </p>
          )}
          {controller.snapshot?.pendingInputRequests.length ? (
            <p className={styles.note}>{t('conversation.sideQuestion.openTaskToRespond')}</p>
          ) : null}
          {(controller.error || controller.observationError) && (
            <div className={styles.recovery}>
              <button
                type='button'
                className={styles.textButton}
                disabled={Boolean(controller.action)}
                onClick={() => void controller.reconnect()}
              >
                {t('conversation.sideQuestion.reconnect')}
              </button>
              {!controller.receipt && !controller.closeRequested && (
                <button
                  type='button'
                  className={styles.textButton}
                  disabled={controller.starting}
                  onClick={() => void controller.retry()}
                >
                  {t('conversation.sideQuestion.retryOriginal')}
                </button>
              )}
              {controller.closeRequested && (
                <button
                  type='button'
                  className={styles.textButton}
                  disabled={Boolean(controller.action)}
                  onClick={() => void controller.close()}
                >
                  {t('conversation.sideQuestion.retryClose')}
                </button>
              )}
              {controller.canRetryFollowup && controller.pendingFollowupText && (
                <button
                  type='button'
                  className={styles.textButton}
                  onClick={() => void controller.send(controller.pendingFollowupText!)}
                >
                  {t('conversation.sideQuestion.retryMessage')}
                </button>
              )}
              {controller.canRetryFollowup && controller.canCancel && (
                <button type='button' className={styles.textButton} onClick={() => void controller.cancel()}>
                  {t('conversation.sideQuestion.cancelPendingMessage')}
                </button>
              )}
            </div>
          )}
          {controller.canResume && (
            <button type='button' className={styles.textButton} onClick={() => void controller.resume()}>
              {t('conversation.sideQuestion.resume')}
            </button>
          )}
        </div>
        <footer className={styles.composer} data-testid='side-chat-composer'>
          <textarea
            ref={input}
            rows={1}
            value={controller.draft}
            className={styles.input}
            placeholder={t('conversation.sideQuestion.followupPlaceholder')}
            aria-label={t('conversation.sideQuestion.followup')}
            onChange={(event) => controller.setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault();
                send();
              }
            }}
          />
          <div className={styles.footerActions}>
            <button
              type='button'
              aria-disabled='true'
              aria-label={t('conversation.sideQuestion.addAttachment')}
              title={t('conversation.sideQuestion.attachmentsUnavailable')}
              className={styles.unavailableAttachment}
              onClick={(event) => event.preventDefault()}
            >
              <Plus size={16} strokeWidth={2} aria-hidden='true' />
            </button>
            <div className={styles.spacer} />
            <button
              type='button'
              className={styles.sendButton}
              disabled={controller.isLoading ? !controller.canCancel : !controller.canSend || !controller.draft.trim()}
              aria-label={t(
                controller.isLoading ? 'conversation.sideQuestion.cancel' : 'conversation.sideQuestion.sendFollowup'
              )}
              onClick={controller.isLoading ? () => void controller.cancel() : send}
            >
              {controller.isLoading ? (
                <Square size={14} strokeWidth={2} aria-hidden='true' />
              ) : (
                <ArrowUp size={16} strokeWidth={2} aria-hidden='true' />
              )}
            </button>
          </div>
        </footer>
      </div>
    </section>
  );
};
export default BtwOverlay;
