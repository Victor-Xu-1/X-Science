import { readFileSync } from 'node:fs';
import {
  composeMessage,
  transformMessage,
  type IMessageToolCall,
  type IMessageToolGroup,
  type TMessage,
} from '@/common/chat/chatLib';
import { normalizeToolMessages } from '@/common/chat/normalizeToolCall';
import { mergeLoadedPageWithCurrent } from '@/renderer/pages/conversation/Messages/messageWindowReconciliation';
import { buildMessagePresentationList } from '@/renderer/pages/conversation/Messages/messageListProjection';
import { describe, expect, it } from 'vitest';
import {
  collapseNestedConnectorDispatches,
  startsNewToolSummary,
} from '@/renderer/pages/conversation/Messages/toolSummaryGroupingModel';
import {
  canGroupToolActivitySequence,
  canGroupToolActivities,
  getToolPublicDetailKind,
  getToolPublicPresentationPolicy,
  isPublicToolActivity,
} from '@/renderer/pages/conversation/Messages/toolActivityPresentationRegistry';

const toolCall = (id: string, name = 'python'): IMessageToolCall =>
  ({
    id,
    type: 'tool_call',
    conversation_id: 'conversation-1',
    content: {
      call_id: id,
      name,
      status: 'completed',
    },
  }) as IMessageToolCall;

const toolGroup = (id: string, name = 'python'): IMessageToolGroup =>
  ({
    id,
    type: 'tool_group',
    conversation_id: 'conversation-1',
    content: [
      {
        call_id: id,
        name,
        status: 'Success',
      },
    ],
  }) as IMessageToolGroup;

describe('tool summary grouping boundaries', () => {
  it('keeps one typed presentation policy for every public tool family', () => {
    expect(
      [
        ['generate_plan', 'plan'],
        ['web_search', 'research'],
        ['web_fetch', 'retrieval'],
        ['mcp__pubmed__get_article_details', 'retrieval'],
        ['read_file', 'inspection'],
        ['python', 'analysis'],
        ['powershell', 'analysis'],
        ['skill', 'method'],
        ['manage_environments', 'environment'],
        ['list_compute', 'compute'],
        ['search_memory', 'memory'],
        ['request_network_access', 'access'],
        ['edit_file', 'file'],
        ['save_artifacts', 'artifact'],
        ['mcp__chemistry__dock_ligands', 'analysis'],
        ['mcp__structures__binding_mode_analysis', 'analysis'],
        ['mcp__chemistry__bindingdb_ligands_by_target', 'inspection'],
        ['mcp__human-genetics__eqtl_associations', 'inspection'],
        ['mcp__variants__gene_variants', 'inspection'],
        ['mcp__structures-interactions__pdb_select_latest_liganded_structure', 'inspection'],
        ['unknown_operation', 'generic'],
      ].map(([name]) => [name, getToolPublicDetailKind(name)])
    ).toEqual([
      ['generate_plan', 'plan'],
      ['web_search', 'research'],
      ['web_fetch', 'retrieval'],
      ['mcp__pubmed__get_article_details', 'retrieval'],
      ['read_file', 'inspection'],
      ['python', 'analysis'],
      ['powershell', 'analysis'],
      ['skill', 'method'],
      ['manage_environments', 'environment'],
      ['list_compute', 'compute'],
      ['search_memory', 'memory'],
      ['request_network_access', 'access'],
      ['edit_file', 'file'],
      ['save_artifacts', 'artifact'],
      ['mcp__chemistry__dock_ligands', 'analysis'],
      ['mcp__structures__binding_mode_analysis', 'analysis'],
      ['mcp__chemistry__bindingdb_ligands_by_target', 'inspection'],
      ['mcp__human-genetics__eqtl_associations', 'inspection'],
      ['mcp__variants__gene_variants', 'inspection'],
      ['mcp__structures-interactions__pdb_select_latest_liganded_structure', 'inspection'],
      ['unknown_operation', 'generic'],
    ]);

    expect(getToolPublicPresentationPolicy('generate_plan')).toEqual({
      detailKind: 'plan',
      showIdentity: false,
      showGenericOutput: false,
      inputBlockMode: 'inline',
      collectionsInitiallyExpanded: false,
    });
    expect(getToolPublicPresentationPolicy('skill')).toEqual({
      detailKind: 'method',
      showIdentity: true,
      showGenericOutput: false,
      inputBlockMode: 'inline',
      collectionsInitiallyExpanded: false,
    });
    expect(getToolPublicPresentationPolicy('python')).toEqual({
      detailKind: 'analysis',
      showIdentity: true,
      showGenericOutput: true,
      inputBlockMode: 'inline',
      collectionsInitiallyExpanded: false,
    });
    expect(getToolPublicPresentationPolicy('request_network_access')).toEqual({
      detailKind: 'access',
      showIdentity: true,
      showGenericOutput: true,
      inputBlockMode: 'inline',
      collectionsInitiallyExpanded: false,
    });
    expect(getToolPublicPresentationPolicy('edit_file')).toEqual({
      detailKind: 'file',
      showIdentity: true,
      showGenericOutput: true,
      inputBlockMode: 'disclosure',
      collectionsInitiallyExpanded: false,
    });
  });

  it('assigns every model root tool to one public type or one dedicated status surface', () => {
    const expectedRootPresentation: Record<string, string> = {
      ask_about_compute: 'compute',
      ask_user: 'hidden',
      bash: 'analysis',
      compute_details: 'compute',
      compute_provider: 'compute',
      delete_host_files: 'file',
      download_public_scientific_file: 'retrieval',
      edit_file: 'file',
      fetch_article_fulltext: 'retrieval',
      generate_plan: 'plan',
      list_compute: 'compute',
      list_host_grants: 'access',
      manage_environments: 'environment',
      manage_packages: 'environment',
      powershell: 'analysis',
      python: 'analysis',
      r: 'analysis',
      read_file: 'inspection',
      read_memory: 'memory',
      repl: 'analysis',
      request_host_access: 'access',
      request_network_access: 'access',
      save_artifacts: 'artifact',
      search_memory: 'memory',
      search_skills: 'method',
      skill: 'method',
      update_step_status: 'hidden',
      wait_for_notification: 'hidden',
      web_fetch: 'retrieval',
      web_search: 'research',
      write_memory: 'memory',
    };

    expect(
      Object.fromEntries(
        Object.keys(expectedRootPresentation).map((name) => [
          name,
          isPublicToolActivity(name) ? getToolPublicDetailKind(name) : 'hidden',
        ])
      )
    ).toEqual(expectedRootPresentation);
  });

  it('assigns every bundled biomedical MCP method to a typed public detail instead of the generic fallback', () => {
    const domains = JSON.parse(
      readFileSync(
        new URL('../../../../assets/optional/mcp-servers/bio-tools/lib/mcp_bio/domains.json', import.meta.url),
        'utf8'
      )
    ) as Record<string, string[]>;
    const genericMethods = Object.entries(domains).flatMap(([domain, methods]) =>
      methods
        .filter((method) => getToolPublicDetailKind(`mcp__${domain}__${method}`) === 'generic')
        .map((method) => `${domain}/${method}`)
    );

    expect(genericMethods).toEqual([]);
  });

  it('keeps the first tool message in the current summary', () => {
    expect(startsNewToolSummary([], toolGroup('group-1'))).toBe(false);
  });

  it('keeps consecutive calls in the same public work phase together', () => {
    expect(startsNewToolSummary([toolCall('call-1', 'web_search')], toolCall('call-2', 'web_fetch'))).toBe(false);
    expect(startsNewToolSummary([toolCall('call-1', 'skill')], toolCall('call-2', 'manage_environments'))).toBe(false);
    expect(startsNewToolSummary([toolCall('call-1', 'read_file')], toolCall('call-2', 'python'))).toBe(false);
    expect(startsNewToolSummary([toolCall('call-1', 'python')], toolCall('call-2', 'save_artifacts'))).toBe(false);
  });

  it('starts a new summary when the public work phase changes', () => {
    expect(startsNewToolSummary([toolGroup('group-1', 'web_search')], toolGroup('group-2', 'python'))).toBe(true);
    expect(startsNewToolSummary([toolCall('call-1', 'generate_plan')], toolGroup('group-2', 'python'))).toBe(true);
    expect(startsNewToolSummary([toolCall('call-1', 'python')], toolGroup('group-2', 'generate_plan'))).toBe(true);
  });

  it('does not let an ambiguous inspection row bridge research into execution', () => {
    expect(canGroupToolActivitySequence(['web_search', 'read_file', 'python'])).toBe(false);
    expect(
      startsNewToolSummary(
        [toolCall('call-1', 'web_search'), toolCall('call-2', 'read_file')],
        toolCall('call-3', 'python')
      )
    ).toBe(true);
    expect(canGroupToolActivitySequence(['skill', 'manage_environments', 'list_compute'])).toBe(true);
  });

  it('classifies every child in a protocol tool group instead of trusting only its first child', () => {
    const mixedResearchAndExecution = {
      ...toolGroup('group-mixed', 'web_fetch'),
      content: [
        { call_id: 'fetch', name: 'web_fetch', status: 'Success' },
        { call_id: 'compute', name: 'python', status: 'Success' },
      ],
    } as IMessageToolGroup;
    const researchOnly = {
      ...toolGroup('group-research', 'web_fetch'),
      content: [
        { call_id: 'fetch', name: 'web_fetch', status: 'Success' },
        { call_id: 'inspect', name: 'read_file', status: 'Success' },
      ],
    } as IMessageToolGroup;

    expect(startsNewToolSummary([toolCall('search', 'web_search')], mixedResearchAndExecution)).toBe(true);
    expect(startsNewToolSummary([toolCall('search', 'web_search')], researchOnly)).toBe(false);
  });

  it('merges only operations that belong to one coherent public stage', () => {
    expect(canGroupToolActivities('web_search', 'web_fetch')).toBe(true);
    expect(canGroupToolActivities('web_fetch', 'read_file')).toBe(true);
    expect(canGroupToolActivities('skill', 'manage_environments')).toBe(true);
    expect(canGroupToolActivities('python', 'save_artifacts')).toBe(true);
    expect(canGroupToolActivities('read_file', 'python')).toBe(true);
    expect(canGroupToolActivities('manage_environments', 'list_compute')).toBe(true);
    expect(canGroupToolActivities('compute_provider', 'python')).toBe(true);
    expect(canGroupToolActivities('search_memory', 'web_search')).toBe(true);
    expect(canGroupToolActivities('write_memory', 'save_artifacts')).toBe(true);
    expect(canGroupToolActivities('request_network_access', 'manage_environments')).toBe(true);

    expect(canGroupToolActivities('web_search', 'python')).toBe(false);
    expect(canGroupToolActivities('manage_environments', 'web_search')).toBe(false);
    expect(canGroupToolActivities('request_network_access', 'python')).toBe(false);
    expect(canGroupToolActivities('generate_plan', 'skill')).toBe(false);
    expect(canGroupToolActivities('save_artifacts', 'generate_plan')).toBe(false);
  });

  it('collapses a connector dispatch wrapper into its typed child result', () => {
    const wrapper = toolCall('wrapper', 'repl');
    wrapper.content.attempt = 1;
    wrapper.content.operation_id = 'wrapper';
    wrapper.content.args = {
      code: 'result = host.mcp("pubmed", "search_articles", query="kinase")',
      human_description: 'Search the literature for kinase evidence',
    };
    const child = toolCall('child', 'mcp__pubmed__search_articles');
    child.content.attempt = 1;
    child.content.operation_id = 'child';
    child.content.parent_operation_id = 'wrapper';
    child.content.args = { query: 'kinase' };
    child.content.output = JSON.stringify({ pmids: ['12345678'] });

    expect(startsNewToolSummary([wrapper], child)).toBe(false);
    const collapsed = collapseNestedConnectorDispatches([...normalizeForTest(wrapper), ...normalizeForTest(child)]);
    expect(collapsed).toHaveLength(1);
    expect(collapsed[0]).toMatchObject({
      name: 'mcp__pubmed__search_articles',
      humanDescription: 'Search the literature for kinase evidence',
      output: JSON.stringify({ pmids: ['12345678'] }),
    });
  });

  it('does not repeat readable wrapper evidence already present in the typed child', () => {
    const wrapper = toolCall('wrapper', 'repl');
    wrapper.content.attempt = 1;
    wrapper.content.operation_id = 'wrapper';
    wrapper.content.args = {
      code: 'result = host.mcp("pubmed", "get_article_metadata", pmids=["41010003"])',
      human_description: 'Reviewing the selected pharmacokinetics article',
    };
    wrapper.content.output = JSON.stringify({
      stdout: 'Title: Fluconazole pharmacokinetics\nAbstract: Public evidence text.',
      stderr: '',
      kernel_id: 'private-kernel',
    });
    const child = toolCall('child', 'mcp__pubmed__get_article_metadata');
    child.content.attempt = 1;
    child.content.operation_id = 'child';
    child.content.parent_operation_id = 'wrapper';
    child.content.args = { pmids: ['41010003'] };
    child.content.output = JSON.stringify({
      count: 1,
      articles: [{ title: 'Fluconazole pharmacokinetics', abstract: 'Public evidence text.' }],
    });

    const [collapsed] = collapseNestedConnectorDispatches([...normalizeForTest(wrapper), ...normalizeForTest(child)]);
    const output = JSON.parse(collapsed.output ?? '{}') as Record<string, unknown>;

    expect(collapsed.humanDescription).toBe('Reviewing the selected pharmacokinetics article');
    expect(output).toMatchObject({
      count: 1,
    });
    expect(output.articles).toHaveLength(1);
    expect(output).not.toHaveProperty('stdout');
    expect(output).not.toHaveProperty('kernel_id');
  });

  it('drops wrapper stdout when it only repeats the typed child as serialized JSON', () => {
    const wrapper = toolCall('wrapper', 'repl');
    wrapper.content.attempt = 1;
    wrapper.content.operation_id = 'wrapper';
    wrapper.content.args = {
      code: 'result = host.mcp("pubmed", "get_article_metadata", pmids=["35658005"])',
      human_description: 'Verify the selected clinical article',
    };
    wrapper.content.output = JSON.stringify({
      stdout: JSON.stringify({ articles: [{ title: 'Adagrasib in NSCLC', doi: '10.1056/NEJMoa2204619' }] }, null, 2),
    });
    const child = toolCall('child', 'mcp__pubmed__get_article_metadata');
    child.content.attempt = 1;
    child.content.operation_id = 'child';
    child.content.parent_operation_id = 'wrapper';
    child.content.args = { pmids: ['35658005'] };
    child.content.output = JSON.stringify({
      count: 1,
      articles: [{ title: 'Adagrasib in NSCLC', doi: '10.1056/NEJMoa2204619' }],
    });

    const [collapsed] = collapseNestedConnectorDispatches([...normalizeForTest(wrapper), ...normalizeForTest(child)]);
    const output = JSON.parse(collapsed.output ?? '{}') as Record<string, unknown>;

    expect(output.articles).toHaveLength(1);
    expect(output).not.toHaveProperty('stdout');
  });

  it('never collapses adjacent operations without an explicit parent identity', () => {
    const wrapper = toolCall('wrapper', 'repl');
    wrapper.content.args = {
      code: 'result = host.mcp("pubmed", "search_articles", query="kinase")',
      human_description: 'Search the literature for kinase evidence',
    };
    wrapper.content.status = 'error';
    const unrelated = toolCall('child', 'mcp__pubmed__search_articles');
    unrelated.content.status = 'completed';

    const collapsed = collapseNestedConnectorDispatches([...normalizeForTest(wrapper), ...normalizeForTest(unrelated)]);

    expect(collapsed).toHaveLength(2);
    expect(collapsed.map((tool) => tool.status)).toEqual(['error', 'completed']);
  });

  it('does not attach a child to a same-named parent from another runner attempt', () => {
    const oldWrapper = toolCall('wrapper-old', 'repl');
    oldWrapper.content.attempt = 1;
    oldWrapper.content.operation_id = 'shared-wrapper';
    oldWrapper.content.args = { code: 'host.mcp("pubmed", "search_articles")' };
    const currentWrapper = toolCall('wrapper-current', 'repl');
    currentWrapper.content.attempt = 2;
    currentWrapper.content.operation_id = 'shared-wrapper';
    currentWrapper.content.args = { code: 'host.mcp("pubmed", "search_articles")' };
    const oldChild = toolCall('child-old', 'mcp__pubmed__search_articles');
    oldChild.content.attempt = 1;
    oldChild.content.operation_id = 'child-old';
    oldChild.content.parent_operation_id = 'shared-wrapper';

    const collapsed = collapseNestedConnectorDispatches([
      ...normalizeForTest(oldWrapper),
      ...normalizeForTest(currentWrapper),
      ...normalizeForTest(oldChild),
    ]);

    expect(collapsed).toHaveLength(2);
    expect(collapsed.map((tool) => `${tool.attempt}:${tool.name}`)).toEqual([
      '1:mcp__pubmed__search_articles',
      '2:repl',
    ]);
  });

  it('does not duplicate a child that precedes its declared parent in a malformed projection', () => {
    const wrapper = toolCall('wrapper-late', 'repl');
    wrapper.content.attempt = 1;
    wrapper.content.operation_id = 'wrapper-late';
    wrapper.content.args = { code: 'host.mcp("pubmed", "search_articles")' };
    const child = toolCall('child-early', 'mcp__pubmed__search_articles');
    child.content.attempt = 1;
    child.content.operation_id = 'child-early';
    child.content.parent_operation_id = 'wrapper-late';

    const collapsed = collapseNestedConnectorDispatches([...normalizeForTest(child), ...normalizeForTest(wrapper)]);

    expect(collapsed).toHaveLength(2);
    expect(collapsed.map((tool) => tool.name)).toEqual(['mcp__pubmed__search_articles', 'repl']);
  });
});

function normalizeForTest(message: IMessageToolCall) {
  return normalizeToolMessages([message]);
}

const scopedStreamTool = (attempt: number, call: string, createdAt: number, status = 'completed'): IMessageToolCall =>
  transformMessage({
    type: 'tool_call',
    msg_id: `transcript-tool:frame:synthetic:${attempt}:${call}`,
    conversation_id: 'scope-conversation',
    created_at: createdAt,
    data: {
      call_id: call,
      name: 'bash',
      args: { command: `synthetic ${call}` },
      status,
      attempt,
      operation_id: call,
      revision: status === 'running' ? 1 : 2,
      xScience: { branchId: 'br_00000001', messageIndex: attempt, blockIndex: 0 },
    },
  } as Parameters<typeof transformMessage>[0]) as IMessageToolCall;

describe('live/history tool group execution scope', () => {
  it('keeps a replaced old history observation before the new page and out of its current group', () => {
    const oldRunning = scopedStreamTool(7, 'old-operation', 10, 'running');
    const initialBaseline = new Set([oldRunning]);
    const oldFinished = scopedStreamTool(7, 'old-operation', 10, 'error');
    const currentTool = scopedStreamTool(9, 'current-operation', 30, 'running');
    let live = composeMessage(oldFinished, [oldRunning]);
    live = composeMessage(currentTool, live);
    const liveWindow = live.filter((message) => !initialBaseline.has(message as IMessageToolCall));
    const merged = mergeLoadedPageWithCurrent('scope-conversation', [currentTool], liveWindow, true, false);
    const groups = buildMessagePresentationList(merged, []).filter((row) => row.type === 'tool_summary');
    const active = groups.find((row) => row.sourceMessageIds.includes(currentTool.id));
    expect(active).toBeDefined();
    expect(normalizeToolMessages(active!.messages).map((tool) => [tool.attempt, tool.messageId])).toEqual([
      [9, currentTool.id],
    ]);
    expect(merged.map((message) => message.id)).toEqual([oldFinished.id, currentTool.id]);
    expect(groups.flatMap((row) => row.sourceMessageIds)).toEqual([oldFinished.id, currentTool.id]);
  });

  it('keeps same-attempt parallel operations together through terminal hydration and repeated refresh', () => {
    const first = scopedStreamTool(9, 'parallel-first', 30, 'running');
    const second = scopedStreamTool(9, 'parallel-second', 31, 'running');
    const completed = scopedStreamTool(9, 'parallel-first', 30);
    let live = composeMessage(first, []);
    live = composeMessage(second, live);
    const merged = mergeLoadedPageWithCurrent('scope-conversation', [completed], live, true, false);
    const refreshed = mergeLoadedPageWithCurrent('scope-conversation', [completed], merged, true, true);
    expect(refreshed.map((message) => message.id)).toEqual([first.id, second.id]);
    const groups = buildMessagePresentationList(refreshed, []).filter((row) => row.type === 'tool_summary');
    expect(groups).toHaveLength(1);
    expect(normalizeToolMessages(groups[0].messages).map((tool) => [tool.messageId, tool.status])).toEqual([
      [first.id, 'completed'],
      [second.id, 'running'],
    ]);
    expect(buildMessagePresentationList(refreshed, [])).toEqual(buildMessagePresentationList(merged, []));
  });

  it.each(['attempt', 'branch', 'conversation'] as const)(
    'keeps adjacent same-family operations in separate %s owners even when arrival order is reversed',
    (scope) => {
      const current = scopedStreamTool(9, 'current-operation', 30);
      const historical = scopedStreamTool(scope === 'attempt' ? 7 : 9, 'historical-operation', 10, 'error');
      if (scope === 'branch') historical.content.xScience!.branchId = 'br_00000002';
      if (scope === 'conversation') historical.conversation_id = 'another-conversation';
      const groups = buildMessagePresentationList([current, historical], []).filter(
        (row) => row.type === 'tool_summary'
      );
      expect(groups).toHaveLength(2);
      expect(groups.map((group) => group.sourceMessageIds)).toEqual([[historical.id], [current.id]]);
      expect(groups.map((group) => normalizeToolMessages(group.messages).map((tool) => tool.messageId))).toEqual([
        [historical.id],
        [current.id],
      ]);
    }
  );

  it('does not borrow a native attempt for an observation without execution scope', () => {
    const current = scopedStreamTool(9, 'current-operation', 30);
    const unbound = toolCall('legacy-operation', 'bash');
    unbound.conversation_id = current.conversation_id;
    unbound.created_at = 10;
    const groups = buildMessagePresentationList([current, unbound], []).filter((row) => row.type === 'tool_summary');
    expect(groups).toHaveLength(2);
    expect(groups.every((group) => group.sourceMessageIds.length === 1)).toBe(true);
  });

  it('retains late historical errors in their own round through current completion and a fresh page', () => {
    const oldRunning = scopedStreamTool(7, 'old-operation', 10, 'running');
    const oldFailed = scopedStreamTool(7, 'old-operation', 10, 'error');
    const current = scopedStreamTool(9, 'current-operation', 30, 'running');
    const currentFinished = scopedStreamTool(9, 'current-operation', 30);
    const terminal = {
      id: 'current-final',
      msg_id: 'current-final',
      conversation_id: current.conversation_id,
      type: 'text',
      position: 'left',
      created_at: 40,
      terminal_status: 'completed',
      content: { content: 'Current operation finished.' },
    } as TMessage;
    const late = mergeLoadedPageWithCurrent('scope-conversation', [oldFailed], [oldRunning, current], true, true);
    const complete = mergeLoadedPageWithCurrent('scope-conversation', [currentFinished, terminal], late, true, false);
    const reload = buildMessagePresentationList([oldFailed, currentFinished, terminal], []);
    expect(complete.map((message) => message.id)).toEqual([oldFailed.id, current.id, terminal.id]);
    expect(buildMessagePresentationList(complete, [])).toEqual(reload);
    const groups = reload.filter((row) => row.type === 'tool_summary');
    expect(groups.map((group) => normalizeToolMessages(group.messages).map((tool) => tool.status))).toEqual([
      ['error'],
      ['completed'],
    ]);
  });

  it('keeps the original unanchored live tail and conversation replacement behavior', () => {
    const history = scopedStreamTool(7, 'history-operation', 10);
    const current = scopedStreamTool(9, 'current-operation', 30, 'running');
    expect(mergeLoadedPageWithCurrent('scope-conversation', [history], [current], true, false)).toEqual([
      history,
      current,
    ]);
    const foreign = { ...current, conversation_id: 'other-conversation' };
    expect(mergeLoadedPageWithCurrent('other-conversation', [foreign], [current], true, false)).toEqual([foreign]);
  });
});
