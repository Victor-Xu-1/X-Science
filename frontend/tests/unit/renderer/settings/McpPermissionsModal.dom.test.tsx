import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Message } from '@arco-design/web-react';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import type {
  XScienceMcpServer,
  XScienceMcpToolPermissions,
  XScienceMcpToolPermissionState,
} from '@/renderer/services/xScienceCapabilities';
import { renderWithI18n } from '../../i18nTestUtils';

const api = vi.hoisted(() => ({ load: vi.fn(), update: vi.fn() }));
vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceMcpToolPermissions: api.load,
  updateXScienceMcpToolPermission: api.update,
}));
import { McpPermissionsModal } from '@/renderer/pages/settings/ToolsSettings/McpPermissionsModal';

const server = (id = 'bundled:structures-interactions', source = 'bundled'): XScienceMcpServer => ({
  id,
  source,
  name: 'structures-interactions',
  displayName: id,
  description: 'Structure tools',
  authRequired: false,
  oauthSupported: false,
  authState: '',
  authHint: '',
  apiKeyConfigurable: false,
  apiKeyConfigured: false,
  apiKeyLabel: '',
  transport: 'builtin',
  upstreams: [],
  hostedByXScience: true,
  health: { ok: true },
  attachedAgents: [],
  enabled: true,
  connectionStatus: 'connected',
});
const permissions = (
  state: XScienceMcpToolPermissionState = 'ask',
  description = 'Backend description.'
): XScienceMcpToolPermissions => ({
  skipApprovalsActive: false,
  tools: ['pdb_search_sequence', 'pdb_search_structure_similarity'].map((toolName) => ({
    toolName,
    title: null as string | null,
    description,
    readOnlyHint: true,
    state,
  })),
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const onCancel = (): undefined => undefined;

beforeEach(() => {
  setRendererAccountOwner('permission-owner-a');
  api.load.mockReset().mockResolvedValue(permissions());
  api.update.mockReset().mockResolvedValue(undefined);
  vi.spyOn(Message, 'error').mockImplementation(() => (): undefined => undefined);
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.restoreAllMocks();
});

describe('PDB method permissions presentation and request ownership', () => {
  it('renders both real method descriptions and updates with the actual i18n language', async () => {
    const view = await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    expect(await screen.findByText(/sequence identity and query coverage/)).toBeVisible();
    expect(screen.getByText(/deposited chain/)).toBeVisible();
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expect(await screen.findByText(/序列一致性和查询覆盖度/)).toBeVisible();
    expect(screen.getByText(/已收录的链/)).toBeVisible();
  });

  it.each([
    ['custom:structures-interactions', 'custom'],
    ['bundled:structures-interactions', 'custom'],
    ['bundled:other', 'bundled'],
  ])('does not relabel tools from %s / %s', async (id, source) => {
    await renderWithI18n(<McpPermissionsModal server={server(id, source)} onCancel={onCancel} />, 'zh-CN');
    expect(await screen.findAllByText('Backend description.')).toHaveLength(2);
    expect(screen.queryByText(/序列一致性和查询覆盖度/)).not.toBeInTheDocument();
  });

  it('shows a load failure rather than an empty catalog, then retries and reads real tools', async () => {
    const first = deferred<XScienceMcpToolPermissions>();
    api.load.mockReturnValueOnce(first.promise).mockResolvedValueOnce(permissions());
    await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    expect(screen.queryByText('No tools reported.')).not.toBeInTheDocument();
    await act(async () => {
      first.reject(new Error('offline'));
    });
    expect(await screen.findByRole('alert')).toHaveTextContent('Tool permissions could not be loaded.');
    expect(screen.queryByText('No tools reported.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText(/sequence identity and query coverage/)).toBeVisible();
    expect(api.load).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('ignores a late catalog response after switching services', async () => {
    const old = deferred<XScienceMcpToolPermissions>();
    api.load.mockReturnValueOnce(old.promise).mockResolvedValueOnce(permissions('ask', 'Current custom tools.'));
    const view = await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    view.rerender(<McpPermissionsModal server={server('custom:second', 'custom')} onCancel={onCancel} />);
    expect(await screen.findAllByText('Current custom tools.')).toHaveLength(2);
    await act(async () => {
      old.resolve(permissions('deny', 'Stale tools.'));
    });
    expect(screen.queryByText('Stale tools.')).not.toBeInTheDocument();
    expect(screen.getAllByText('Current custom tools.')).toHaveLength(2);
  });

  it.each(['single', 'all'] as const)(
    'does not apply an old %s permission acknowledgement to the new server',
    async (mode) => {
      const oldWrite = deferred<void>();
      api.update.mockReturnValue(oldWrite.promise);
      const view = await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
      await screen.findByTestId('x-science-mcp-permission-pdb_search_sequence-ask');
      if (mode === 'single') fireEvent.click(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-deny'));
      else fireEvent.click(screen.getByTestId('x-science-mcp-skip-approvals'));
      await waitFor(() => expect(api.update).toHaveBeenCalled());
      view.rerender(<McpPermissionsModal server={server('custom:second', 'custom')} onCancel={onCancel} />);
      await screen.findAllByText('Backend description.');
      await act(async () => {
        oldWrite.resolve();
      });
      expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-ask')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-deny')).toHaveAttribute(
        'aria-pressed',
        'false'
      );
      expect(screen.getByTestId('x-science-mcp-skip-approvals')).toHaveAttribute('aria-checked', 'false');
      expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-ask')).not.toBeDisabled();
      expect(api.update.mock.calls.every(([id]) => id === 'bundled:structures-interactions')).toBe(true);
    }
  );

  it.each([
    ['read', 'different-owner'],
    ['write', 'different-owner'],
    ['read', 'same-owner-new-session'],
    ['write', 'same-owner-new-session'],
  ] as const)('closes the old selection and rejects a late %s after %s', async (phase, transition) => {
    const oldRead = deferred<XScienceMcpToolPermissions>();
    const oldWrite = deferred<void>();
    api.load
      .mockImplementationOnce(() => (phase === 'read' ? oldRead.promise : Promise.resolve(permissions())))
      .mockResolvedValueOnce(permissions('ask', 'Current account tools.'));
    api.update.mockReturnValueOnce(oldWrite.promise);
    const dismiss = vi.fn();
    const previousServer = { ...server('custom:private', 'custom'), displayName: 'Previous private connector' };
    const view = await renderWithI18n(<McpPermissionsModal server={previousServer} onCancel={dismiss} />, 'en-US');
    if (phase === 'write')
      fireEvent.click(await screen.findByTestId('x-science-mcp-permission-pdb_search_sequence-deny'));
    await act(async () => {
      if (transition === 'same-owner-new-session') {
        setRendererAccountOwner(null);
        setRendererAccountOwner('permission-owner-a');
      } else setRendererAccountOwner('permission-owner-b');
    });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(dismiss).toHaveBeenCalledTimes(1);
    expect(api.load).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/Previous private connector/)).not.toBeInTheDocument();
    await act(async () => {
      if (phase === 'read') oldRead.resolve(permissions('deny', 'Previous account tools.'));
      else oldWrite.resolve();
    });
    view.rerender(<McpPermissionsModal server={previousServer} onCancel={dismiss} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.load).toHaveBeenCalledTimes(1);
    expect(Message.error).not.toHaveBeenCalled();
    // Only an actual cleared selection followed by a new choice admits the new account.
    view.rerender(<McpPermissionsModal server={null} onCancel={dismiss} />);
    view.rerender(
      <McpPermissionsModal
        server={{ ...previousServer, displayName: 'Current account connector' }}
        onCancel={dismiss}
      />
    );
    expect(await screen.findAllByText('Current account tools.')).toHaveLength(2);
    expect(api.load).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-ask')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-deny')).toHaveAttribute(
      'aria-pressed',
      'false'
    );
    expect(Message.error).not.toHaveBeenCalled();
  });

  it('derives allow-all from mixed permissions and acknowledged individual changes', async () => {
    api.load.mockResolvedValueOnce({
      ...permissions(),
      tools: [permissions().tools[0], permissions('allow').tools[1]],
    });
    await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    const toggle = await screen.findByTestId('x-science-mcp-skip-approvals');
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-allow'));
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'));
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-allow')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    fireEvent.click(screen.getByTestId('x-science-mcp-permission-pdb_search_structure_similarity-deny'));
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'));
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_structure_similarity-deny')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(api.update.mock.calls).toEqual([
      ['bundled:structures-interactions', 'pdb_search_sequence', 'allow'],
      ['bundled:structures-interactions', 'pdb_search_structure_similarity', 'deny'],
    ]);
    expect(api.load).toHaveBeenCalledTimes(1);
  });

  it('keeps a single write owner while an explicit permission change is in flight', async () => {
    const write = deferred<void>();
    api.update.mockReturnValueOnce(write.promise);
    await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    fireEvent.click(await screen.findByTestId('x-science-mcp-permission-pdb_search_sequence-deny'));
    const other = screen.getByTestId('x-science-mcp-permission-pdb_search_structure_similarity-allow');
    expect(other).toBeDisabled();
    fireEvent.click(other);
    expect(api.update).toHaveBeenCalledTimes(1);
    await act(async () => {
      write.resolve();
    });
    expect(other).not.toBeDisabled();
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-deny')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  it('waits for every issued batch write before reconciling partial failure', async () => {
    const first = deferred<void>();
    const second = deferred<void>();
    api.update.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    api.load.mockResolvedValueOnce(permissions()).mockResolvedValueOnce({
      ...permissions(),
      tools: [permissions().tools[0], permissions('allow').tools[1]],
    });
    await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    fireEvent.click(await screen.findByTestId('x-science-mcp-skip-approvals'));
    await act(async () => {
      first.reject(new Error('permission write rejected'));
    });
    expect(api.load).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-ask')).toBeDisabled();
    await act(async () => {
      second.resolve();
    });
    await waitFor(() => expect(api.load).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_sequence-ask')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(screen.getByTestId('x-science-mcp-permission-pdb_search_structure_similarity-allow')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(screen.getByTestId('x-science-mcp-skip-approvals')).toHaveAttribute('aria-checked', 'false');
    expect(api.update).toHaveBeenCalledTimes(2);
  });

  it('discards late errors after closing instead of showing a detached error', async () => {
    const old = deferred<XScienceMcpToolPermissions>();
    api.load.mockReturnValueOnce(old.promise);
    const view = await renderWithI18n(<McpPermissionsModal server={server()} onCancel={onCancel} />, 'en-US');
    view.rerender(<McpPermissionsModal server={null} onCancel={onCancel} />);
    await act(async () => {
      old.reject(new Error('closed'));
    });
    expect(Message.error).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
