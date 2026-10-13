import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import XScienceTextArtifactViewer from '@/renderer/pages/artifact/XScienceTextArtifactViewer';
import { resolveMarkdownArtifactVersionReferences } from '@/renderer/pages/conversation/Preview/components/viewers/markdownArtifactReferences';
import { renderWithI18n } from '../i18nTestUtils';
import {
  resolveXScienceArtifactPreviewPlan,
  X_SCIENCE_TEXT_ACCEPT_HEADER,
} from '@/renderer/services/xScienceArtifactPreview';
import { Message } from '@arco-design/web-react';

vi.mock('@/renderer/pages/conversation/Preview/components/editors/CodeEditor', () => ({
  default: ({ value, fileName }: { value: string; fileName: string }) => (
    <pre aria-label={`${fileName} editor`}>{value}</pre>
  ),
}));

describe('XScienceTextArtifactViewer', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('passes a downloaded HTML response through passive srcDoc isolation', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response('<h1>Downloaded source</h1><script>parent.pwned=true</script>', {
            status: 200,
            headers: { 'X-Science-HTML-Preview': 'passive' },
          })
      )
    );
    const { container } = await renderWithI18n(
      <XScienceTextArtifactViewer filename='source.html' contentUrl='/api/artifacts/source' kind='html' />,
      'en-US'
    );
    await waitFor(() => expect(container.querySelector('iframe')?.srcdoc).toContain('Downloaded source'));
    const frame = container.querySelector('iframe');
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts');
    expect(frame?.srcdoc).not.toContain('parent.pwned');
  });

  it('preserves the declared encoding of a passive source preview', async () => {
    const body = Buffer.from('<h1>完整来源</h1>', 'utf16le');
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(body, {
            status: 200,
            headers: { 'Content-Type': 'text/html; charset=utf-16le', 'X-Science-HTML-Preview': 'passive' },
          })
      )
    );
    const { container } = await renderWithI18n(
      <XScienceTextArtifactViewer filename='source.html' contentUrl='/api/artifacts/source' kind='html' />,
      'en-US'
    );
    await waitFor(() => expect(container.querySelector('iframe')?.srcdoc).toContain('完整来源'));
  });

  it('loads a text artifact and exposes an English accessible content name', async () => {
    const fetchMock = vi.fn(async () => new Response('status: ready', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(
      <XScienceTextArtifactViewer filename='report.txt' contentUrl='/api/artifacts/text-1' kind='code' />,
      'en-US'
    );

    expect(await screen.findByLabelText('report.txt content')).toBeInTheDocument();
    expect(screen.getByLabelText('report.txt editor')).toHaveTextContent('status: ready');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/artifacts/text-1',
      expect.objectContaining({ credentials: 'same-origin' })
    );
  });

  it('preserves fetched JSON numeric tokens and whitespace in exact Source and Copy', async () => {
    const content =
      ' {\n "ns_before":1760000000000000000,"ns_after":1760000000000000001,"sample64":9007199254740993,"max_u64":18446744073709551615,"tiny":1e-400,"large":1e400,"finite":1.25,"safe":9007199254740991,"quoted":"9007199254740993","missing":null,"zero":-0\n}\n';
    const fetchMock = vi.fn(async () => new Response(content, { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const writeText = vi.fn(async (_content: string) => undefined);
    const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    vi.spyOn(Message, 'success').mockImplementation(() => undefined as never);
    const plan = resolveXScienceArtifactPreviewPlan({ filename: 'measurements.json', contentType: 'application/json' });
    expect(plan).toEqual({ type: 'code', fetchText: true, language: 'json' });
    try {
      await renderWithI18n(
        <XScienceTextArtifactViewer
          filename='measurements.json'
          contentUrl='/api/artifacts/json-1'
          kind='code'
          language={plan.language}
        />,
        'en-US'
      );
      expect((await screen.findByLabelText('measurements.json editor')).textContent).toBe(content);
      expect(screen.getByRole('tab', { name: 'Tree' })).toBeDisabled();
      expect(
        screen.getByText(
          'Some numeric values cannot be shown faithfully in Tree view. Source and Copy JSON preserve the exact original text.'
        )
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Copy JSON' }));
      await waitFor(() => expect(writeText).toHaveBeenCalledWith(content));
      expect(fetchMock).toHaveBeenCalledWith('/api/artifacts/json-1', {
        headers: { Accept: X_SCIENCE_TEXT_ACCEPT_HEADER },
        credentials: 'same-origin',
        signal: expect.any(AbortSignal),
      });
    } finally {
      if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard);
      else Reflect.deleteProperty(navigator, 'clipboard');
    }
  });

  it.each([' {"finite":1.25,"safe":9007199254740991,"quoted":"1e400","missing":null}\n', '{invalid\n'])(
    'preserves ordinary and invalid downloaded JSON source exactly',
    async (content) => {
      vi.stubGlobal(
        'fetch',
        vi.fn(async () => new Response(content, { status: 200 }))
      );
      await renderWithI18n(
        <XScienceTextArtifactViewer
          filename='source.json'
          contentUrl='/api/artifacts/json-control'
          kind='code'
          language='json'
        />,
        'en-US'
      );
      const sourceTab = await screen.findByRole('tab', { name: 'Source' });
      if (screen.getByRole('tab', { name: 'Tree' }).getAttribute('aria-selected') === 'true') {
        expect(screen.getByRole('tab', { name: 'Tree' })).not.toBeDisabled();
        fireEvent.click(sourceTab);
      }
      expect(screen.getByLabelText('source.json editor').textContent).toBe(content);
    }
  );

  it('keeps Source and Copy usable for a fetched wide scientific array', async () => {
    const content = `[${'0,'.repeat(149_999)}0]`;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(content, { status: 200 }))
    );
    const writeText = vi.fn(async (_content: string) => undefined);
    const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    vi.spyOn(Message, 'success').mockImplementation(() => undefined as never);
    try {
      await renderWithI18n(
        <XScienceTextArtifactViewer
          filename='counts.json'
          contentUrl='/api/artifacts/json-wide'
          kind='code'
          language='json'
        />,
        'en-US'
      );
      expect((await screen.findByLabelText('counts.json editor')).textContent).toBe(content);
      expect(screen.getByRole('tab', { name: 'Tree' })).toBeDisabled();
      expect(
        screen.getByText('The file contains more than 12,000 nodes. Source view is used to keep the page responsive.')
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Copy JSON' }));
      await waitFor(() => expect(writeText).toHaveBeenCalledWith(content));
    } finally {
      if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard);
      else Reflect.deleteProperty(navigator, 'clipboard');
    }
  });

  it('shows a stable localized request error and can retry successfully', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response('unavailable', { status: 503 }))
      .mockResolvedValueOnce(new Response('recovered', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(
      <XScienceTextArtifactViewer filename='report.txt' contentUrl='/api/artifacts/text-1' kind='code' />,
      'en-US'
    );

    expect(await screen.findByText('Failed to load file preview')).toBeInTheDocument();
    expect(screen.getByText('Failed to load file content (HTTP 503).')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('recovered')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('resolves strict immutable artifact version references to same-origin preview URLs', () => {
    const imageVersionId = 'de03acd4-6097-4fe9-a3af-f8fa47341850';
    const validationVersionId = 'f923cba6-0629-4b15-9cea-6a2a28fd210e';
    const markdown = [
      `![TOPSIS]({{artifact:${imageVersionId}}})`,
      `[validation.json]({{artifact:${validationVersionId}}})`,
      '![invalid]({{artifact:../../secrets}})',
      '[legacy]({{artifact:art_not_a_version}})',
    ].join('\n');

    const resolved = resolveMarkdownArtifactVersionReferences(markdown);

    expect(resolved).toContain(`![TOPSIS](/api/artifacts/versions/${imageVersionId})`);
    expect(resolved).toContain(`[validation.json](/api/artifacts/versions/${validationVersionId})`);
    expect(resolved).toContain('![invalid]({{artifact:../../secrets}})');
    expect(resolved).toContain('[legacy]({{artifact:art_not_a_version}})');
  });
});
