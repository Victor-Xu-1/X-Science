import React, { useId, useLayoutEffect, useRef, useState } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import './libraryAbstract.css';

const PREVIEW_LINES = 8;

function previewHeight(content: HTMLElement): number {
  const style = window.getComputedStyle(content);
  const line = Number.parseFloat(style.lineHeight);
  if (!Number.isFinite(line) || line <= 0) return content.clientHeight;
  const font = Number.parseFloat(style.fontSize);
  const pixels = line < 4 && Number.isFinite(font) && font > 0 ? line * font : line;
  return pixels * PREVIEW_LINES;
}

/** Present only a saved text field; the existing detail reader owns its identity and data. */
export function LibraryAbstract({ value }: { value: unknown }) {
  const { t } = useTranslation();
  const text = typeof value === 'string' && value.trim() ? value : undefined;
  const headingId = useId();
  const textId = useId();
  const paragraph = useRef<HTMLParagraphElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [expandable, setExpandable] = useState(false);

  useLayoutEffect(() => {
    setExpanded(false);
    const content = paragraph.current;
    if (!content) {
      setExpandable(false);
      return;
    }
    let active = true;
    const measure = () => {
      if (!active || !content.isConnected) return;
      setExpandable(content.scrollHeight > previewHeight(content) + 1);
    };
    measure();
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : undefined;
    observer?.observe(content);
    return () => {
      active = false;
      observer?.disconnect();
    };
  }, [text]);

  if (!text) return null;
  return (
    <section className='library-abstract' aria-labelledby={headingId}>
      <h3 id={headingId}>{t('conversation.libraryAbstract.title')}</h3>
      <p
        ref={paragraph}
        id={textId}
        className={`library-abstract__text${expanded ? '' : ' library-abstract__text--collapsed'}`}
      >
        {text}
      </p>
      {expandable && (
        <button
          type='button'
          className='library-abstract__toggle'
          aria-expanded={expanded}
          aria-controls={textId}
          onClick={() => setExpanded((value) => !value)}
        >
          {t(`conversation.libraryAbstract.${expanded ? 'showLess' : 'showMore'}`)}
          {expanded ? <ChevronUp size={14} aria-hidden='true' /> : <ChevronDown size={14} aria-hidden='true' />}
        </button>
      )}
    </section>
  );
}
