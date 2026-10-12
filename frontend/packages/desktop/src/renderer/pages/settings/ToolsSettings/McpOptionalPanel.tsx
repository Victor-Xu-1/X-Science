import { Button, Empty, Message, Spin } from '@arco-design/web-react';
import { Refresh } from '@icon-park/react';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  loadXScienceOptionalMcps,
  startXScienceOptionalMcpInstall,
  type XScienceOptionalMcp,
} from '@/renderer/services/xScienceCapabilities';
import { sanitizeExternalHttpUrl } from '@/renderer/utils/platform';
import { McpConnectorVisualMark, resolveMcpConnectorVisual } from './mcpConnectorVisuals';

type Props = {
  onInstalled: () => Promise<void>;
};

export const McpOptionalPanel: React.FC<Props> = ({ onInstalled }) => {
  const { t } = useTranslation();
  const [items, setItems] = useState<XScienceOptionalMcp[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const generationRef = useRef(0);
  const installedNoticeRef = useRef(new Set<string>());

  const refresh = useCallback(
    async (showSpinner = true) => {
      const generation = ++generationRef.current;
      if (showSpinner) setRefreshing(true);
      try {
        const nextItems = await loadXScienceOptionalMcps();
        if (generation === generationRef.current) {
          setItems((current) => {
            const currentByID = new Map(current.map((item) => [item.id, item]));
            for (const item of nextItems) {
              const previous = currentByID.get(item.id);
              if (item.installed && !installedNoticeRef.current.has(item.id) && previous?.installed === false) {
                installedNoticeRef.current.add(item.id);
                void onInstalled();
                Message.success(t('settings.xScienceMcpOptionalInstalled'));
              }
            }
            return nextItems;
          });
        }
      } catch (error) {
        if (generation === generationRef.current) {
          console.error('Failed to load optional X-Science MCPs:', error);
          Message.error(t('settings.xScienceMcpOptionalLoadError'));
        }
      } finally {
        if (generation === generationRef.current) setRefreshing(false);
      }
    },
    [onInstalled, t]
  );

  useEffect(() => {
    setLoading(true);
    void refresh(false).finally(() => setLoading(false));
    return () => {
      generationRef.current += 1;
    };
  }, [refresh]);

  const hasInstalling = items.some((item) => item.status === 'installing');
  useEffect(() => {
    if (!hasInstalling) return;
    const timer = window.setInterval((): void => {
      void refresh(false);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [hasInstalling, refresh]);

  const install = useCallback(
    async (item: XScienceOptionalMcp) => {
      if (busyId || item.status === 'installing' || item.installed) return;
      setBusyId(item.id);
      try {
        const result = await startXScienceOptionalMcpInstall(item.id);
        if (result.installed) installedNoticeRef.current.add(result.id);
        setItems((current) => current.map((candidate) => (candidate.id === result.id ? result : candidate)));
        if (result.status === 'installing') {
          Message.info(t('settings.xScienceMcpOptionalInstallStarted'));
        } else if (result.installed) {
          Message.success(t('settings.xScienceMcpOptionalInstalled'));
          await onInstalled();
        }
      } catch (error) {
        console.error('Failed to start optional X-Science MCP installation:', error);
        Message.error(t('settings.xScienceMcpOptionalInstallError'));
        await refresh(false);
      } finally {
        setBusyId(null);
      }
    },
    [busyId, onInstalled, refresh, t]
  );

  return (
    <div className='flex flex-col gap-14px' data-testid='x-science-mcp-optional'>
      <div className='flex items-start justify-between gap-10px border-y border-arco-2 py-12px'>
        <div className='min-w-0'>
          <h2 className='m-0 text-16px font-650 text-t-primary'>{t('settings.xScienceMcpOptionalTitle')}</h2>
          <p className='m-0 mt-3px max-w-760px text-12px leading-5 text-t-tertiary'>
            {t('settings.xScienceMcpOptionalDescription')}
          </p>
        </div>
        <Button
          type='text'
          size='mini'
          icon={<Refresh size='13' />}
          loading={refreshing}
          aria-label={t('common.refresh')}
          data-testid='x-science-mcp-optional-refresh'
          onClick={() => void refresh()}
        />
      </div>

      <Spin loading={loading} className='w-full'>
        {items.length > 0 ? (
          <div className='x-science-mcp-grid grid grid-cols-1 items-stretch sm:grid-cols-2 xl:grid-cols-3' role='list'>
            {items.map((item) => (
              <OptionalMcpCard
                key={item.id}
                item={item}
                busy={busyId === item.id}
                onInstall={() => void install(item)}
              />
            ))}
          </div>
        ) : !loading ? (
          <Empty description={t('settings.xScienceMcpOptionalEmpty')} />
        ) : null}
      </Spin>
    </div>
  );
};

const OptionalMcpCard: React.FC<{
  item: XScienceOptionalMcp;
  busy: boolean;
  onInstall: () => void;
}> = ({ item, busy, onInstall }) => {
  const { t } = useTranslation();
  const visual = resolveMcpConnectorVisual(item.name, item.displayName);
  const status =
    item.status === 'installed' && item.installed
      ? t('settings.xScienceMcpOptionalStatusInstalled')
      : item.status === 'installing'
        ? t('settings.xScienceMcpOptionalStatusInstalling')
        : item.status === 'failed' || item.status === 'broken'
          ? t('settings.xScienceMcpOptionalStatusFailed')
          : t('settings.xScienceMcpOptionalStatusNotInstalled');
  const canInstall = !item.installed && item.status !== 'installing';
  const sourceUrl = sanitizeExternalHttpUrl(item.sourceRef);
  return (
    <article
      role='listitem'
      className='x-science-mcp-card flex min-w-0 flex-col border border-arco-2 bg-fill-1 transition-colors'
      data-testid={`x-science-mcp-optional-${item.name}`}
      data-mcp-tone={visual.tone}
      data-mcp-visual={visual.glyph}
      data-mcp-install-status={item.status}
      aria-label={`${item.displayName}. ${status}`}
    >
      <div className='x-science-mcp-card__top flex min-w-0 items-center'>
        <div className='x-science-mcp-card__icon flex shrink-0 items-center justify-center'>
          <McpConnectorVisualMark visual={visual} />
        </div>
        <div className='x-science-mcp-card__identity min-w-0 flex-1'>
          <div className='x-science-mcp-card__title-row'>
            <span className='min-w-0 font-650 text-t-primary' title={item.displayName}>
              {item.displayName}
            </span>
          </div>
        </div>
      </div>
      <p className='x-science-mcp-card__description' title={item.description}>
        {item.description}
      </p>
      <div className='flex flex-col gap-3px text-12px leading-5 text-t-tertiary break-words'>
        <span>
          {t('settings.xScienceMcpOptionalLicense')}: {item.license}
        </span>
        <span>{item.sizeLabel}</span>
        {item.installPath ? (
          <span title={item.installPath}>
            {t('settings.xScienceMcpOptionalPath')}: {item.installPath}
          </span>
        ) : null}
        {item.error ? (
          <span className='text-danger-6' title={item.error}>
            {item.error}
          </span>
        ) : null}
      </div>
      <div className='x-science-mcp-card__actions flex items-center justify-between'>
        <div className='x-science-mcp-card__action-cluster flex items-center'>
          <Button
            size='small'
            type='secondary'
            loading={busy || item.status === 'installing'}
            disabled={!canInstall || busy}
            data-testid={`x-science-mcp-optional-install-${item.name}`}
            onClick={onInstall}
          >
            {item.status === 'installing'
              ? t('settings.xScienceMcpOptionalInstalling')
              : item.status === 'failed' || item.status === 'broken'
                ? t('settings.xScienceMcpOptionalRetry')
                : item.installed
                  ? t('settings.xScienceMcpOptionalInstalledAction')
                  : t('settings.xScienceMcpOptionalInstall')}
          </Button>
        </div>
        {sourceUrl ? (
          <a
            className='x-science-mcp-card__more inline-flex items-center justify-center text-12px'
            href={sourceUrl}
            target='_blank'
            rel='noreferrer'
          >
            {t('settings.xScienceMcpOptionalSource')}
          </a>
        ) : null}
      </div>
    </article>
  );
};
