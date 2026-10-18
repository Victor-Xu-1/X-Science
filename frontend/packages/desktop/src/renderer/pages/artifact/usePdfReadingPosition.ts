import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import type { PdfReadingPosition } from '@/common/adapter/pdfReadingContext';
import type { VirtuosoHandle } from 'react-virtuoso';

/** Position follows parsed pages and the actual viewport, not a read receipt. */
export function usePdfReadingPosition(
  source: string,
  pageCount: number,
  pages: RefObject<VirtuosoHandle | null>,
  onChange?: (position: PdfReadingPosition) => void,
  targetPage?: number,
  revealId?: string
) {
  const [currentPage, setCurrentPage] = useState(1);
  const viewport = useRef<HTMLElement | null>(null);
  const callback = useRef(onChange);
  callback.current = onChange;
  const currentSource = useRef(source);
  currentSource.current = source;
  const current = useRef(1);
  const report = useCallback(
    (pageNumber: number) => {
      if (currentSource.current !== source || pageCount < 1 || pageNumber < 1 || pageNumber > pageCount) return;
      current.current = pageNumber;
      setCurrentPage(pageNumber);
      callback.current?.({ pageNumber, pageCount });
    },
    [source, pageCount]
  );
  const navigate = useCallback(
    (page: number) => {
      if (!Number.isSafeInteger(page) || page < 1 || page > pageCount) return;
      pages.current?.scrollToIndex({ index: page - 1, align: 'start' });
      report(page);
    },
    [pageCount, pages, report]
  );
  useEffect(() => {
    current.current = 1;
    setCurrentPage(1);
  }, [source]);
  useEffect(() => {
    if (pageCount > 0) navigate(targetPage && targetPage <= pageCount ? targetPage : 1);
  }, [source, pageCount, targetPage, revealId, navigate]);
  useEffect(() => {
    const scroller = viewport.current;
    if (!scroller || !pageCount) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const update = () => {
      timer = undefined;
      const bounds = scroller.getBoundingClientRect();
      if (bounds.height <= 0) return;
      const midpoint = bounds.top + bounds.height / 2;
      const visible = [...scroller.querySelectorAll<HTMLElement>('[data-pdf-page]')]
        .map((element) => ({
          page: Number(element.dataset.pdfPage),
          rect: element.getBoundingClientRect(),
        }))
        .filter(({ rect }) => rect.height > 0 && rect.bottom > bounds.top && rect.top < bounds.bottom);
      const nearest =
        visible.find(({ rect }) => rect.top <= midpoint && rect.bottom >= midpoint) ??
        visible.toSorted((left, right) => Math.abs(left.rect.top - midpoint) - Math.abs(right.rect.top - midpoint))[0];
      if (nearest && nearest.page !== current.current) report(nearest.page);
    };
    const schedule = () => {
      timer ??= setTimeout(update, 100);
    };
    scroller.addEventListener('scroll', schedule, { passive: true });
    return () => {
      scroller.removeEventListener('scroll', schedule);
      if (timer) clearTimeout(timer);
    };
  }, [source, pageCount, report]);
  return {
    currentPage,
    navigate,
    scrollerRef: (element: HTMLElement | Window | null) => {
      viewport.current = element instanceof HTMLElement ? element : null;
    },
  };
}
