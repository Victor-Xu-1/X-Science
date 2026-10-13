import React from 'react';
import { DecompressionStream as NativeDecompressionStream } from 'node:stream/web';
import { gzipSync } from 'node:zlib';
import { cleanup, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import SequenceViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceSequenceViewer';
import * as sequenceModel from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';
import { fixtureBytes, packetFixtures } from './snapgenePacketFixtures';

vi.mock('@arco-design/web-react', () => ({ Spin: () => <div>loading</div> }));
const seqVizMock = vi.hoisted(() => vi.fn());
vi.mock('seqviz', () => ({
  SeqViz: (props: Record<string, unknown>) => {
    seqVizMock(props);
    return <div data-testid='seqviz'>{String(props.name)}</div>;
  },
}));

const NativeResponse = globalThis.Response;

beforeEach(() => {
  seqVizMock.mockClear();
  vi.stubGlobal('DecompressionStream', NativeDecompressionStream);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('native SnapGene packet framing in raw and gzip previews', () => {
  it.each(
    packetFixtures.flatMap((fixture) =>
      [false, true].map((gzip) => ({ id: fixture.id, hex: fixture.hex, outcome: fixture.outcome, gzip }))
    )
  )('handles $id (gzip=$gzip) without a partial scientific document', async (fixture) => {
    const bytes = fixtureBytes(fixture);
    const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
    const response = new NativeResponse(fixture.gzip ? gzipSync(bytes) : bytes);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response)
    );
    const filename = fixture.gzip ? 'SynthETIC.DNA.GZ' : 'SynthETIC.DNA';
    await renderWithI18n(<SequenceViewer filename={filename} contentUrl='/test/synthetic/content' />, 'en-US');

    if (fixture.outcome === 'sequence') {
      expect(await screen.findByText('8 bp · 0 annotations')).toBeInTheDocument();
      await waitFor(() =>
        expect(seqVizMock).toHaveBeenCalledWith(
          expect.objectContaining({ name: 'SynthETIC', seq: 'ACGTTGCA', seqType: 'dna', annotations: [] })
        )
      );
      expect(screen.queryByText('Failed to parse sequence file.')).toBeNull();
    } else {
      const error = fixture.outcome === 'empty-content' ? 'sequence file is empty.' : 'Failed to parse sequence file.';
      expect(await screen.findByText(error)).toBeInTheDocument();
      expect(seqVizMock).not.toHaveBeenCalled();
      expect(screen.queryByTestId('x-science-sequence-host')).toBeNull();
      expect(screen.queryByText(/\d+ bp · \d+ annotations/)).toBeNull();
    }

    expect(parse).toHaveBeenCalledOnce();
    expect(parse.mock.calls[0][1]).toBe(filename);
    expect(new Uint8Array(parse.mock.calls[0][0] as ArrayBuffer)).toEqual(Uint8Array.from(bytes));
    await waitFor(() => expect(response.body?.locked).toBe(false));
    expect(response.bodyUsed).toBe(true);
  });
});
