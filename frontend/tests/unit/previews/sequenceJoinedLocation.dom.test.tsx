import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18next from 'i18next';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { gzipSync } from 'node:zlib';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, describe, expect, it, vi } from 'vitest';
import XScienceSequenceViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceSequenceViewer';
import enPreview from '@/renderer/services/i18n/locales/en-US/preview.json';
import zhPreview from '@/renderer/services/i18n/locales/zh-CN/preview.json';
import { resolveXScienceArtifactPreviewPlan } from '@/renderer/services/xScienceArtifactPreview';

const fixture = (name: string): string =>
  readFileSync(resolve('tests/fixtures/sequence-joined-location', name.includes('.') ? name : `${name}.gbk`), 'utf8');
const errorMessages = {
  'en-US':
    'This GenBank record uses joined or ordered feature locations that this preview cannot display accurately. Open the original file in a viewer that supports compound feature locations.',
  'zh-CN':
    '此 GenBank 文件包含连接（join）或有序（order）的多片段特征位置，当前预览无法准确显示。请使用支持多片段特征位置的查看器打开原始文件。',
};

// Supply jsdom layout only. Both scientific packages are the real locked engines.
class SizedObserver {
  private readonly timers: ReturnType<typeof setTimeout>[] = [];
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe(target: Element): void {
    this.timers.push(
      setTimeout(
        () =>
          this.callback(
            [
              {
                target,
                contentRect: { width: 1200, height: 500 },
                contentBoxSize: [{ inlineSize: 1200, blockSize: 500 }],
                borderBoxSize: [{ inlineSize: 1200, blockSize: 500 }],
              } as unknown as ResizeObserverEntry,
            ],
            this as unknown as ResizeObserver
          ),
        0
      )
    );
  }
  unobserve(): void {}
  disconnect(): void {
    this.timers.forEach(clearTimeout);
  }
}

async function mount(name: string, route = 'inline', language: 'en-US' | 'zh-CN' = 'en-US') {
  vi.stubGlobal('ResizeObserver', SizedObserver);
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  const source = fixture(name);
  const filename = `${name.includes('.') ? name : `${name}.gbk`}${route === 'gzip' ? '.gz' : ''}`;
  const fetchMock = vi.fn(async () => {
    const body = route === 'gzip' ? gzipSync(source) : Buffer.from(source);
    return new Response(body, { headers: { 'content-length': String(body.length) } });
  });
  if (route !== 'inline') vi.stubGlobal('fetch', fetchMock);
  const i18n = i18next.createInstance();
  await i18n.init({
    lng: language,
    fallbackLng: 'en-US',
    resources: { 'en-US': { translation: { preview: enPreview } }, 'zh-CN': { translation: { preview: zhPreview } } },
    interpolation: { escapeValue: false },
  });
  const view = render(
    <I18nextProvider i18n={i18n}>
      <XScienceSequenceViewer
        filename={filename}
        content={route === 'inline' ? source : undefined}
        contentUrl={route === 'inline' ? undefined : '/api/artifacts/compound-fixture'}
      />
    </I18nextProvider>
  );
  return { view, fetchMock, i18n };
}

async function selectCopy(view: ReturnType<typeof render>, mode: 'Linear' | 'Circular') {
  await screen.findByTestId('la-vz-seqviz', {}, { timeout: 10000 });
  fireEvent.click(screen.getByRole('radio', { name: mode }));
  let path: Element | null = null;
  await waitFor(() => {
    path = view.container.querySelector(
      mode === 'Circular'
        ? '.la-vz-circular-annotations .la-vz-annotation'
        : '.la-vz-linear-annotations .la-vz-annotation'
    );
    if (!path && mode === 'Linear') path = view.container.querySelector('.la-vz-annotation');
    expect(path).toBeTruthy();
  });
  let copied: string | undefined;
  const copy = vi.fn(() => {
    copied = (document.querySelector('textarea') as HTMLTextAreaElement)?.innerText;
    return true;
  });
  Object.defineProperty(document, 'execCommand', { configurable: true, value: copy });
  fireEvent.mouseDown(path!, { button: 0 });
  fireEvent.mouseUp(path!, { button: 0 });
  fireEvent.keyDown(view.container.querySelector('.la-vz-viewer-event-router')!, { key: 'c', ctrlKey: true });
  await waitFor(() => expect(copy).toHaveBeenCalledWith('copy'));
  return copied;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('real GenBank preview compound-location rejection', () => {
  it.each(
    ['joined', 'reverse', 'origin'].flatMap((name) => ['inline', 'url', 'gzip'].map((route) => [name, route] as const))
  )('rejects %s via %s before any partial annotation or successful statistics appear', async (name, route) => {
    expect(resolveXScienceArtifactPreviewPlan({ filename: `${name}.gbk${route === 'gzip' ? '.gz' : ''}` })).toEqual({
      type: 'sequence',
      fetchText: false,
    });
    const { view, fetchMock } = await mount(name, route);
    expect(await screen.findByText(errorMessages['en-US'])).toBeInTheDocument();
    expect(view.queryByTestId('x-science-sequence-host')).not.toBeInTheDocument();
    expect(view.queryByTestId('la-vz-seqviz')).not.toBeInTheDocument();
    expect(view.container.textContent).not.toContain('36 bp · 1 annotations');
    if (route !== 'inline')
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/artifacts/compound-fixture',
        expect.objectContaining({ credentials: 'include', signal: expect.any(AbortSignal) })
      );
  });

  it.each(['inline', 'url', 'gzip'])(
    'preserves native JBEI parsing/rendering with a GenBank example in an XML comment via %s',
    async (route) => {
      const filename = `dispatch-comment.jbei${route === 'gzip' ? '.gz' : ''}`;
      expect(resolveXScienceArtifactPreviewPlan({ filename })).toEqual({ type: 'sequence', fetchText: false });
      const { view, fetchMock } = await mount('dispatch-comment.jbei', route);
      expect(await screen.findByText('9 bp · 0 annotations')).toBeInTheDocument();
      expect(await screen.findByTestId('la-vz-seqviz', {}, { timeout: 10000 })).toBeInTheDocument();
      expect(screen.queryByText(errorMessages['en-US'])).not.toBeInTheDocument();
      let copied: string | undefined;
      const copy = vi.fn(() => {
        copied = (document.querySelector('textarea') as HTMLTextAreaElement)?.innerText;
        return true;
      });
      Object.defineProperty(document, 'execCommand', { configurable: true, value: copy });
      const router = view.container.querySelector('.la-vz-viewer-event-router')!;
      fireEvent.keyDown(router, { key: 'a', ctrlKey: true });
      fireEvent.keyDown(router, { key: 'c', ctrlKey: true });
      await waitFor(() => expect(copy).toHaveBeenCalledWith('copy'));
      expect(copied).toBe('ATGCCCTAA');
      if (route !== 'inline')
        expect(fetchMock).toHaveBeenCalledWith(
          '/api/artifacts/compound-fixture',
          expect.objectContaining({ credentials: 'include', signal: expect.any(AbortSignal) })
        );
    }
  );

  it('provides the actionable compound-location explanation in Chinese', async () => {
    const { view } = await mount('joined', 'inline', 'zh-CN');
    expect(await screen.findByText(errorMessages['zh-CN'])).toBeInTheDocument();
    expect(view.queryByTestId('la-vz-seqviz')).not.toBeInTheDocument();
  });

  it.each(['Linear', 'Circular'] as const)(
    'preserves all nine bases when selecting/copying the native contiguous annotation in %s mode',
    async (mode) => {
      const { view } = await mount('contiguous');
      expect(await screen.findByText('36 bp · 1 annotations')).toBeInTheDocument();
      expect(await selectCopy(view, mode)).toBe('atgccctaa');
      expect(screen.getByRole('radio', { name: mode })).toHaveAttribute('aria-checked', 'true');
      fireEvent.change(screen.getByRole('slider', { name: 'Sequence zoom' }), { target: { value: '75' } });
      expect(screen.getByRole('slider', { name: 'Sequence zoom' })).toHaveValue('75');
      expect(screen.getByText('75%')).toBeInTheDocument();
    }
  );

  it('clears an earlier successful preview on rejection and restores it after returning to a supported record', async () => {
    const { view, i18n } = await mount('contiguous');
    expect(await selectCopy(view, 'Linear')).toBe('atgccctaa');
    view.rerender(
      <I18nextProvider i18n={i18n}>
        <XScienceSequenceViewer filename='joined.gbk' content={fixture('joined')} />
      </I18nextProvider>
    );
    expect(await screen.findByText(errorMessages['en-US'])).toBeInTheDocument();
    expect(view.queryByTestId('la-vz-seqviz')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', { name: 'Circular' }));
    expect(view.queryByTestId('la-vz-seqviz')).not.toBeInTheDocument();
    view.rerender(
      <I18nextProvider i18n={i18n}>
        <XScienceSequenceViewer filename='contiguous.gbk' content={fixture('contiguous')} />
      </I18nextProvider>
    );
    expect(await selectCopy(view, 'Linear')).toBe('atgccctaa');
    expect(screen.queryByText(errorMessages['en-US'])).not.toBeInTheDocument();
  });
});
