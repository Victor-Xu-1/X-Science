import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@nightingale-elements/nightingale-msa', () => ({}));

import XScienceMsaViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceMsaViewer';

describe('XScienceMsaViewer', () => {
  it('renders parsed alignment data through the Nightingale custom element', async () => {
    const { container } = await renderWithI18n(
      <XScienceMsaViewer filename='aligned.fasta' content={'>alpha\nAC-E\n>beta\nACFE'} />,
      'en-US'
    );

    expect(await screen.findByRole('region', { name: 'Multiple sequence alignment preview' })).toBeInTheDocument();
    expect(await screen.findByText('2 sequences · 4 positions')).toBeInTheDocument();

    await waitFor(() => expect(container.querySelector('nightingale-msa')).not.toBeNull());
    const msa = container.querySelector('nightingale-msa') as HTMLElement & {
      data?: Array<{ name: string; sequence: string }>;
    };
    expect(msa.getAttribute('color-scheme')).toBe('clustal2');
    expect(msa.data).toEqual([
      { name: 'alpha', sequence: 'AC-E' },
      { name: 'beta', sequence: 'ACFE' },
    ]);

    fireEvent.change(screen.getByRole('combobox', { name: 'Color scheme' }), { target: { value: 'nucleotide' } });
    expect(msa.getAttribute('color-scheme')).toBe('nucleotide');
  });

  it.each([
    [
      'en-US',
      'This Stockholm file contains multiple alignments. Open a file containing a single alignment to preview it.',
    ],
    ['zh-CN', '此 Stockholm 文件包含多个比对。请打开仅包含一个比对的文件进行预览。'],
  ] as const)('shows an actionable multiple-record error in %s', async (language, message) => {
    const first = '# STOCKHOLM 1.0\nalpha AC-G\nbeta ACUG\n//\n';
    const second = '# STOCKHOLM 1.0\nalpha UU-C\nbeta UUGC\n//\n';
    const { container } = await renderWithI18n(
      <XScienceMsaViewer filename='families.sto' content={first + second} />,
      language
    );

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(container.querySelector('nightingale-msa')).toBeNull();
    expect(screen.queryByText('2 sequences · 8 positions')).not.toBeInTheDocument();
  });

  it('passes every normalized alternative-gap Stockholm row to the custom element', async () => {
    const { container } = await renderWithI18n(
      <XScienceMsaViewer
        filename='alignment.sto'
        content={'# STOCKHOLM 1.0\nalpha ac_g\nbeta ac~g\ngamma ACUG\n//\n'}
      />,
      'en-US'
    );

    expect(await screen.findByText('3 sequences · 4 positions')).toBeInTheDocument();
    await waitFor(() => expect(container.querySelector('nightingale-msa')).not.toBeNull());
    const msa = container.querySelector('nightingale-msa') as HTMLElement & {
      data?: Array<{ name: string; sequence: string }>;
    };
    await waitFor(() =>
      expect(msa.data).toEqual([
        { name: 'alpha', sequence: 'AC-G' },
        { name: 'beta', sequence: 'AC-G' },
        { name: 'gamma', sequence: 'ACUG' },
      ])
    );
  });
});
