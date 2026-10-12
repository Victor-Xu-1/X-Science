import { screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

type MockTableVirtuosoProps = {
  data: string[][];
  fixedHeaderContent: () => React.ReactNode;
  itemContent: (rowIndex: number, row: string[]) => React.ReactNode;
};

vi.mock('react-virtuoso', () => ({
  TableVirtuoso: ({ data, fixedHeaderContent, itemContent }: MockTableVirtuosoProps) => (
    <table>
      <thead>{fixedHeaderContent()}</thead>
      <tbody>
        {data.map((row: string[], rowIndex: number) => (
          <tr key={rowIndex}>{itemContent(rowIndex, row)}</tr>
        ))}
      </tbody>
    </table>
  ),
}));

import XScienceTableViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceTableViewer';

describe('XScienceTableViewer', () => {
  it('renders parsed remote scientific data as a scrollable table rather than source code', async () => {
    await renderWithI18n(
      <XScienceTableViewer filename='scores.csv' content={'gene,score\nSTAT6,0.91\nCRBN,0.83'} />,
      'en-US'
    );

    expect(await screen.findByRole('region', { name: 'Data table preview' })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('2 rows · 2 columns')).toBeInTheDocument());
    expect(screen.getByRole('columnheader', { name: 'gene' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'score' })).toBeInTheDocument();
    expect(screen.getByText('STAT6')).toBeInTheDocument();
    expect(screen.getByText('0.91')).toBeInTheDocument();
  });

  it('renders UTF-8 scientific symbols and multilingual text without mojibake', async () => {
    await renderWithI18n(
      <XScienceTableViewer
        filename='evidence.csv'
        content={'unit,claim\nÅngström,mechanism — confirmed\n中文,NEK7–NLRP3'}
      />,
      'en-US'
    );

    expect(await screen.findByText('Ångström')).toBeInTheDocument();
    expect(screen.getByText('mechanism — confirmed')).toBeInTheDocument();
    expect(screen.getByText('中文')).toBeInTheDocument();
    expect(screen.getByText('NEK7–NLRP3')).toBeInTheDocument();
  });

  it.each(['samples.csv', 'samples.tsv', 'sep-directive.csv'])(
    '%s excludes an incomplete quoted sample from cells and the displayed count',
    async (filename) => {
      const delimiter = filename.startsWith('sep-') ? ';' : filename.endsWith('.tsv') ? '\t' : ',';
      const directive = filename.startsWith('sep-') ? 'sep=;\n' : '';
      const prefix = `${directive}sample${delimiter}note${delimiter}value\n00123${delimiter}valid${delimiter}0.123456789\n00001${delimiter}${delimiter}0\n00124${delimiter}"`;
      const padding = 'x'.repeat(4 * 1024 * 1024 - 100 - new TextEncoder().encode(prefix).byteLength);
      const source = `${prefix}${padding}\n${'y'.repeat(300)}"${delimiter}0.999\n`;

      await renderWithI18n(<XScienceTableViewer filename={filename} content={source} />, 'en-US');

      expect(await screen.findByText('Previewing 2 rows · 3 columns')).toBeInTheDocument();
      const rows = screen.getAllByRole('row');
      expect(rows).toHaveLength(3);
      const cells = within(rows[2]).getAllByRole('cell');
      expect(cells.map((cell) => cell.textContent)).toEqual(['00001', '', '0']);
      expect(cells[2]).toHaveAttribute('title', '0');
      expect(screen.getByText('0.123456789')).toBeInTheDocument();
      expect(screen.queryByText('00124')).not.toBeInTheDocument();
      expect(screen.queryByText('0.999')).not.toBeInTheDocument();
    }
  );

  it('labels a bounded table preview instead of implying that sampled data is complete', async () => {
    const header = Array.from({ length: 140 }, (_, index) => `sample_${index + 1}`).join(',');
    const row = Array.from({ length: 140 }, (_, index) => String(index + 1)).join(',');

    await renderWithI18n(<XScienceTableViewer filename='matrix.csv' content={`${header}\n${row}`} />, 'en-US');

    expect(await screen.findByText('Previewing 1 rows · 120 columns')).toBeInTheDocument();
  });
});
