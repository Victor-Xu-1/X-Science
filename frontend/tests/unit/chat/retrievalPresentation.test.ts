import { describe, expect, it } from 'vitest';
import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import {
  buildToolStepPublicPresentation,
  buildToolStepGroupSummary,
  toolSourceUnavailable,
} from '@/renderer/pages/conversation/Messages/components/toolStepSummaryModel';
import { buildToolPublicDetailPresentation } from '@/renderer/pages/conversation/Messages/toolDetails/detailProjection';
import { hasNativeRetrievalReceipt } from '@/renderer/pages/conversation/Messages/toolActivityPresentationRegistry';

const tool = (input: object, output: object = {}): NormalizedToolCall => ({
  key: 'receipt',
  name: 'web_fetch',
  status: 'completed',
  input: JSON.stringify(input),
  output: JSON.stringify(output),
});

describe('retrieval presentation', () => {
  it.each(['mcp__inventory__fetch_record', 'mcp__external__web_fetch', 'fetch_report', 'get_document'])(
    'does not interpret arbitrary %s output as a native source receipt',
    (name) => {
      const item = {
        ...tool(
          { url: 'https://example.org/request' },
          {
            requestedUrl: 'https://example.org/other',
            sourceUnavailable: true,
            available: false,
            bytesRead: 0,
            complete: true,
          }
        ),
        name,
      };
      expect(toolSourceUnavailable(item)).toBe(false);
      expect(buildToolStepPublicPresentation(item, 'en-US').resultSummary).not.toMatch(
        /Source unavailable|Full text unavailable|Check source|Read 0 B/
      );
      expect(buildToolPublicDetailPresentation(item, 'en-US', null).notices).toEqual([]);
      expect(buildToolStepGroupSummary([item, item], 'en-US').headline).toBe('Retrieved 2 sources');
      expect(item.status).toBe('completed');
    }
  );
  it.each(['web_fetch', 'WebFetch', 'fetch_article_fulltext', 'download_public_scientific_file'])(
    'preserves native unavailable and abstract-record semantics for %s',
    (name) => {
      const unavailable = { ...tool({}, { available: false, sourceUnavailable: true }), name };
      const abstract = {
        ...tool({}, { available: false, recordAvailable: true, recordDepth: 'abstract_record' }),
        name,
      };
      expect(toolSourceUnavailable(unavailable)).toBe(true);
      expect(buildToolStepPublicPresentation(unavailable, 'en-US').resultSummary).toBe('Source unavailable');
      expect(buildToolPublicDetailPresentation(unavailable, 'en-US', null).notices).toHaveLength(1);
      expect(toolSourceUnavailable(abstract)).toBe(false);
      expect(buildToolStepPublicPresentation(abstract, 'en-US').resultSummary).toBe('Full text unavailable');
      expect(buildToolPublicDetailPresentation(abstract, 'en-US', null).notices).toEqual([]);
      expect(buildToolStepGroupSummary([unavailable, unavailable], 'en-US').headline).toBe('Source unavailable · 2');
      expect(buildToolStepGroupSummary([abstract, abstract], 'en-US').headline).toBe('Retrieved 2 sources');
      expect(unavailable.status).toBe('completed');
    }
  );
  it.each([
    'mcp__external__WebFetch',
    'mcp_external__WebFetch',
    'mcp__external__fetch_article_fulltext',
    'mcp__external__download_public_scientific_file',
    'webfetch',
    'WEBFETCH',
    ' WebFetch ',
  ])('does not grant native receipt authority to the alias lookalike %s', (name) => {
    const item = { ...tool({}, { available: false, sourceUnavailable: true }), name };
    expect(hasNativeRetrievalReceipt(name)).toBe(false);
    expect(toolSourceUnavailable(item)).toBe(false);
    expect(buildToolStepPublicPresentation(item, 'en-US').resultSummary).not.toBe('Source unavailable');
    expect(buildToolPublicDetailPresentation(item, 'en-US', null).notices).toEqual([]);
    expect(buildToolStepGroupSummary([item, item], 'en-US').headline).not.toContain('Source unavailable');
  });
  it('does not count unavailable sources as acquired material or mutate task status', () => {
    const unavailable = tool({}, { result: { sourceUnavailable: true, bytesRead: 0 } });
    const acquired = tool({}, { bytesRead: 50, complete: true });
    expect(toolSourceUnavailable(unavailable)).toBe(true);
    expect(toolSourceUnavailable(acquired)).toBe(false);
    expect(
      toolSourceUnavailable(tool({}, { available: false, recordAvailable: true, recordDepth: 'abstract_record' }))
    ).toBe(false);
    expect(buildToolStepGroupSummary([unavailable, unavailable], 'en-US').headline).toBe('Source unavailable · 2');
    expect(buildToolStepGroupSummary([unavailable, acquired], 'zh-CN').headline).toContain('来源不可用 · 1');
    expect(unavailable.status).toBe('completed');
  });
  it('shows a repairable runtime admission failure instead of an unexplained skipped step', () => {
    const item = {
      ...tool(
        {},
        {
          ok: false,
          executed: false,
          preflight: true,
          code: 'kernel_runtime_directories_unavailable',
          message: 'Runtime configuration requires repair.',
        }
      ),
      name: 'python',
      status: 'error' as const,
    };
    expect(buildToolStepPublicPresentation(item, 'en-US').resultSummary).toBe('Runtime needs repair');
    expect(buildToolStepPublicPresentation(item, 'zh-CN').resultSummary).toBe('运行环境需修复');
  });
  it.each([403, 404, 618, 0])(
    'shows native unavailable receipt %i without treating response bytes as evidence',
    (statusCode) => {
      const item = tool(
        { url: 'https://example.org/source' },
        {
          ok: true,
          result: {
            requestedUrl: 'https://example.org/source',
            url: 'https://example.org/source',
            sourceUnavailable: true,
            statusCode,
            bytesRead: statusCode === 0 ? 0 : 146,
            complete: statusCode !== 0,
            body: 'upstream error page',
          },
        }
      );
      expect(buildToolStepPublicPresentation(item, 'en-US').resultSummary).toBe('Source unavailable');
      expect(buildToolStepPublicPresentation(item, 'zh-CN').resultSummary).toBe('来源不可用');
      const detail = buildToolPublicDetailPresentation(item, 'en-US', null);
      expect(detail.notices).toContain(
        'This response is not usable source evidence. The task can retry or use another source.'
      );
      // Retrieval availability does not rewrite the transport or logical-task state.
      expect(item.status).toBe('completed');
    }
  );
  it.each([
    [{ bytesRead: 2048, complete: true }, '已读取 2.0 KB'],
    [{ bytesRead: 2048, complete: false }, '部分内容 · 2.0 KB'],
    [{ data: JSON.stringify({ bytesRead: 0, complete: true }) }, '已读取 0 B'],
    [{ sourceUnavailable: 'true', bytesRead: 1, complete: true }, '已读取 1 B'],
    [{ body: JSON.stringify({ sourceUnavailable: true }), bytesRead: 1, complete: true }, '已读取 1 B'],
  ])('reads typed receipt metadata only: %j', (receipt, summary) => {
    expect(buildToolStepPublicPresentation(tool({}, receipt), 'zh-CN').resultSummary).toBe(summary);
  });
  it('checks native requested identity and keeps unavailable evidence visible alongside a mismatch', () => {
    const item = tool(
      { url: 'https://example.org/a' },
      {
        ok: true,
        result: {
          requestedUrl: 'https://example.org/b',
          url: 'https://example.org/b',
          sourceUnavailable: true,
        },
      }
    );
    expect(buildToolStepPublicPresentation(item, 'en-US').resultSummary).toBe('Check source');
    expect(buildToolPublicDetailPresentation(item, 'en-US', null).notices).toHaveLength(2);
  });
  it('does not unwrap arbitrary downloaded content as a retrieval receipt', () => {
    const item = tool({}, { body: { result: { sourceUnavailable: true } } });
    expect(buildToolStepPublicPresentation(item, 'en-US').resultSummary).not.toBe('Source unavailable');
    expect(buildToolPublicDetailPresentation(item, 'en-US', null).notices).toEqual([]);
  });
  it('prefers explicit subjects and preserves distinguishable public URL paths', () => {
    expect(
      buildToolStepPublicPresentation(tool({ url: 'https://example.org/articles/123', title: '随访研究' }), 'zh-CN')
        .detail
    ).toBe('随访研究');
    expect(
      buildToolStepPublicPresentation(tool({ url: 'https://example.org/articles/123?token=private' }), 'zh-CN').detail
    ).toBe('example.org · articles/123');
  });
  it('flags contradictory requested sources without claiming a redirect or changing the result', () => {
    const item = tool(
      { url: 'https://example.org/article/123' },
      {
        requested_url: 'https://other.example/paper/456',
        url: 'https://other.example/paper/456',
        bytes_read: 2998,
        complete: true,
        content_type: 'text/html',
        body_sha256: 'abc123',
        body_hash_scope: 'returned_response_bytes',
        etag: 'opaque',
      }
    );
    const compact = buildToolStepPublicPresentation(item, 'zh-CN');
    expect(compact.resultSummary).toBe('来源待核对');
    const detail = buildToolPublicDetailPresentation(item, 'zh-CN', compact.resultSummary);
    expect(detail.notices).toContain('请求来源与结果回执不一致，请核对详情；不能据此视为同一来源。');
    expect(detail.resultRows).toContainEqual({ label: '已读取', value: '2.9 KB' });
    expect(detail.resultRows.some((row) => /Hash|Sha256|Etag|校验|缓存标识/.test(row.label))).toBe(false);
    expect(JSON.stringify(detail.outputTree)).toContain('缓存标识');
    expect(JSON.stringify(detail)).not.toContain('Body Hash Scope');
  });
  it('distinguishes partial responses, unavailable full text and a returned resource', () => {
    expect(buildToolStepPublicPresentation(tool({}, { bytes_read: 100, complete: false }), 'zh-CN').resultSummary).toBe(
      '部分内容 · 100 B'
    );
    expect(
      buildToolStepPublicPresentation(tool({}, { available: false, reason: 'no_open_access_full_text' }), 'zh-CN')
        .resultSummary
    ).toBe('全文不可用');
    expect(buildToolStepPublicPresentation(tool({}, { bytes_read: 0, complete: true }), 'zh-CN').resultSummary).toBe(
      '已读取 0 B'
    );
  });
  it('formats limits and sanitizes source URLs at every disclosure level', () => {
    const item = tool(
      { url: 'https://user:password@example.org/a?token=secret#token=secret', limit: 4194304 },
      { url: 'https://user:password@example.org/a?token=secret#token=secret', bytes_read: 1024 }
    );
    const detail = buildToolPublicDetailPresentation(item, 'zh-CN', null);
    expect(detail.inputRows).toContainEqual({ label: '读取上限', value: '4.0 MB' });
    expect(JSON.stringify(detail)).not.toContain('token=secret');
    expect(JSON.stringify(detail)).not.toContain('user:password');
  });
});
