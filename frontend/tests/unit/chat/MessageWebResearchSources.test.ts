import { describe, expect, it } from 'vitest';
import {
  extractResearchSourcePresentation,
  isResearchActivityTool,
} from '@/renderer/pages/conversation/Messages/components/researchSourcePresentation';
import {
  observeResearchReturnClaim,
  researchSourceState,
  type ResearchSourceShape,
} from '@/renderer/pages/conversation/Messages/components/researchSourceShape';

describe('MessageWebResearchSources model', () => {
  it('recognizes explicit zero returned counts without accepting invalid or conflicting counters', () => {
    for (const key of [
      'n_records_returned',
      'rows_retrieved',
      'n_retrieved',
      'n_returned',
      'results_returned',
      'returnedResults',
      'retrieved',
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify({ [key]: 0 }))).toMatchObject({
        state: 'empty',
        results: [],
      });
      for (const value of [-1, NaN, Infinity, -Infinity, '0', null, 1]) {
        const shape: ResearchSourceShape = {
          collectionSeen: false,
          nonemptyCollection: false,
          positiveReturnedClaim: false,
        };
        observeResearchReturnClaim({ [key]: value }, shape);
        expect(researchSourceState(shape, 0)).toBe('unrecognized');
      }
    }
    for (const value of [
      { n_records_returned: 0, rows_retrieved: 1 },
      { n_records_returned: 1, rows_retrieved: 0 },
      { n_records_returned: 0, records: [{ unsupported: 'record' }] },
      { api_total: 0 },
      { n_cids_total: 0 },
      { total: 0 },
      { results: 0 },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value)).state).toBe('unrecognized');
    }
    for (const value of [
      { result: JSON.stringify({ n_records_returned: 0 }) },
      { content: [{ type: 'text', text: JSON.stringify({ rows_retrieved: 0 }) }] },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value)).state).toBe('empty');
    }
  });

  it('recognizes decoded empty result arrays without treating empty transport content as results', () => {
    for (const value of [
      { result: '[]' },
      { result: JSON.stringify('[]') },
      { results: '[]' },
      { content: [{ type: 'text', text: '[]' }] },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value))).toMatchObject({
        state: 'empty',
        results: [],
      });
    }
    for (const value of [
      { content: [] },
      { content: '[]' },
      { content: [{ type: 'text', text: 'not structured data' }] },
      { result: '[{"unsupported":"record"}]' },
      { retrieved: 1, result: '[]' },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value)).state).toBe('unrecognized');
    }
  });

  it('projects compound identifiers and properties once, preserving record titles and actual page size', () => {
    const presentation = extractResearchSourcePresentation(
      undefined,
      JSON.stringify({
        query: 'public fixture',
        namespace: 'name',
        n_cids_total: 900,
        truncated: true,
        cids: [12345, 67890, 12345],
        properties: [
          { CID: 12345, title: 'First compound' },
          { CID: 67890, IUPACName: 'Second compound' },
        ],
      })
    );
    expect(presentation.state).toBe('available');
    expect(presentation.results).toHaveLength(2);
    expect(presentation.results.map((source) => [source.title, source.url])).toEqual([
      ['First compound', 'https://pubchem.ncbi.nlm.nih.gov/compound/12345'],
      ['Second compound', 'https://pubchem.ncbi.nlm.nih.gov/compound/67890'],
    ]);
  });

  it('supports identifier-only, property-only and nested MCP compound results', () => {
    for (const value of [
      { cids: [12345], properties: [] },
      { properties: [{ CID: 12345 }] },
      { content: [{ type: 'text', text: JSON.stringify({ cids: ['12345'], properties: [{ CID: 12345 }] }) }] },
      { ok: true, result: JSON.stringify({ cids: [12345] }) },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value))).toMatchObject({
        state: 'available',
        results: [{ url: 'https://pubchem.ncbi.nlm.nih.gov/compound/12345' }],
      });
    }
  });

  it('rejects invalid compound identities rather than inventing links or declaring empty results', () => {
    const presentation = extractResearchSourcePresentation(
      undefined,
      JSON.stringify({
        cids: [0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, '1/../../private', 'https://localhost/private', '01'],
        properties: [{ CID: 'private value' }],
      })
    );
    expect(presentation.results).toEqual([]);
    expect(presentation.state).toBe('unrecognized');
  });

  it('distinguishes actual empty collections from unsupported nonempty connector output', () => {
    for (const value of [
      { cids: [], properties: [], n_cids_total: 0 },
      { results: [] },
      [],
      { result: [] },
      { result: JSON.stringify({ records: [] }) },
      { content: [{ type: 'text', text: JSON.stringify({ cids: [] }) }] },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value)).state).toBe('empty');
    }
    for (const value of [
      { custom_data: ['record'] },
      { records: [{ unsupported: 'record' }] },
      { retrieved: 8, records: [] },
      { properties: [{ CID: -1 }] },
    ]) {
      expect(extractResearchSourcePresentation(undefined, JSON.stringify(value)).state).toBe('unrecognized');
    }
  });
  it.each(['web_search', 'WebSearch', 'WebResearch', 'web_research'])('recognizes %s as a research tool', (name) => {
    expect(isResearchActivityTool(name)).toBe(true);
  });

  it.each(['web_fetch', 'fetch_article_fulltext', 'download_public_scientific_file'])(
    'keeps %s in the retrieval-detail projection instead of a search-result card',
    (name) => {
      expect(isResearchActivityTool(name)).toBe(false);
    }
  );

  it('extracts only safe HTTP sources, removes duplicates and respects the limit', () => {
    const output = [
      'https://example.org/a, https://example.org/a',
      'https://pubmed.ncbi.nlm.nih.gov/17187687/',
      'javascript:alert(1)',
      'file:///etc/passwd',
      'https://rcsb.org/structure/8OIZ',
    ].join('\n');

    expect(extractResearchSourcePresentation(undefined, output, 2)).toEqual({
      query: null,
      state: 'available',
      results: [
        {
          key: 'https://example.org/a',
          title: 'example.org/a',
          url: 'https://example.org/a',
          source: 'https://example.org/a',
          snippet: null,
        },
        {
          key: 'https://pubmed.ncbi.nlm.nih.gov/17187687/',
          title: 'pubmed.ncbi.nlm.nih.gov/17187687',
          url: 'https://pubmed.ncbi.nlm.nih.gov/17187687/',
          source: 'https://pubmed.ncbi.nlm.nih.gov/17187687/',
          snippet: null,
        },
      ],
    });
  });

  it('does not render loopback, private-network, credential-bearing, or local-host research links', () => {
    const presentation = extractResearchSourcePresentation(
      undefined,
      [
        'http://127.0.0.1/private',
        'http://192.168.1.10/source',
        'https://user:password@example.org/private',
        'https://example.org/signed?access_token=secret-value',
        'https://example.org/signed?X-Amz-Signature=abcdef',
        'http://[::ffff:127.0.0.1]/private',
        'http://evidence.local/study',
        'https://example.org/public',
      ].join('\n')
    );

    expect(presentation.results).toEqual([expect.objectContaining({ url: 'https://example.org/public' })]);
  });

  it('does not reinterpret structured numeric counters as PDB identifiers', () => {
    const presentation = extractResearchSourcePresentation(
      undefined,
      JSON.stringify({
        api_total: 4821,
        records: [{ id: 'one' }, { id: 'two' }],
      })
    );

    expect(presentation.results).toHaveLength(2);
    expect(presentation.results.map((result) => result.title)).toEqual(['one', 'two']);
    expect(presentation.results.some((result) => result.url?.includes('/structure/4821'))).toBe(false);
  });

  it('does not inflate structured source collections from serialized diagnostic URLs', () => {
    const presentation = extractResearchSourcePresentation(
      undefined,
      JSON.stringify({
        sources: [
          { title: 'Primary study', url: 'https://example.org/study' },
          {
            title: 'Independent study',
            url: 'https://example.org/independent',
          },
        ],
        diagnostics: {
          providerEndpoint: 'https://search.example.org/query',
          debugReferences: ['https://example.org/not-a-returned-source'],
        },
      })
    );

    expect(presentation.results.map((result) => result.url)).toEqual([
      'https://example.org/study',
      'https://example.org/independent',
    ]);
  });

  it('prefers canonical sources over raw provider candidates', () => {
    const presentation = extractResearchSourcePresentation(
      undefined,
      JSON.stringify({
        result: {
          results: [
            {
              title: 'Raw provider hit',
              url: 'https://provider.example.org/raw',
            },
            {
              title: 'Another raw hit',
              url: 'https://provider.example.org/other',
            },
          ],
          sources: [
            {
              title: 'Ranked source',
              url: 'https://evidence.example.org/ranked',
            },
          ],
        },
      })
    );

    expect(presentation.results.map((result) => result.url)).toEqual(['https://evidence.example.org/ranked']);
  });

  it('renders scientific database records instead of treating nested identifiers as separate search hits', () => {
    const presentation = extractResearchSourcePresentation(
      JSON.stringify({ gene_symbol: 'TYK2' }),
      JSON.stringify({
        count: 2,
        targets: [
          {
            target_chembl_id: 'CHEMBL3553',
            pref_name: 'Non-receptor tyrosine-protein kinase TYK2',
            components: [{ target_component_xrefs: [{ xref_id: '8TB6' }, { xref_id: '8S9A' }] }],
          },
          { target_chembl_id: 'CHEMBL2363062', pref_name: 'Janus kinase family' },
        ],
      })
    );

    expect(presentation.results).toEqual([
      {
        key: 'CHEMBL3553:Non-receptor tyrosine-protein kinase TYK2',
        title: 'Non-receptor tyrosine-protein kinase TYK2',
        url: null,
        source: 'CHEMBL3553',
        snippet: null,
      },
      {
        key: 'CHEMBL2363062:Janus kinase family',
        title: 'Janus kinase family',
        url: null,
        source: 'CHEMBL2363062',
        snippet: null,
      },
    ]);
  });

  it('preserves every clinical-trial identity when several records share the same title', () => {
    const trials = Array.from({ length: 20 }, (_, index) => ({
      nct_id: `NCT${String(index + 1).padStart(8, '0')}`,
      title: 'A shared study title that must not become the record identity',
      condition: 'Solid tumor',
    }));
    const presentation = extractResearchSourcePresentation(
      JSON.stringify({ condition: 'Solid tumor' }),
      JSON.stringify({ trials })
    );

    expect(presentation.query).toBe('Solid tumor');
    expect(presentation.results).toHaveLength(20);
    expect(new Set(presentation.results.map((result) => result.key))).toHaveLength(20);
    expect(presentation.results[0]).toMatchObject({
      source: 'NCT00000001',
      url: null,
    });
  });

  it('links only explicitly typed publication identifiers instead of guessing from a generic numeric id', () => {
    const presentation = extractResearchSourcePresentation(
      JSON.stringify({ query: 'kinase evidence' }),
      JSON.stringify({
        records: [
          { id: '123456', title: 'Generic database record' },
          { pmid: '41010003', title: 'Typed PubMed record' },
        ],
      })
    );

    expect(presentation.results).toHaveLength(2);
    expect(presentation.results[0]).toMatchObject({ source: '123456', url: null });
    expect(presentation.results[1]).toMatchObject({
      source: '41010003',
      url: 'https://pubmed.ncbi.nlm.nih.gov/41010003/',
    });
  });
});
