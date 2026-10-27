import { describe, expect, it } from 'vitest';
import {
  resolveXScienceMcpDescription,
  resolveXScienceMcpToolDescription,
} from '@/renderer/services/mcp/xScienceMcpDescriptions';

describe('X-Science MCP descriptions', () => {
  it.each(['x-science-research', 'open-targets-official', 'tamarind-bio', 'adaptyv-cloud-lab', 'idc-rest'])(
    'localizes the installed %s connector',
    (name) => {
      expect(resolveXScienceMcpDescription(name, 'English fallback.', 'zh-CN')).toMatch(/[\u4e00-\u9fff]/);
    }
  );
  it('uses the biomedical Chinese copy for the Chinese interface', () => {
    expect(resolveXScienceMcpDescription('pubmed', 'PubMed biomedical literature.', 'zh-CN')).toContain(
      'PubMed 生物医学文献'
    );
  });

  it('keeps the backend English description in the English interface', () => {
    expect(resolveXScienceMcpDescription('pubmed', 'Biomedical literature search and metadata.', 'en-US')).toBe(
      'Biomedical literature search and metadata.'
    );
  });

  it('prefers an API-provided localized description', () => {
    expect(resolveXScienceMcpDescription('pubmed', 'Fallback', 'zh-CN', { 'zh-CN': '本地化 PubMed 说明。' })).toBe(
      '本地化 PubMed 说明。'
    );
  });
});

describe('bundled PDB method descriptions', () => {
  const server = { id: 'bundled:structures-interactions', source: 'bundled' };
  it.each([
    ['pdb_search_sequence', 'en-US', 'sequence identity and query coverage'],
    ['pdb_search_sequence', 'zh-CN', '序列一致性和查询覆盖度'],
    ['pdb_search_structure_similarity', 'en-US', 'deposited chain'],
    ['pdb_search_structure_similarity', 'zh-CN', '已收录的链'],
  ])('describes %s in %s', (method, language, expected) => {
    expect(resolveXScienceMcpToolDescription(server, method, 'Original description.', language)).toContain(expected);
  });
  it.each([undefined, 'de-DE'])('uses English for missing or unsupported language: %s', (language) => {
    expect(resolveXScienceMcpToolDescription(server, 'pdb_search_sequence', 'Original.', language)).toContain(
      'protein sequence'
    );
  });
  it.each([
    { id: 'custom:structures-interactions', source: 'bundled' },
    { id: 'bundled:structures-interactions', source: 'custom' },
    { id: 'bundled:other', source: 'bundled' },
  ])('preserves descriptions outside the exact built-in identity: %j', (other) => {
    expect(resolveXScienceMcpToolDescription(other, 'pdb_search_sequence', 'User-defined description.', 'zh-CN')).toBe(
      'User-defined description.'
    );
  });
  it('preserves unknown tool descriptions including a missing description', () => {
    expect(resolveXScienceMcpToolDescription(server, 'unknown_method', 'Provider description.', 'zh-CN')).toBe(
      'Provider description.'
    );
    expect(resolveXScienceMcpToolDescription(server, 'unknown_method', undefined, 'en-US')).toBe('');
  });
});
