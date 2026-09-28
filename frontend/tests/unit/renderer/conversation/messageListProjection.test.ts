import type { TMessage } from '@/common/chat/chatLib';
import type { IConversationArtifact } from '@/common/adapter/ipcBridge';
import { buildMessagePresentationList } from '@/renderer/pages/conversation/Messages/messageListProjection';
import { describe, expect, it } from 'vitest';

const tool = (id: string, name: string, createdAt: number): TMessage =>
  ({
    id,
    type: 'tool_call',
    position: 'left',
    created_at: createdAt,
    content: {
      call_id: id,
      name,
      args: {},
      output: JSON.stringify({ ok: true }),
      status: 'completed',
    },
  }) as unknown as TMessage;

const text = (id: string, content: string, createdAt: number): TMessage =>
  ({
    id,
    type: 'text',
    position: 'left',
    created_at: createdAt,
    content: { content },
  }) as unknown as TMessage;

describe('message list projection controller', () => {
  const produced = (artifactId: string, versionId: string) => ({
    artifact_id: artifactId,
    version_id: versionId,
    relation: 'produced' as const,
    availability: 'available' as const,
  });
  const inventory = () =>
    [
      {
        kind: 'scientific_files',
        status: 'active',
        payload: {
          files: [produced('a', 'v1'), produced('a', 'v2'), produced('b', 'vb')].map((ref) =>
            Object.assign(ref, {
              filename: 'same-name.csv',
              content_url: `/api/artifacts/${ref.artifact_id}/versions/${ref.version_id}`,
              is_intermediate: false,
            })
          ),
        },
      },
    ] as unknown as IConversationArtifact[];

  it('coalesces round versions without merging distinct artifacts that share a filename', () => {
    const final = {
      ...text('final', 'Saved files.', 3),
      terminal_status: 'completed',
      artifact_refs: [produced('a', 'v1'), produced('a', 'v2'), produced('b', 'vb'), produced('b', 'vb')],
    } as TMessage;
    const before = JSON.stringify(final);
    const rows = buildMessagePresentationList([final], inventory());
    expect(rows.filter((row) => row.type === 'referenced_files')).toMatchObject([
      {
        sourceMessageIds: ['final'],
        files: [
          { artifact_id: 'a', version_id: 'v2' },
          { artifact_id: 'b', version_id: 'vb' },
        ],
      },
    ]);
    expect(JSON.stringify(final)).toBe(before);
  });

  it('keeps terminal-only pages stable when generating tools are loaded later', () => {
    const final = {
      ...text('final', 'Saved.', 3),
      terminal_status: 'completed',
      artifact_refs: [produced('a', 'v2')],
    } as TMessage;
    const oldTool = {
      ...tool('old-save', 'save_artifacts', 1),
      artifact_refs: [produced('a', 'v1')],
    } as TMessage;
    const files = (messages: TMessage[]) =>
      buildMessagePresentationList(messages, inventory()).filter((row) => row.type === 'referenced_files');
    expect(files([oldTool, final])).toEqual(files([final]));
  });

  it('delivers after an empty completed answer without inserting an empty message row', () => {
    const final = {
      ...text('empty-final', '', 3),
      terminal_status: 'completed',
      artifact_refs: [produced('a', 'v2')],
    } as TMessage;
    const rows = buildMessagePresentationList([final], inventory());
    expect(rows).toMatchObject([{ type: 'referenced_files', sourceMessageIds: ['empty-final'] }]);
    expect(rows).toHaveLength(1);
  });

  it.each([
    { status: 'finish' },
    { status: 'error', terminal_status: 'failed' },
    { status: 'finish', terminal_status: 'cancelled' },
    {
      status: 'finish',
      terminal_status: 'completed',
      terminal_superseded: true,
    },
  ])('does not deliver an unfinished or superseded round: %j', (state) => {
    const message = {
      ...text('not-final', 'Progress retained.', 1),
      ...state,
      artifact_refs: [produced('a', 'v1')],
    } as TMessage;
    expect(buildMessagePresentationList([message], inventory()).map((row) => row.type)).toEqual(['text']);
  });

  it('delivers produced files once after the completed round, not at intermediate saves', () => {
    const ref = {
      artifact_id: 'a',
      version_id: 'v1',
      relation: 'produced',
      availability: 'available',
    };
    const origin = {
      ...tool('save', 'save_artifacts', 1),
      artifact_refs: [ref],
    } as TMessage;
    const summary = {
      ...text('summary', 'Complete.', 3),
      terminal_status: 'completed',
      artifact_refs: [ref],
    } as TMessage;
    const artifacts = [
      {
        kind: 'scientific_files',
        status: 'active',
        payload: {
          files: [
            {
              artifact_id: 'a',
              version_id: 'v1',
              filename: 'result.csv',
              content_url: '/api/artifacts/a/versions/v1',
              is_intermediate: false,
            },
          ],
        },
      },
    ] as unknown as IConversationArtifact[];
    const rows = buildMessagePresentationList([origin, tool('later', 'read_file', 2), summary], artifacts);
    const files = rows.filter((row) => row.type === 'referenced_files');
    expect(files).toHaveLength(1);
    expect(files[0]).toMatchObject({
      sourceMessageIds: ['summary'],
      files: [{ version_id: 'v1' }],
    });
    expect(rows.findIndex((row) => row.type === 'referenced_files')).toBe(
      rows.findIndex((row) => row.id === 'summary') + 1
    );
    expect(
      buildMessagePresentationList([origin, { ...summary, terminal_status: undefined }], artifacts).filter(
        (row) => row.type === 'referenced_files'
      )
    ).toEqual([]);
    expect(buildMessagePresentationList([origin, tool('later', 'read_file', 2), summary], artifacts)).toEqual(rows);
  });

  it('omits orphan assistant delimiters without hiding user input or scientific symbols', () => {
    const user = { ...text('user', '></', 1), position: 'right' } as TMessage;
    const projected = buildMessagePresentationList(
      [text('orphan', '></', 0), user, text('formula', 'p < 0.05', 2), text('symbol', '∞', 3)],
      [],
      []
    );
    expect(projected.map((item) => item.id)).toEqual(['user', 'formula', 'symbol']);
  });

  it('groups adjacent tool activity and honors durable page boundaries', () => {
    const first = buildMessagePresentationList(
      [tool('tool-1', 'web_search', 1), tool('tool-2', 'web_fetch', 2)],
      [],
      []
    );
    expect(first).toHaveLength(1);
    expect(first[0].type).toBe('tool_summary');
    expect(first[0]).toMatchObject({ sourceMessageIds: ['tool-1', 'tool-2'] });

    const split = buildMessagePresentationList(
      [tool('tool-1', 'web_search', 1), tool('tool-2', 'web_fetch', 2)],
      [],
      ['tool-2']
    );
    expect(split.map((item) => item.type)).toEqual(['tool_summary', 'tool_summary']);
  });

  it('splits adjacent tool-only history at a semantic phase change', () => {
    const projected = buildMessagePresentationList(
      [tool('search-1', 'web_search', 1), tool('fetch-1', 'web_fetch', 2), tool('analysis-1', 'python', 3)],
      [],
      []
    );
    expect(projected.map((item) => item.type)).toEqual(['tool_summary', 'tool_summary']);
    expect(projected[0]).toMatchObject({
      sourceMessageIds: ['search-1', 'fetch-1'],
    });
    expect(projected[1]).toMatchObject({ sourceMessageIds: ['analysis-1'] });
  });

  it('keeps real public progress prose between tool groups', () => {
    const projected = buildMessagePresentationList(
      [
        tool('search-1', 'web_search', 1),
        text('progress-1', '已经找到候选资料，接下来核对完整证据。', 2),
        tool('fetch-1', 'web_fetch', 3),
      ],
      [],
      []
    );
    expect(projected.map((item) => item.type)).toEqual(['tool_summary', 'text', 'tool_summary']);
    expect(projected[1]).toMatchObject({ id: 'progress-1' });
  });

  it('treats an empty assistant segment as a durable tool-group boundary', () => {
    const projected = buildMessagePresentationList(
      [tool('search-1', 'web_search', 1), text('boundary', '   ', 2), tool('search-2', 'web_search', 3)],
      [],
      []
    );
    expect(projected.map((item) => item.type)).toEqual(['tool_summary', 'tool_summary']);
  });

  it('does not project hidden transport rows', () => {
    const hidden = {
      id: 'hidden',
      type: 'text',
      position: 'left',
      created_at: 1,
      hidden: true,
      content: { content: 'internal transport row' },
    } as unknown as TMessage;
    expect(buildMessagePresentationList([hidden], [], [])).toEqual([]);
  });
});
