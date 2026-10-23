import { Button, Empty, Message, Spin, Switch, Tag } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import React, { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { resolveXScienceMcpToolDescription } from '@/renderer/services/mcp/xScienceMcpDescriptions';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '@/renderer/services/rendererAccountScope';
import {
  loadXScienceMcpToolPermissions,
  updateXScienceMcpToolPermission,
  type XScienceMcpServer,
  type XScienceMcpToolPermission,
  type XScienceMcpToolPermissionState,
} from '@/renderer/services/xScienceCapabilities';

type Props = {
  server: XScienceMcpServer | null;
  onCancel: () => void;
};

const DECISIONS: XScienceMcpToolPermissionState[] = ['allow', 'ask', 'deny'];

export const McpPermissionsModal: React.FC<Props> = ({ server, onCancel }) => {
  const subscriptionId = useId();
  const subscribe = useCallback(
    (changed: () => void) => registerRendererAccountReset(`mcp-permissions:${subscriptionId}`, changed),
    [subscriptionId]
  );
  const account = useSyncExternalStore(subscribe, getRendererAccountScopeToken, getRendererAccountScopeToken);
  const selection = useRef({ account, dismissed: false });
  const accountChanged = Boolean(server) && selection.current.account !== account;
  useLayoutEffect(() => {
    if (!server) selection.current = { account, dismissed: false };
    else if (accountChanged && !selection.current.dismissed) {
      selection.current.dismissed = true;
      onCancel();
    }
  }, [server, account, accountChanged, onCancel]);
  // A no-op or delayed parent close must not re-admit the prior account's selection.
  const currentServer = !accountChanged && getRendererAccountOwnerId() ? server : null;
  return (
    <McpPermissionsPanel
      key={JSON.stringify([account, currentServer?.id, currentServer?.source])}
      account={account}
      server={currentServer}
      onCancel={onCancel}
    />
  );
};

/** A service switch retires this request owner, not the requests already sent. */
const McpPermissionsPanel: React.FC<Props & { account: string }> = ({ server, onCancel, account }) => {
  const { t, i18n } = useTranslation();
  const [tools, setTools] = useState<XScienceMcpToolPermission[]>([]);
  const [loading, setLoading] = useState(Boolean(server));
  const [loadFailed, setLoadFailed] = useState(false);
  const [mutatingTool, setMutatingTool] = useState<string | null>(null);
  const [mutatingAll, setMutatingAll] = useState(false);
  const [skipApprovalsActive, setSkipApprovalsActive] = useState(false);
  const loadGeneration = useRef(0);
  const active = useRef(true);
  const writing = useRef(false);
  const serverId = server?.id;
  const current = useCallback(() => active.current && account === getRendererAccountScopeToken(), [account]);

  useLayoutEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
      loadGeneration.current += 1;
    };
  }, []);

  const load = useCallback(async () => {
    const generation = ++loadGeneration.current;
    setTools([]);
    setSkipApprovalsActive(false);
    setLoadFailed(false);
    if (!serverId) {
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const permissions = await loadXScienceMcpToolPermissions(serverId);
      if (current() && generation === loadGeneration.current) {
        setTools(permissions.tools);
        setSkipApprovalsActive(
          permissions.skipApprovalsActive ||
            (permissions.tools.length > 0 && permissions.tools.every((tool) => tool.state === 'allow'))
        );
      }
    } catch (error) {
      if (!current() || generation !== loadGeneration.current) return;
      console.warn('[McpPermissionsModal] Failed to load MCP tool permissions', {
        errorName: error instanceof Error ? error.name : typeof error,
      });
      if (generation === loadGeneration.current) {
        setTools([]);
        setSkipApprovalsActive(false);
        setLoadFailed(true);
      }
    } finally {
      if (current() && generation === loadGeneration.current) {
        setLoading(false);
      }
    }
  }, [serverId, current]);

  useEffect(() => {
    void load();
    return () => {
      loadGeneration.current += 1;
    };
  }, [load]);

  const setDecision = useCallback(
    async (tool: XScienceMcpToolPermission, state: XScienceMcpToolPermissionState) => {
      if (!server || !current() || loading || writing.current) return;
      writing.current = true;
      setMutatingTool(tool.toolName);
      try {
        await updateXScienceMcpToolPermission(server.id, tool.toolName, state);
        if (!current()) return;
        const nextTools = tools.map((item) => (item.toolName === tool.toolName ? { ...item, state } : item));
        setTools(nextTools);
        setSkipApprovalsActive(nextTools.length > 0 && nextTools.every((item) => item.state === 'allow'));
      } catch (error) {
        if (!current()) return;
        console.warn('[McpPermissionsModal] Failed to update MCP tool permission', {
          errorName: error instanceof Error ? error.name : typeof error,
        });
        Message.error(t('settings.xScienceMcpMutationError'));
      } finally {
        writing.current = false;
        if (current()) setMutatingTool(null);
      }
    },
    [server, t, loading, current, tools]
  );

  const setAllDecisions = useCallback(
    async (allow: boolean) => {
      if (!server || !current() || loading || writing.current || tools.length === 0) return;
      writing.current = true;
      setMutatingAll(true);
      const state: XScienceMcpToolPermissionState = allow ? 'allow' : 'ask';
      try {
        const results = await Promise.allSettled(
          tools.map((tool) => updateXScienceMcpToolPermission(server.id, tool.toolName, state))
        );
        if (!current()) return;
        if (results.some((result) => result.status === 'rejected'))
          throw new Error('MCP permission batch did not fully commit');
        setTools((prevTools) => prevTools.map((tool) => ({ ...tool, state })));
        setSkipApprovalsActive(allow);
      } catch (error) {
        if (!current()) return;
        console.warn('[McpPermissionsModal] Failed to update all MCP tool permissions', {
          errorName: error instanceof Error ? error.name : typeof error,
        });
        Message.error(t('settings.xScienceMcpMutationError'));
        await load();
      } finally {
        writing.current = false;
        if (current()) setMutatingAll(false);
      }
    },
    [load, server, t, tools, loading, current]
  );

  return (
    <Modal
      visible={Boolean(server)}
      title={t('settings.xScienceMcpPermissionsTitle', { name: server?.displayName ?? '' })}
      footer={null}
      onCancel={onCancel}
      style={{ width: 720, maxWidth: 'calc(100vw - 32px)' }}
      unmountOnExit
    >
      <Spin loading={loading} className='w-full'>
        <div className='max-h-60vh overflow-y-auto flex flex-col gap-8px'>
          {loading ? <p role='status'>{t('common.loading')}</p> : null}
          {loadFailed ? (
            <div role='alert'>
              <p>{t('settings.xScienceMcpPermissionsLoadError')}</p>
              <Button
                size='mini'
                disabled={loading || mutatingAll || Boolean(mutatingTool)}
                onClick={() => void load()}
              >
                {t('common.retry')}
              </Button>
            </div>
          ) : null}
          {tools.length > 0 ? (
            <div className='mb-4px flex items-start justify-between gap-16px rounded-8px border border-arco-2 bg-fill-1 px-13px py-12px'>
              <div>
                <div className='text-13px font-650 text-t-primary'>{t('settings.xScienceMcpSkipApprovals')}</div>
                <div className='mt-3px text-12px leading-5 text-t-secondary'>
                  {t('settings.xScienceMcpSkipApprovalsDetail')}
                </div>
              </div>
              <Switch
                checked={skipApprovalsActive}
                loading={mutatingAll}
                disabled={mutatingAll || Boolean(mutatingTool)}
                aria-label={t('settings.xScienceMcpSkipApprovals')}
                data-testid='x-science-mcp-skip-approvals'
                onChange={(checked) => void setAllDecisions(checked)}
              />
            </div>
          ) : null}
          {tools.map((tool) => {
            const description = server
              ? resolveXScienceMcpToolDescription(
                  server,
                  tool.toolName,
                  tool.description,
                  i18n.resolvedLanguage ?? i18n.language
                )
              : tool.description;
            return (
              <div
                key={tool.toolName}
                className='border-t border-arco-2 px-2px py-12px first:border-t-0 flex flex-col sm:flex-row sm:items-start sm:justify-between gap-10px'
              >
                <div className='min-w-0'>
                  <div className='flex flex-wrap items-center gap-6px'>
                    <span className='text-13px font-600 text-t-primary'>{tool.title || tool.toolName}</span>
                    {tool.readOnlyHint ? <Tag size='small'>{t('settings.xScienceMcpReadOnlyTool')}</Tag> : null}
                  </div>
                  <div className='mt-3px text-11px text-t-tertiary break-all'>{tool.toolName}</div>
                  {description ? (
                    <div className='mt-4px text-12px leading-5 text-t-secondary'>{description}</div>
                  ) : null}
                </div>
                <div className='flex flex-wrap gap-6px shrink-0'>
                  {DECISIONS.map((decision) => (
                    <Button
                      key={decision}
                      size='mini'
                      type={tool.state === decision ? 'primary' : 'secondary'}
                      loading={mutatingTool === tool.toolName && tool.state !== decision}
                      disabled={Boolean(mutatingTool) || mutatingAll}
                      aria-pressed={tool.state === decision}
                      data-testid={`x-science-mcp-permission-${tool.toolName}-${decision}`}
                      onClick={() => void setDecision(tool, decision)}
                    >
                      {permissionLabel(decision, t)}
                    </Button>
                  ))}
                </div>
              </div>
            );
          })}
          {!loading && !loadFailed && tools.length === 0 ? (
            <Empty description={t('settings.xScienceMcpPermissionsEmpty')} />
          ) : null}
        </div>
      </Spin>
    </Modal>
  );
};

function permissionLabel(state: XScienceMcpToolPermissionState, t: ReturnType<typeof useTranslation>['t']): string {
  if (state === 'allow') return t('settings.xScienceMcpPermissionAllow');
  if (state === 'deny') return t('settings.xScienceMcpPermissionDeny');
  return t('settings.xScienceMcpPermissionAsk');
}
