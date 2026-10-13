import XScienceJsonViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceJsonViewer';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/pages/conversation/Preview/components/editors/CodeEditor', () => ({
  default: ({ value }: { value: string }) => <pre data-testid='json-source'>{value}</pre>,
}));

describe('XScienceJsonViewer', () => {
  it('preserves a source selection made as soon as the document is committed', async () => {
    const ImmediateSelection = () => {
      React.useLayoutEffect(() => {
        screen.getByRole('tab', { name: 'Source' }).click();
      }, []);
      return <XScienceJsonViewer filename='plan.json' content={'{"version":3}'} />;
    };
    await renderWithI18n(<ImmediateSelection />, 'en-US');
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Source' })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.getByTestId('json-source')).toHaveTextContent('"version":3');
  });

  it('uses the structured tree by default and expands nested values', async () => {
    await renderWithI18n(
      <XScienceJsonViewer
        filename='plan.json'
        content={'{"version":3,"phases":[{"title":"Plan"}]}'}
        onCodeSelection={() => undefined}
      />,
      'en-US'
    );

    expect(screen.getByRole('tab', { name: 'Tree' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('version')).toBeInTheDocument();
    fireEvent.click(screen.getByText('phases'));
    fireEvent.click(screen.getByText('[0]'));
    expect(screen.getByText('Plan')).toBeInTheDocument();
  });

  it('resets view state only when the document identity changes', async () => {
    const { rerender } = await renderWithI18n(
      <XScienceJsonViewer filename='first.json' content={'{"a":1}'} />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('tab', { name: 'Source' }));
    rerender(<XScienceJsonViewer filename='first.json' content={'{"a":2}'} />);
    expect(screen.getByRole('tab', { name: 'Source' })).toHaveAttribute('aria-selected', 'true');
    rerender(<XScienceJsonViewer filename='second.json' content={'{"b":3}'} />);
    expect(screen.getByRole('tab', { name: 'Tree' })).toHaveAttribute('aria-selected', 'true');
  });

  it('searches nested values and switches to exact source', async () => {
    await renderWithI18n(
      <XScienceJsonViewer
        filename='plan.json'
        content={'{"phases":[{"title":"Download matrix"}],"owner":"OPERON"}'}
        onCodeSelection={() => undefined}
      />,
      'en-US'
    );

    fireEvent.change(screen.getByRole('textbox', { name: 'Search JSON' }), { target: { value: 'download' } });
    expect(screen.getByText('Download matrix')).toBeInTheDocument();
    expect(screen.queryByText('OPERON')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'Source' }));
    expect(screen.getByTestId('json-source')).toHaveTextContent('"Download matrix"');
  });

  it('falls back to source with a stable diagnostic for invalid JSON', async () => {
    await renderWithI18n(
      <XScienceJsonViewer filename='broken.json' content='{broken' onCodeSelection={() => undefined} />,
      'en-US'
    );

    expect(screen.getByRole('tab', { name: 'Tree' })).toBeDisabled();
    expect(screen.getByTestId('json-source')).toHaveTextContent('{broken');
    expect(screen.getByText('Invalid JSON. Source view is shown instead.')).toBeInTheDocument();
  });

  it.each([
    [
      'en-US',
      'Tree',
      'Some numeric values cannot be shown faithfully in Tree view. Source and Copy JSON preserve the exact original text.',
    ],
    ['zh-CN', '结构', '部分数值无法在树视图中准确显示。源码视图和复制 JSON 会保留完整的原始文本。'],
  ] as const)('shows an honest localized source fallback for precise numbers in %s', async (language, tree, notice) => {
    const content = '{"timestamp":1760000000000000001,"tiny":1e-400,"large":1e400,"zero":-0}';
    await renderWithI18n(<XScienceJsonViewer filename='measurements.json' content={content} />, language);
    expect(screen.getByRole('tab', { name: tree })).toBeDisabled();
    expect(screen.queryByRole('tree')).not.toBeInTheDocument();
    expect(screen.getByTestId('json-source').textContent).toBe(content);
    expect(screen.getByText(notice)).toBeInTheDocument();
  });

  it('switches an existing tree to exact source when an edit introduces imprecise numbers', async () => {
    const { rerender } = await renderWithI18n(
      <XScienceJsonViewer filename='measurements.json' content='{"value":0.1}' />,
      'en-US'
    );
    expect(screen.getByRole('tree')).toBeInTheDocument();
    const content = '{"value":0.100000000000000005}';
    rerender(<XScienceJsonViewer filename='measurements.json' content={content} />);
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Source' })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.getByRole('tab', { name: 'Tree' })).toBeDisabled();
    expect(screen.getByTestId('json-source').textContent).toBe(content);
  });

  it('retains usable exact source for a wide workspace array', async () => {
    const content = `[${'0,'.repeat(149_999)}0]`;
    await renderWithI18n(<XScienceJsonViewer filename='counts.json' content={content} />, 'en-US');
    expect(screen.getByRole('tab', { name: 'Tree' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'Source' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('json-source').textContent).toBe(content);
    expect(
      screen.getByText('The file contains more than 12,000 nodes. Source view is used to keep the page responsive.')
    ).toBeInTheDocument();
  });
});
