import { PRODUCT_NAME } from '@/common/config/productIdentity';
import { Dropdown, Message, Tooltip } from '@arco-design/web-react';
import { Check } from '@icon-park/react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { AcpConfigSetStatus, AcpDerivedOption } from '@/renderer/hooks/xScience/runtime/useAcpConfigOptions';
import XSciencePermissionIcon, { isWarningPermissionMode } from './XSciencePermissionIcon';
import SessionPermissionGrants from './SessionPermissionGrants';
import {
  useSessionPermissionGrantsController,
  useSessionPermissionGrantsView,
} from '@/renderer/hooks/xScience/runtime/useSessionPermissionGrants';
import type { SessionGrantIdentity } from '@/renderer/services/runtime/sessionPermissionGrants';
import type { SessionPermissionGrantsController } from '@/renderer/services/runtime/sessionPermissionGrantsController';

type XSciencePermissionMenuProps = {
  option: AcpDerivedOption | null;
  setStatus: AcpConfigSetStatus;
  setConfigOption: (optionId: string, value: string) => Promise<unknown>;
  /** New conversations use the local assistant override contract. Existing
   * conversations must render only modes advertised by the live runtime. */
  includeStandardModes?: boolean;
  disabled?: boolean;
  sessionIdentity?: SessionGrantIdentity;
  grantsController?: SessionPermissionGrantsController | null;
};

const STANDARD_PERMISSION_MODES: Array<{ value: string; label: string; description: string | null }> = [
  { value: 'default', label: 'Default', description: null },
  { value: 'smart', label: 'Smart', description: null },
  { value: 'bypassPermissions', label: 'Bypass Permissions', description: null },
] as const;

const permissionRowClass =
  'composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-12px text-left font-inherit text-14px leading-none text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50';

const XSciencePermissionMenu: React.FC<XSciencePermissionMenuProps> = ({
  option,
  setStatus,
  setConfigOption,
  includeStandardModes = false,
  disabled = false,
  sessionIdentity,
  grantsController,
}) => {
  const { i18n, t } = useTranslation();
  const [popupVisible, setPopupVisible] = useState(false);
  const localController = useSessionPermissionGrantsController(grantsController ? null : sessionIdentity);
  const controller = grantsController ?? localController;
  const grants = useSessionPermissionGrantsView(controller);
  const currentValue = option?.currentValue ?? 'default';
  const optionId = option?.id ?? 'mode';
  const contextKey = controller?.presentationKey ?? 'permission-mode';
  const completionContext = useRef({ contextKey, optionId, setConfigOption });
  const mounted = useRef(false);
  const mountGeneration = useRef(0);
  if (
    completionContext.current.contextKey !== contextKey ||
    completionContext.current.optionId !== optionId ||
    completionContext.current.setConfigOption !== setConfigOption
  ) {
    completionContext.current = { contextKey, optionId, setConfigOption };
  }
  const isSetting = setStatus.state === 'setting';
  const options = useMemo(() => {
    const merged = [...(includeStandardModes ? STANDARD_PERMISSION_MODES : []), ...(option?.options ?? [])];
    const seen = new Set<string>();
    return merged
      .filter((item) => {
        if (seen.has(item.value)) return false;
        seen.add(item.value);
        return true;
      })
      .map((item) => ({
        value: item.value,
        label: permissionModeLabel(item.value, item.label, i18n, t),
        description: permissionModeDescription(item.value, item.description, t),
      }));
  }, [i18n, includeStandardModes, option?.options, t]);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      mountGeneration.current += 1;
    };
  }, []);
  useEffect(() => {
    setPopupVisible(false);
  }, [controller]);
  useEffect(() => {
    if (controller && !controller.isCurrent()) setPopupVisible(false);
  }, [controller, grants]);

  if (options.length === 0 && !controller) return null;

  const selectedOption = options.find((item) => item.value === currentValue) ?? options[0];
  const isUnavailable = disabled || isSetting;
  const triggerUnavailable = isUnavailable && !controller?.isCurrent();
  const selectPermission = async (value: string) => {
    setPopupVisible(false);
    if (value === currentValue || isUnavailable) return;
    const requestContext = completionContext.current;
    const requestGeneration = mountGeneration.current;
    const canReportCompletion = () =>
      mounted.current &&
      mountGeneration.current === requestGeneration &&
      completionContext.current === requestContext &&
      (!controller || controller.isCurrent());
    try {
      await setConfigOption(optionId, value);
      if (!canReportCompletion()) return;
      Message.success(t('agentMode.switchSuccess'));
    } catch (error) {
      if (!canReportCompletion()) return;
      console.error(`[${PRODUCT_NAME} PermissionMenu] Failed to set permission mode`, error);
      Message.error(t('agentMode.switchFailed'));
    }
  };

  const menu = (
    <div
      className='app-overlay-menu composer-control-menu box-border overflow-y-auto rd-8px py-6px'
      data-testid='x-science-permission-menu'
      role='menu'
      aria-label={t('agentMode.switchMode')}
      style={{
        width: 'min(210px, calc(100vw - 24px))',
        maxWidth: 'calc(100vw - 24px)',
        maxHeight: 'calc(100vh - 24px)',
        opacity: 1,
      }}
    >
      {options.map((item) => {
        const selected = item.value === currentValue;
        const warning = isWarningPermissionMode(item.value);
        return (
          <Tooltip key={item.value} content={item.description} position='left'>
            <button
              type='button'
              role='menuitemradio'
              aria-checked={selected}
              aria-label={`${item.label}: ${item.description}`}
              title={item.description}
              data-testid={`x-science-permission-option-${item.value}`}
              className={permissionRowClass}
              disabled={isUnavailable}
              onClick={() => void selectPermission(item.value)}
            >
              <span className='inline-flex w-20px shrink-0 text-t-tertiary'>
                <XSciencePermissionIcon mode={item.value} size={18} />
              </span>
              <span className='min-w-0 flex-1 truncate' style={warning ? { color: 'var(--warning)' } : undefined}>
                {item.label}
              </span>
              <span
                className='inline-flex w-20px shrink-0 justify-end text-t-primary'
                style={warning ? { color: 'var(--warning)' } : undefined}
              >
                {selected ? <Check theme='outline' size={15} fill={warning ? 'var(--warning)' : undefined} /> : null}
              </span>
            </button>
          </Tooltip>
        );
      })}
      {controller ? <SessionPermissionGrants controller={controller} open={popupVisible} /> : null}
    </div>
  );

  return (
    <Dropdown
      key={controller?.presentationKey ?? 'permission-mode'}
      trigger='click'
      position='tl'
      popupVisible={popupVisible}
      onVisibleChange={(visible) => !triggerUnavailable && setPopupVisible(visible)}
      droplist={menu}
    >
      <button
        type='button'
        data-testid='x-science-permission-selector'
        data-current-permission={selectedOption?.value}
        aria-label={
          selectedOption ? `${t('agentMode.permission')} · ${selectedOption.label}` : t('agentMode.permission')
        }
        title={selectedOption ? `${selectedOption.label}: ${selectedOption.description}` : undefined}
        disabled={triggerUnavailable}
        onClick={() => setPopupVisible((visible) => !visible)}
        className='composer-icon-control relative'
      >
        <XSciencePermissionIcon mode={selectedOption?.value ?? 'default'} size={17} />
        {controller?.isCurrent() && grants.available && Boolean(grants.snapshot?.grants.length) ? (
          <span
            className='absolute -right-4px -top-4px rounded-full bg-fill-3 px-3px text-10px leading-14px'
            aria-label={t('agentMode.sessionGrants.count', { count: grants.snapshot!.grants.length })}
          >
            {grants.snapshot!.grants.length}
          </span>
        ) : null}
      </button>
    </Dropdown>
  );
};

function isFullAccessMode(value: string): boolean {
  return isWarningPermissionMode(value);
}

function permissionModeLabel(
  value: string,
  fallback: string,
  i18n: ReturnType<typeof useTranslation>['i18n'],
  t: ReturnType<typeof useTranslation>['t']
): string {
  if (value === 'default') return t('agentMode.requestApproval');
  if (value === 'smart') return t('agentMode.helpApprove');
  if (value === 'bypassPermissions') return t('agentMode.fullAccess');
  return i18n.exists(`agentMode.${value}`) ? t(`agentMode.${value}`) : fallback;
}

function permissionModeDescription(
  value: string,
  provided: string | null | undefined,
  t: ReturnType<typeof useTranslation>['t']
): string {
  if (['default', 'smart', 'bypassPermissions'].includes(value)) return fallbackPermissionDescription(value, t);
  return provided || fallbackPermissionDescription(value, t);
}

function fallbackPermissionDescription(value: string, t: ReturnType<typeof useTranslation>['t']): string {
  if (isFullAccessMode(value)) {
    return t('agentMode.fullAccessDescription');
  }
  if (['smart', 'auto', 'dontAsk', 'autoEdit'].includes(value)) {
    return t('agentMode.riskyOperationsDescription');
  }
  return t('agentMode.approvalRequiredDescription');
}

export default XSciencePermissionMenu;
