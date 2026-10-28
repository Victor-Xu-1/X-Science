import React from 'react';
import { act, cleanup, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { LibraryBibliographicDetails } from '@/renderer/pages/library/LibraryBibliographicDetails';
import { renderWithI18n } from '../../i18nTestUtils';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('presents saved scalar metadata in both languages and reads replacement props without requests or writes', async () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const metadata = Object.freeze({
    item_type: 'journalArticle',
    container_title: 'Saved publication',
    url: 'https://example.org/paper?version=1',
    volume: '12',
    issue: '3',
    pages: '40–52',
    publisher: 'Saved publisher',
    publisher_place: 'Cambridge',
    edition: 'Second edition',
  });
  const view = await renderWithI18n(<LibraryBibliographicDetails metadata={metadata} />, 'en-US');
  expect(screen.getByText('Journal article')).toBeInTheDocument();
  expect(screen.getByText('Reference type')).toBeInTheDocument();
  for (const value of Object.values(metadata).filter((value) => value !== 'journalArticle'))
    expect(screen.getByText(value)).toBeInTheDocument();
  expect(view.container.querySelectorAll('dt')).toHaveLength(9);
  expect(view.container.querySelectorAll('button,input,textarea,select,form')).toHaveLength(0);
  const link = screen.getByRole('link', { name: `URL: ${metadata.url}` });
  expect(link).toHaveAttribute('href', metadata.url);
  expect(link).toHaveAttribute('target', '_blank');
  expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  await act(async () => {
    await view.i18n.changeLanguage('zh-CN');
  });
  expect(screen.getByText('期刊文章')).toBeInTheDocument();
  expect(screen.getByText('出版物')).toBeInTheDocument();
  expect(screen.getByText('版次')).toBeInTheDocument();
  view.rerender(<LibraryBibliographicDetails metadata={Object.freeze({ item_type: 'preprint', pages: 'S1–S4' })} />);
  expect(screen.getByText('预印本')).toBeInTheDocument();
  expect(screen.getByText('S1–S4')).toBeInTheDocument();
  expect(screen.queryByText('Saved publication')).not.toBeInTheDocument();
  expect(screen.queryByRole('link')).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
  expect(metadata.item_type).toBe('journalArticle');
});

it('omits missing or nontext fields and preserves unknown types and literal source text', async () => {
  const metadata: Readonly<LiteratureItem['metadata']> = Object.freeze({
    item_type: 'unknown <type>',
    container_title: '<img src=x onerror=alert(1)>',
    volume: 12,
    issue: ['3'],
    pages: null,
    publisher: { name: 'Not a saved scalar' },
    publisher_place: false,
    edition: ' \n ',
    creators: [{ familyName: 'Outside this scalar scope' }],
  });
  const view = await renderWithI18n(<LibraryBibliographicDetails metadata={metadata} />, 'en-US');
  expect(screen.getByText('unknown <type>')).toBeInTheDocument();
  expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeInTheDocument();
  expect(view.container.querySelectorAll('dt')).toHaveLength(2);
  expect(view.container.querySelectorAll('img,script')).toHaveLength(0);
  expect(screen.queryByText('Document')).not.toBeInTheDocument();
  expect(view.container).not.toHaveTextContent('[object Object]');
  view.rerender(<LibraryBibliographicDetails metadata={Object.freeze({ item_type: 42, url: [] })} />);
  expect(view.container).toBeEmptyDOMElement();
});

it('links only safe absolute HTTP(S) URLs while keeping other saved URLs as escaped text', async () => {
  const view = await renderWithI18n(<LibraryBibliographicDetails metadata={{}} />, 'en-US');
  for (const [value, href] of [
    ['http://example.org/paper', 'http://example.org/paper'],
    ['HTTPS://example.org/paper', 'https://example.org/paper'],
    ['javascript:alert(1)', undefined],
    ['data:text/html,<script>alert(1)</script>', undefined],
    ['file:///tmp/source.pdf', undefined],
    ['/relative/paper', undefined],
    ['//example.org/paper', undefined],
    ['https://user:password@example.org/paper', undefined],
    ['ht\ntps://example.org/paper', undefined],
    ['https://', undefined],
  ] as const) {
    view.rerender(<LibraryBibliographicDetails metadata={Object.freeze({ url: value })} />);
    const stored = view.container.querySelector('dd');
    expect(stored?.textContent).toBe(value);
    expect(stored).toHaveAttribute('title', value);
    if (href) expect(screen.getByRole('link')).toHaveAttribute('href', href);
    else expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(view.container.querySelectorAll('script,img')).toHaveLength(0);
  }
});
