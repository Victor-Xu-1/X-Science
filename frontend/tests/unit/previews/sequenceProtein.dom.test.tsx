import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSequenceViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceSequenceViewer';
import { renderWithI18n } from '../i18nTestUtils';

// Supply jsdom layout only; seqparse and SeqViz are the real locked engines.
class SequenceSizeObserver {
  private timer?: ReturnType<typeof setTimeout>;
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe(target: Element): void {
    this.timer = setTimeout(() => {
      this.callback(
        [
          {
            target,
            contentRect: { width: 900, height: 500 },
            contentBoxSize: [{ inlineSize: 900, blockSize: 500 }],
            borderBoxSize: [{ inlineSize: 900, blockSize: 500 }],
          } as unknown as ResizeObserverEntry,
        ],
        this as unknown as ResizeObserver
      );
    }, 0);
  }
  unobserve(): void {}
  disconnect(): void {
    clearTimeout(this.timer);
  }
}

beforeEach(() => vi.stubGlobal('ResizeObserver', SequenceSizeObserver));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('native protein sequence preview', () => {
  it('renders amino-acid units and the intact identifier in the native linear viewer', async () => {
    const view = await renderWithI18n(
      <XScienceSequenceViewer
        filename='sequence.fasta'
        content={'>db|SYNTHETIC001|Example protein description\nMPEPTIDEQF'}
      />,
      'en-US'
    );
    await screen.findByTestId('la-vz-seqviz');

    expect(screen.getByText('10 aa · 0 annotations')).toBeInTheDocument();
    expect(screen.getByTitle('db|SYNTHETIC001|Example protein description')).toHaveTextContent(
      'db|SYNTHETIC001|Example protein description'
    );
    expect(await screen.findByTestId('la-vz-viewer-linear')).toBeInTheDocument();
    expect(screen.queryByTestId('la-vz-viewer-circular')).not.toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Circular' })).not.toBeInTheDocument();
    expect(view.container.textContent).not.toContain('10 bp');
    await waitFor(() => expect(view.container.querySelector('.la-vz-linear-aa-translation')).toBeInTheDocument());
  });

  it('renders protein residues and neutral unknown symbols in Chinese', async () => {
    const view = await renderWithI18n(
      <XScienceSequenceViewer filename='protein.fasta' content={'>protein\nMPEPTIDEQF'} />,
      'zh-CN'
    );
    expect(await screen.findByText('10 aa · 0 个注释')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: '线性' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.queryByRole('radio', { name: '环形' })).not.toBeInTheDocument();

    view.rerender(<XScienceSequenceViewer filename='ambiguous.fasta' content={'>uncertain\nACGTRYSWKMBDHVN'} />);
    expect(await screen.findByText('15 字符 · 0 个注释')).toBeInTheDocument();
    expect(await screen.findByTestId('x-science-sequence-symbols')).toHaveTextContent('ACGTRYSWKMBDHVN');
    expect(screen.queryByTestId('la-vz-seqviz')).not.toBeInTheDocument();
    expect(screen.queryByTestId('la-vz-viewer-circular')).not.toBeInTheDocument();
    expect(view.container.querySelector('.la-vz-linear-aa-translation')).not.toBeInTheDocument();
    expect(view.container.querySelector('.la-vz-comp-seq')).not.toBeInTheDocument();
    expect(screen.queryByRole('slider', { name: '序列缩放' })).not.toBeInTheDocument();
  });

  it('keeps DNA controls and view choice while RNA and proteins use accurate linear units', async () => {
    const view = await renderWithI18n(
      <XScienceSequenceViewer filename='control.fasta' content={'>dna\nACGTN'} />,
      'en-US'
    );
    expect(await screen.findByText('5 bp · 0 annotations')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', { name: 'Circular' }));
    expect(await screen.findByTestId('la-vz-viewer-circular')).toBeInTheDocument();

    view.rerender(<XScienceSequenceViewer filename='protein.fasta' content={'>protein\nMPEPTIDEQF'} />);
    expect(await screen.findByText('10 aa · 0 annotations')).toBeInTheDocument();
    expect(await screen.findByTestId('la-vz-viewer-linear')).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Circular' })).not.toBeInTheDocument();

    view.rerender(<XScienceSequenceViewer filename='rna.fasta' content={'>rna\nACGUN'} />);
    expect(await screen.findByText('5 nt · 0 annotations')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Linear' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.queryByRole('radio', { name: 'Circular' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('la-vz-viewer-circular')).not.toBeInTheDocument();

    view.rerender(<XScienceSequenceViewer filename='control.fasta' content={'>dna\nACGTN'} />);
    expect(await screen.findByText('5 bp · 0 annotations')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Circular' })).toHaveAttribute('aria-checked', 'true');

    view.rerender(<XScienceSequenceViewer filename='ambiguous.fasta' content={'>uncertain\nACGTRYSWKMBDHVN'} />);
    expect(await screen.findByText('15 symbols · 0 annotations')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-sequence-symbols')).toHaveTextContent('ACGTRYSWKMBDHVN');
    expect(screen.queryByRole('slider', { name: 'Sequence zoom' })).not.toBeInTheDocument();
  });

  it('bounds the native circular caption while preserving the complete long DNA header', async () => {
    const header = `db|SYNTHETIC004🧬|Long\tDNA\u2028description ${'additional metadata '.repeat(80)}end`;
    const view = await renderWithI18n(
      <XScienceSequenceViewer filename='long.fasta' content={`>${header}\n${'ACGT'.repeat(30)}`} />,
      'en-US'
    );
    await screen.findByTestId('la-vz-viewer-circular');
    const fullName = view.container.querySelector('span[title]');
    expect(fullName!.getAttribute('title')).toBe(header);
    expect(fullName!.textContent).toBe(header);
    await waitFor(() => {
      const caption = view.container.querySelector('.la-vz-viewer-circular text[font-size="20"]');
      expect(caption).toBeInTheDocument();
      expect(caption!.textContent!.length).toBeLessThanOrEqual(60);
      expect(caption!.querySelectorAll('tspan').length).toBeLessThanOrEqual(2);
      expect(caption!.textContent).not.toMatch(/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u);
      expect(new TextDecoder().decode(new TextEncoder().encode(caption!.textContent!))).toBe(caption!.textContent);
    });
  });
});
