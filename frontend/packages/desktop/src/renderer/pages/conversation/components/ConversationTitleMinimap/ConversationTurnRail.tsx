import React, { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal, flushSync } from 'react-dom';
import { useTranslation } from 'react-i18next';
import type { TurnPreviewItem } from './minimapTypes';
import { TURN_RAIL_MAX_HEIGHT, TURN_RAIL_ROW_HEIGHT, turnRailScale, turnRailWindow } from './turnRailModel';
import { useTurnRailGeometry } from './useTurnRailGeometry';
import styles from './ConversationTurnRail.module.css';

type Props = { items: TurnPreviewItem[]; viewport: HTMLDivElement | null; onJump: (messageId: string) => void };
export default function ConversationTurnRail({ items, viewport, onJump }: Props) {
  const { t } = useTranslation();
  const geometry = useTurnRailGeometry(items, viewport);
  const rail = useRef<HTMLOListElement>(null);
  const pointerFrame = useRef(0);
  const pointerPosition = useRef(0);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [highlight, setHighlight] = useState<number | null>(null);
  const [focus, setFocus] = useState(-1);
  const [scrollTop, setScrollTop] = useState(0);
  const [preview, setPreview] = useState<{ id: string; x: number; y: number; open: boolean }>();
  const previewId = useId();
  const height = Math.min(items.length * TURN_RAIL_ROW_HEIGHT, TURN_RAIL_MAX_HEIGHT);
  const close = useCallback(() => {
    cancelAnimationFrame(pointerFrame.current);
    pointerFrame.current = 0;
    clearTimeout(closeTimer.current);
    setHighlight(null);
    setPreview((value) => (value?.open ? { ...value, open: false } : value));
  }, []);
  const deferClose = () => {
    clearTimeout(closeTimer.current);
    closeTimer.current = setTimeout(close, 120);
  };
  useEffect(() => close(), [close, geometry?.left, geometry?.top, geometry?.rtl]);
  const reveal = useCallback((index: number) => {
    const list = rail.current;
    if (!list) return;
    const rowTop = index * TURN_RAIL_ROW_HEIGHT;
    const inset = Math.min(TURN_RAIL_ROW_HEIGHT, list.clientHeight / 4);
    list.scrollTop = Math.max(
      0,
      Math.max(rowTop + TURN_RAIL_ROW_HEIGHT + inset - list.clientHeight, Math.min(list.scrollTop, rowTop - inset))
    );
    setScrollTop(list.scrollTop);
  }, []);
  useLayoutEffect(() => {
    if (geometry) reveal(geometry.progress);
  }, [geometry, reveal]);
  useEffect(() => {
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close();
    };
    document.addEventListener('keydown', escape);
    window.addEventListener('resize', close);
    viewport?.addEventListener('scroll', close, { passive: true });
    return () => {
      document.removeEventListener('keydown', escape);
      window.removeEventListener('resize', close);
      viewport?.removeEventListener('scroll', close);
      clearTimeout(closeTimer.current);
      cancelAnimationFrame(pointerFrame.current);
    };
  }, [close, viewport]);
  const show = (index: number, button: HTMLButtonElement) => {
    cancelAnimationFrame(pointerFrame.current);
    pointerFrame.current = 0;
    clearTimeout(closeTimer.current);
    const rect = button.getBoundingClientRect();
    const width = Math.min(256, window.innerWidth - 24);
    const desired = geometry?.rtl ? rect.left - width - 8 : rect.right + 8;
    setHighlight(index);
    setPreview({
      id: items[index].messageId!,
      open: true,
      x: Math.max(12, Math.min(desired, window.innerWidth - width - 12)),
      y: Math.max(12, Math.min(rect.top + rect.height / 2 - 44, window.innerHeight - 100)),
    });
  };
  if (items.length === 0 || !geometry) return null;
  const mark = items.find((item) => item.messageId === preview?.id);
  const open = Boolean(preview?.open && mark);
  const rendered = turnRailWindow(items.length, scrollTop, rail.current?.clientHeight || height, focus);
  const fallback = t('conversation.minimap.content');
  return createPortal(
    <>
      <nav
        aria-label={t('conversation.minimap.turnNavigation')}
        data-testid='conversation-turn-rail'
        dir={geometry.rtl ? 'rtl' : 'ltr'}
        className={styles.rail}
        style={{ left: geometry.left, top: geometry.top }}
      >
        <ol
          ref={rail}
          className={styles.list}
          style={{ height }}
          onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
          onPointerMove={(event) => {
            if (event.pointerType === 'touch') return;
            pointerPosition.current = Math.max(
              0,
              Math.min(
                items.length - 1,
                (event.clientY - event.currentTarget.getBoundingClientRect().top + event.currentTarget.scrollTop) /
                  TURN_RAIL_ROW_HEIGHT -
                  0.5
              )
            );
            if (!pointerFrame.current)
              pointerFrame.current = requestAnimationFrame(() => {
                pointerFrame.current = 0;
                setHighlight(pointerPosition.current);
              });
          }}
          onPointerLeave={() => {
            cancelAnimationFrame(pointerFrame.current);
            pointerFrame.current = 0;
            if (!rail.current?.contains(document.activeElement)) setHighlight(null);
          }}
        >
          <li role='presentation' aria-hidden='true' style={{ height: items.length * TURN_RAIL_ROW_HEIGHT }} />
          {rendered.map((index) => (
            <li
              className={styles.item}
              key={items[index].messageId}
              aria-posinset={index + 1}
              aria-setsize={items.length}
              style={{ top: index * TURN_RAIL_ROW_HEIGHT, height: TURN_RAIL_ROW_HEIGHT }}
            >
              <button
                type='button'
                className={styles.button}
                data-turn-index={index}
                data-visible={geometry.visible.includes(index)}
                aria-current={index === Math.floor(geometry.progress) ? 'location' : undefined}
                aria-label={t('conversation.minimap.goToTurn', {
                  index: index + 1,
                  preview: (items[index].question || fallback).slice(0, 80),
                })}
                aria-describedby={open && mark === items[index] ? previewId : undefined}
                onClick={() => {
                  close();
                  if (items[index].messageId) onJump(items[index].messageId);
                }}
                onFocus={(event) => {
                  setFocus(index);
                  reveal(index);
                  show(index, event.currentTarget);
                }}
                onBlur={() => {
                  setFocus(-1);
                  deferClose();
                }}
                onPointerEnter={(event) => {
                  if (event.pointerType !== 'touch') show(index, event.currentTarget);
                }}
                onPointerLeave={(event) => {
                  if (event.currentTarget !== document.activeElement) deferClose();
                }}
                onKeyDown={(event) => {
                  if (event.key !== 'Tab' || event.altKey || event.ctrlKey || event.metaKey) return;
                  const next = index + (event.shiftKey ? -1 : 1);
                  if (next < 0 || next >= items.length) return;
                  event.preventDefault();
                  flushSync(() => {
                    setFocus(next);
                    reveal(next);
                  });
                  rail.current
                    ?.querySelector<HTMLButtonElement>(`[data-turn-index="${next}"]`)
                    ?.focus({ preventScroll: true });
                }}
              >
                <span
                  aria-hidden='true'
                  className={styles.line}
                  data-distance={open && highlight !== null ? Math.abs(Math.round(highlight) - index) : undefined}
                  style={{ transform: `scaleX(${turnRailScale(open ? highlight : null, index)})` }}
                />
              </button>
            </li>
          ))}
        </ol>
      </nav>
      {preview && mark && (
        <div
          id={previewId}
          role='tooltip'
          data-testid='conversation-turn-preview'
          aria-hidden={!open}
          data-open={open}
          className={styles.preview}
          dir={geometry.rtl ? 'rtl' : 'ltr'}
          style={{ transform: `translate3d(${preview.x}px, ${preview.y}px, 0)` }}
          onPointerEnter={() => clearTimeout(closeTimer.current)}
          onPointerLeave={deferClose}
        >
          <p className={styles.question}>{mark.questionRaw || fallback}</p>
          {mark.answerRaw && <p className={styles.answer}>{mark.answerRaw}</p>}
        </div>
      )}
    </>,
    document.body
  );
}
