import {
  APPROVAL_SCOPE_COPY,
  getXScienceApprovalGrantHint,
  getXScienceApprovalHeadline,
  getXScienceApprovalScopeCopy,
  getXScienceApprovalScopePolicy,
} from '@/renderer/components/xScience/runtime/approvalScopeModel';
import { describe, expect, it } from 'vitest';

const localExecRequest = {
  requestId: 'request-1',
  toolId: 'tool-1',
  kind: 'local_exec',
  tool: 'python',
  code: 'print("STAT6")',
  description: null,
  environment: 'python',
  mode: 'live',
  questions: [],
};

describe('X-Science approval scope model', () => {
  it('keeps persistent scopes available but defaults every discretionary grant to once', () => {
    expect(getXScienceApprovalScopePolicy('local_exec')).toEqual({
      scopes: ['once', 'conversation', 'project', 'always'],
      defaultScope: 'once',
    });
    expect(getXScienceApprovalScopePolicy('local_exec', 'software_runtime')).toEqual({
      scopes: ['once'],
      defaultScope: 'once',
    });
    expect(getXScienceApprovalScopePolicy('remote_exec').defaultScope).toBe('once');
    expect(getXScienceApprovalScopePolicy('remote_read').defaultScope).toBe('once');
    expect(getXScienceApprovalScopePolicy('mcp_tool').defaultScope).toBe('once');
    expect(getXScienceApprovalScopePolicy('agent_tool', 'edit_file')).toEqual({
      scopes: ['once', 'always'],
      defaultScope: 'once',
    });
    expect(getXScienceApprovalScopePolicy('agent_tool', 'edit_file', false)).toEqual({
      scopes: ['once'],
      defaultScope: 'once',
    });
    expect(getXScienceApprovalScopePolicy('customize_mutation').defaultScope).toBe('once');
    expect(getXScienceApprovalScopePolicy('infer_submit').defaultScope).toBe('once');
    expect(getXScienceApprovalScopePolicy('network')).toEqual({ scopes: ['always'], defaultScope: 'always' });
    expect(getXScienceApprovalScopePolicy('artifact_delete')).toEqual({ scopes: ['once'], defaultScope: 'once' });
    expect(getXScienceApprovalScopePolicy('capability_install')).toEqual({ scopes: ['once'], defaultScope: 'once' });
    expect(APPROVAL_SCOPE_COPY).toMatchObject({
      once: { label: '本次', hint: '仅本次调用' },
      conversation: { label: '本会话', hint: '直到本次会话结束' },
      project: { label: '本项目', hint: '在本项目中记住' },
      always: { label: '全局', hint: '跨所有项目记住' },
    });
  });

  it('keeps capability installation approval one-shot', () => {
    const capabilityRequest = {
      ...localExecRequest,
      kind: 'capability_install',
      tool: 'host.skills.install',
      rememberable: false,
    };
    expect(getXScienceApprovalGrantHint(capabilityRequest)).toBe('此次授权仅适用于当前请求，不会被记住。');
    expect(getXScienceApprovalGrantHint(capabilityRequest, 'en-US')).toBe(
      'This approval applies only to this request and cannot be remembered.'
    );
  });

  it('uses the v1.1 command headline', () => {
    expect(getXScienceApprovalHeadline(localExecRequest)).toBe('运行 Python 代码？');
  });

  it('names the exact network target in the collapsed approval headline', () => {
    const request = { ...localExecRequest, kind: 'network', tool: 'request_network_access', target: 'files.rcsb.org' };
    expect(getXScienceApprovalHeadline(request)).toBe('允许访问网络域名 files.rcsb.org？');
    expect(getXScienceApprovalHeadline(request, 'en-US')).toBe('Allow network access to files.rcsb.org?');
  });

  it('names the exact file target in an agent-tool approval headline', () => {
    const request = {
      ...localExecRequest,
      kind: 'agent_tool',
      tool: 'edit_file',
      target: 'CRBN_binding_mode_analysis.md',
    };
    expect(getXScienceApprovalHeadline(request)).toBe('允许写入文件 CRBN_binding_mode_analysis.md？');
    expect(getXScienceApprovalHeadline(request, 'en-US')).toBe('Write CRBN_binding_mode_analysis.md?');
  });

  it('provides equivalent English approval policy copy', () => {
    expect(getXScienceApprovalScopeCopy('en-US')).toMatchObject({
      once: { label: 'Once', hint: 'Only this invocation' },
      conversation: { label: 'Conversation', hint: 'Until this conversation ends' },
      project: { label: 'Project', hint: 'Remember in this project' },
      always: { label: 'Always', hint: 'Remember across all projects' },
    });
    expect(getXScienceApprovalHeadline(localExecRequest, 'en-US')).toBe('Run Python code?');
  });
});
