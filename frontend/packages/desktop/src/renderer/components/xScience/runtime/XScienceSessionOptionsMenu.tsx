import {
  loadXScienceComputeProviders,
  loadXScienceSessionComputeProviders,
  setXScienceSessionComputeProvider,
  type XScienceComputeProvider,
} from '@/renderer/services/xScienceCompute';
import { resolveLocaleKey } from '@/common/utils';
import { loadXScienceAssistants } from '@/renderer/services/xScienceCatalog';
import type { XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import { resolveAssistantName } from '@/renderer/utils/model/assistantDisplay';
import { Dropdown, Message, Spin, Switch } from '@arco-design/web-react';
import { Check, Right, SettingConfig } from '@icon-park/react';
import { Server, Shield, UserRound } from 'lucide-react';
import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { useNestedMenuKeyboard } from '@/renderer/hooks/ui/useNestedMenuKeyboard';
import { getMenuItems } from '@/renderer/utils/menuKeyboard';
import XScienceAgentControlToggles from './XScienceAgentControlToggles';
import XSciencePermissionIcon, { isWarningPermissionMode } from './XSciencePermissionIcon';
import SessionPermissionGrants from './SessionPermissionGrants';
import { XScienceFullAccessConfirmation, XSciencePermissionChoices } from './XSciencePermissionChoices';
import { useXSciencePermissionControl, type XSciencePermissionControlInput } from './useXSciencePermissionControl';

type SpecialistOption = { id: string; label: string; agentId: string };
type Submenu = 'permission' | 'specialist' | 'compute' | null;

export type XScienceSessionOptionsMenuProps = {
  rootFrameId?: string;
  value: XScienceSessionOptions;
  onChange: (next: XScienceSessionOptions) => void;
  onPersistentOptionChange?: (key: 'autoReview' | 'memory', value: boolean) => void | Promise<void>;
  computeSelection?: string[];
  onComputeSelectionChange?: (providers: string[]) => void;
  onClearGoal?: () => void | Promise<void>;
  goalClearInFlight?: boolean;
  disabled?: boolean;
  permissionControl?: XSciencePermissionControlInput;
};

const rowClass =
  'composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-8px text-left font-inherit text-13px leading-none text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50';
const checkedSwitchStyle: React.CSSProperties = { backgroundColor: 'var(--workspace-accent)' };
const clampToRange = (candidate: number, minimum: number, maximum: number) =>
  Math.min(Math.max(candidate, minimum), maximum);

const XScienceSessionOptionsMenu: React.FC<XScienceSessionOptionsMenuProps> = ({
  rootFrameId,
  value,
  onChange,
  onPersistentOptionChange,
  computeSelection,
  onComputeSelectionChange,
  onClearGoal,
  goalClearInFlight = false,
  disabled = false,
  permissionControl,
}) => {
  const { t, i18n } = useTranslation();
  const localeKey = resolveLocaleKey(i18n.language);
  const navigate = useNavigate();
  const translationRef = useRef(t);
  translationRef.current = t;
  const [visible, setVisible] = useState(false);
  const [submenu, setSubmenu] = useState<Submenu>(null);
  const [loading, setLoading] = useState(false);
  const [pendingProvider, setPendingProvider] = useState<string | null>(null);
  const [pendingOption, setPendingOption] = useState<'autoReview' | 'memory' | null>(null);
  const [specialists, setSpecialists] = useState<SpecialistOption[]>([]);
  const [computeProviders, setComputeProviders] = useState<XScienceComputeProvider[]>([]);
  const [enabledComputeProviders, setEnabledComputeProviders] = useState<string[]>(
    (computeSelection ?? []).filter((provider) => provider !== 'local')
  );
  const submenuCloseTimerRef = useRef<number | null>(null);
  const [submenuPosition, setSubmenuPosition] = useState<React.CSSProperties>();

  const cancelSubmenuClose = () => {
    if (submenuCloseTimerRef.current === null) return;
    window.clearTimeout(submenuCloseTimerRef.current);
    submenuCloseTimerRef.current = null;
  };

  const keyboard = useNestedMenuKeyboard<Exclude<Submenu, null>>({
    open: visible,
    setOpen: setVisible,
    submenu,
    setSubmenu,
    cancelHoverClose: cancelSubmenuClose,
  });
  const { menuRef: menuSurfaceRef, submenuRef, triggerRef, menuId } = keyboard;
  const permission = useXSciencePermissionControl(permissionControl, () => {
    setVisible(false);
    setSubmenu(null);
    triggerRef.current?.focus();
  });
  // Optional session-option hydration must not withdraw the independent mode
  // and observed-grant capabilities that were already available to the user.
  const triggerDisabled = disabled && !permission.available;
  const hasPermissionChoices = permission.options.length > 0;
  useLayoutEffect(() => {
    if (hasPermissionChoices || submenu !== 'permission') return;
    setSubmenu(null);
    (getMenuItems(menuSurfaceRef.current)[0] ?? triggerRef.current)?.focus();
  }, [hasPermissionChoices, submenu]);

  const scheduleSubmenuClose = () => {
    cancelSubmenuClose();
    submenuCloseTimerRef.current = window.setTimeout(() => {
      setSubmenu(null);
      submenuCloseTimerRef.current = null;
    }, 220);
  };

  useEffect(
    () => () => {
      if (submenuCloseTimerRef.current !== null) window.clearTimeout(submenuCloseTimerRef.current);
    },
    []
  );

  useEffect(() => {
    if (!visible) {
      setSubmenu(null);
      return;
    }
    let active = true;
    setLoading(true);
    void Promise.all([
      loadXScienceAssistants(),
      loadXScienceComputeProviders(),
      rootFrameId ? loadXScienceSessionComputeProviders(rootFrameId) : Promise.resolve(computeSelection ?? []),
    ])
      .then(([assistants, providers, enabledProviders]) => {
        if (!active) return;
        setSpecialists(
          assistants
            .map((assistant) => ({
              id: assistant.id,
              label: resolveAssistantName(assistant, localeKey, assistant.name),
              agentId: assistant.agent_id || assistant.name,
            }))
            .filter((assistant) => assistant.agentId && assistant.agentId !== 'OPERON')
        );
        setComputeProviders(providers.filter((provider) => provider.checked && provider.name !== 'local'));
        setEnabledComputeProviders(enabledProviders.filter((provider) => provider !== 'local'));
      })
      .catch((error) => {
        console.error('Failed to load session options:', error);
        if (active) Message.error(translationRef.current('conversation.xscienceRuntime.sessionOptions.loadFailed'));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [computeSelection, localeKey, rootFrameId, visible]);

  useEffect(() => {
    if (computeSelection) setEnabledComputeProviders(computeSelection.filter((provider) => provider !== 'local'));
  }, [computeSelection]);

  useLayoutEffect(() => {
    if (!submenu) return;

    const updateSubmenuPosition = () => {
      const menuRect = menuSurfaceRef.current?.getBoundingClientRect();
      const submenuRect = submenuRef.current?.getBoundingClientRect();
      if (!menuRect || !submenuRect) return;

      const viewportWidth = window.innerWidth;
      const viewportHeight = window.innerHeight;
      const viewportInset = 12;
      const gap = 6;
      const canOpenRight = menuRect.right + gap + submenuRect.width <= viewportWidth - viewportInset;
      const canOpenLeft = menuRect.left - gap - submenuRect.width >= viewportInset;
      const preferredLeft = canOpenRight || !canOpenLeft ? menuRect.width + gap : -submenuRect.width - gap;
      const left = clampToRange(
        preferredLeft,
        viewportInset - menuRect.left,
        viewportWidth - viewportInset - submenuRect.width - menuRect.left
      );
      const top = clampToRange(
        menuRect.height - submenuRect.height,
        viewportInset - menuRect.top,
        viewportHeight - viewportInset - submenuRect.height - menuRect.top
      );
      setSubmenuPosition({ left, top });
    };

    updateSubmenuPosition();
    window.addEventListener('resize', updateSubmenuPosition);
    const resizeObserver =
      typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(updateSubmenuPosition);
    if (menuSurfaceRef.current) resizeObserver?.observe(menuSurfaceRef.current);
    if (submenuRef.current) resizeObserver?.observe(submenuRef.current);

    return () => {
      window.removeEventListener('resize', updateSubmenuPosition);
      resizeObserver?.disconnect();
    };
  }, [submenu]);

  const activeSpecialistLabel =
    value.targetAgent === 'OPERON'
      ? t('conversation.xscienceRuntime.sessionOptions.none')
      : specialists.find((specialist) => specialist.agentId === value.targetAgent)?.label || value.targetAgent;

  const computeLabel = useMemo(() => {
    if (enabledComputeProviders.length === 0) return t('conversation.xscienceRuntime.sessionOptions.localMachine');
    if (enabledComputeProviders.length === 1) {
      const selected = computeProviders.find((provider) => provider.name === enabledComputeProviders[0]);
      return selected?.displayName || enabledComputeProviders[0];
    }
    return t('conversation.xscienceRuntime.sessionOptions.providerCount', {
      count: enabledComputeProviders.length,
    });
  }, [computeProviders, enabledComputeProviders, t]);

  const updateComputeProvider = async (providerName: string, checked: boolean) => {
    if (disabled) return;
    setPendingProvider(providerName);
    try {
      if (rootFrameId) await setXScienceSessionComputeProvider(rootFrameId, providerName, checked);
      setEnabledComputeProviders((current) => {
        const next = checked
          ? Array.from(new Set([...current, providerName]))
          : current.filter((name) => name !== providerName);
        onComputeSelectionChange?.(next);
        return next;
      });
    } catch (error) {
      console.error('Failed to update session compute provider:', error);
      Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
    } finally {
      setPendingProvider(null);
    }
  };

  const selectLocalCompute = async () => {
    if (disabled || enabledComputeProviders.length === 0 || pendingProvider !== null) return;
    setPendingProvider('__local__');
    try {
      if (rootFrameId) {
        await Promise.all(
          enabledComputeProviders.map((providerName) =>
            setXScienceSessionComputeProvider(rootFrameId, providerName, false)
          )
        );
      }
      setEnabledComputeProviders([]);
      onComputeSelectionChange?.([]);
    } catch (error) {
      console.error('Failed to restore local session compute:', error);
      Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
    } finally {
      setPendingProvider(null);
    }
  };

  const updateToggle = async (key: 'delegation' | 'autoReview' | 'memory', checked: boolean) => {
    if (disabled) return;
    if (key === 'delegation') {
      onChange({ ...value, delegation: checked });
      return;
    }

    setPendingOption(key);
    try {
      await onPersistentOptionChange?.(key, checked);
      if (!onPersistentOptionChange) onChange({ ...value, [key]: checked });
    } catch (error) {
      console.error('Failed to update session option:', error);
      Message.error(t('conversation.xscienceRuntime.sessionOptions.updateFailed'));
    } finally {
      setPendingOption(null);
    }
  };

  const submenuPanel =
    submenu && (submenu !== 'permission' || hasPermissionChoices) ? (
      <div
        ref={submenuRef}
        className='app-overlay-menu composer-control-submenu absolute box-border overflow-y-auto rd-8px py-6px'
        role='menu'
        aria-label={
          submenu === 'permission'
            ? t('agentMode.switchMode')
            : submenu === 'specialist'
              ? t('conversation.xscienceRuntime.sessionOptions.expert')
              : t('conversation.xscienceRuntime.sessionOptions.compute')
        }
        style={{
          width: 'min(288px, calc(100vw - 24px))',
          maxWidth: 'calc(100vw - 24px)',
          maxHeight: 'calc(100vh - 24px)',
          opacity: 1,
          ...submenuPosition,
        }}
        onMouseEnter={cancelSubmenuClose}
        onMouseLeave={scheduleSubmenuClose}
        onKeyDown={keyboard.onSubmenuKeyDown}
      >
        {submenu === 'permission' ? (
          <XSciencePermissionChoices control={permission} />
        ) : submenu === 'specialist' ? (
          <>
            <button
              type='button'
              role='menuitemradio'
              aria-checked={value.targetAgent === 'OPERON'}
              aria-label={t('conversation.xscienceRuntime.sessionOptions.selectExpert', {
                name: t('conversation.xscienceRuntime.sessionOptions.none'),
              })}
              className={rowClass}
              disabled={disabled}
              onClick={() => onChange({ ...value, targetAgent: 'OPERON' })}
            >
              <span className='inline-flex w-20px'>{value.targetAgent === 'OPERON' ? <Check size={14} /> : null}</span>
              <span>{t('conversation.xscienceRuntime.sessionOptions.none')}</span>
            </button>
            {specialists.map((specialist) => (
              <button
                key={specialist.id}
                type='button'
                role='menuitemradio'
                aria-checked={specialist.agentId === value.targetAgent}
                aria-label={t('conversation.xscienceRuntime.sessionOptions.selectExpert', {
                  name: specialist.label,
                })}
                className={rowClass}
                disabled={disabled}
                onClick={() => onChange({ ...value, targetAgent: specialist.agentId })}
              >
                <span className='inline-flex w-20px'>
                  {specialist.agentId === value.targetAgent ? <Check size={14} /> : null}
                </span>
                <span className='truncate'>{specialist.label}</span>
              </button>
            ))}
            {!loading && specialists.length === 0 ? (
              <div className='px-12px py-8px text-12px text-t-tertiary'>
                {t('conversation.xscienceRuntime.sessionOptions.noExperts')}
              </div>
            ) : null}
            <div role='separator' className='mx-12px my-5px h-1px bg-fill-3' />
            <button
              type='button'
              role='menuitem'
              className={rowClass}
              onClick={() => {
                setVisible(false);
                setSubmenu(null);
                void navigate('/settings/experts?create=1');
              }}
            >
              {t('conversation.xscienceRuntime.sessionOptions.createExpert')}
            </button>
          </>
        ) : (
          <>
            <button
              type='button'
              role='menuitemradio'
              aria-checked={enabledComputeProviders.length === 0}
              className={`${rowClass} gap-10px`}
              disabled={disabled || pendingProvider !== null}
              onClick={() => void selectLocalCompute()}
            >
              <span className='inline-flex w-20px'>
                {enabledComputeProviders.length === 0 ? <Check size={14} /> : null}
              </span>
              <span>{t('conversation.xscienceRuntime.sessionOptions.localMachine')}</span>
            </button>
            {computeProviders.map((provider) => {
              const checked = enabledComputeProviders.includes(provider.name);
              return (
                <div key={provider.name} role='none' className={`${rowClass} cursor-default gap-10px`}>
                  <span className='min-w-0 flex-1 truncate'>{provider.displayName}</span>
                  <Switch
                    size='small'
                    role='menuitemcheckbox'
                    aria-checked={checked}
                    aria-label={t('conversation.xscienceRuntime.sessionOptions.computeNamed', {
                      name: provider.displayName,
                    })}
                    checked={checked}
                    style={checked ? checkedSwitchStyle : undefined}
                    loading={pendingProvider === provider.name}
                    disabled={disabled || (pendingProvider !== null && pendingProvider !== provider.name)}
                    onChange={(next) => void updateComputeProvider(provider.name, next)}
                  />
                </div>
              );
            })}
            {!loading && computeProviders.length === 0 ? (
              <div className='px-12px py-8px text-12px leading-18px text-t-tertiary'>
                {t('conversation.xscienceRuntime.sessionOptions.noCompute')}
              </div>
            ) : null}
            <div role='separator' className='mx-12px my-5px h-1px bg-fill-3' />
            <button
              type='button'
              role='menuitem'
              className={rowClass}
              onClick={() => {
                setVisible(false);
                setSubmenu(null);
                void navigate('/settings/compute');
              }}
            >
              {t('conversation.xscienceRuntime.sessionOptions.manageCompute')}
            </button>
          </>
        )}
      </div>
    ) : null;

  const dropdownContent = (
    <div
      ref={keyboard.bindMenu}
      className='app-overlay-menu composer-control-menu relative box-border overflow-visible rd-8px py-6px'
      role='menu'
      id={menuId}
      aria-label={t('conversation.xscienceRuntime.sessionOptions.title')}
      onClick={(event) => event.stopPropagation()}
      onMouseEnter={cancelSubmenuClose}
      onMouseLeave={scheduleSubmenuClose}
      onKeyDown={keyboard.onMenuKeyDown}
      style={{
        width: 'min(288px, calc(100vw - 24px))',
        maxWidth: 'calc(100vw - 24px)',
        maxHeight: 'calc(100vh - 24px)',
        opacity: 1,
      }}
    >
      {permission.available ? (
        <>
          {hasPermissionChoices ? (
            <button
              type='button'
              role='menuitem'
              aria-haspopup='menu'
              aria-expanded={submenu === 'permission'}
              data-testid='session-config-row-permission'
              data-submenu='permission'
              className={rowClass}
              onMouseEnter={() => {
                cancelSubmenuClose();
                setSubmenu('permission');
              }}
              onMouseLeave={() => {
                if (submenu === 'permission') scheduleSubmenuClose();
              }}
              onKeyDown={(event) => keyboard.onSubmenuTriggerKeyDown(event, 'permission')}
              onClick={() => {
                cancelSubmenuClose();
                setSubmenu('permission');
              }}
            >
              <Shield size={16} aria-hidden='true' className='shrink-0 text-t-secondary' />
              <span className='min-w-0 flex-1 truncate'>{t('agentMode.switchMode')}</span>
              <span
                className='inline-flex shrink-0 items-center gap-4px rounded-full bg-fill-2 px-8px py-3px text-11px'
                style={isWarningPermissionMode(permission.currentValue) ? { color: 'var(--warning)' } : undefined}
              >
                <XSciencePermissionIcon mode={permission.currentValue} size={12} />
                <span className='max-w-112px truncate'>
                  {permission.selectedOption?.label ?? t('agentMode.permission')}
                </span>
                <Right size={12} />
              </span>
            </button>
          ) : null}
          {permission.controller ? <SessionPermissionGrants controller={permission.controller} open={visible} /> : null}
          <div role='separator' className='mx-8px my-4px h-1px bg-fill-3' />
        </>
      ) : null}
      <XScienceAgentControlToggles
        value={value}
        pending={pendingOption}
        disabled={disabled}
        onChange={(key, next) => {
          void updateToggle(key, next);
        }}
      />
      {value.goalText ? (
        <div className='px-12px py-6px'>
          <div className='truncate text-12px text-t-secondary' title={value.goalText}>
            {t('conversation.xscienceRuntime.sessionOptions.goal', { goal: value.goalText })}
          </div>
          {onClearGoal ? (
            <button
              type='button'
              role='menuitem'
              className='mt-4px border-0 bg-transparent p-0 text-12px text-primary disabled:opacity-50'
              disabled={disabled || goalClearInFlight}
              onClick={() => void onClearGoal()}
            >
              {goalClearInFlight
                ? t('conversation.xscienceRuntime.sessionOptions.clearingGoal')
                : t('conversation.xscienceRuntime.sessionOptions.clearGoal')}
            </button>
          ) : null}
        </div>
      ) : null}
      {value.asRoutine ? (
        <div className='px-12px py-6px text-12px text-t-tertiary'>
          {t('conversation.xscienceRuntime.sessionOptions.routineUnsupported')}
        </div>
      ) : null}
      <div role='separator' className='mx-12px my-5px h-1px bg-fill-3' />
      <button
        type='button'
        role='menuitem'
        aria-haspopup='menu'
        aria-expanded={submenu === 'specialist'}
        data-testid='session-config-row-specialist'
        data-submenu='specialist'
        className={`${rowClass} justify-between`}
        onMouseEnter={() => {
          cancelSubmenuClose();
          setSubmenu('specialist');
        }}
        onMouseLeave={() => {
          if (submenu === 'specialist') scheduleSubmenuClose();
        }}
        onKeyDown={(event) => keyboard.onSubmenuTriggerKeyDown(event, 'specialist')}
        onClick={() => {
          cancelSubmenuClose();
          setSubmenu('specialist');
        }}
      >
        <UserRound size={16} aria-hidden='true' className='shrink-0 text-t-secondary' />
        <span className='min-w-0 flex-1'>{t('conversation.xscienceRuntime.sessionOptions.expert')}</span>
        <span className='flex min-w-0 items-center gap-8px text-t-tertiary'>
          <span className='max-w-88px truncate'>{activeSpecialistLabel}</span>
          <Right size={14} />
        </span>
      </button>
      <button
        type='button'
        role='menuitem'
        aria-haspopup='menu'
        aria-expanded={submenu === 'compute'}
        data-testid='session-config-row-compute'
        data-submenu='compute'
        className={`${rowClass} justify-between`}
        onMouseEnter={() => {
          cancelSubmenuClose();
          setSubmenu('compute');
        }}
        onMouseLeave={() => {
          if (submenu === 'compute') scheduleSubmenuClose();
        }}
        onKeyDown={(event) => keyboard.onSubmenuTriggerKeyDown(event, 'compute')}
        onClick={() => {
          cancelSubmenuClose();
          setSubmenu('compute');
        }}
      >
        <Server size={16} aria-hidden='true' className='shrink-0 text-t-secondary' />
        <span className='min-w-0 flex-1'>{t('conversation.xscienceRuntime.sessionOptions.compute')}</span>
        <span className='flex min-w-0 items-center gap-8px text-t-tertiary'>
          <span className='max-w-88px truncate'>{computeLabel}</span>
          <Right size={14} />
        </span>
      </button>
      {loading ? (
        <div
          className='absolute inset-x-0 bottom-0 flex h-24px items-center justify-center'
          aria-label={t('conversation.xscienceRuntime.sessionOptions.loading')}
        >
          <Spin size={14} />
        </div>
      ) : null}
      {submenuPanel}
    </div>
  );

  return (
    <>
      <Dropdown
        trigger='click'
        position='tl'
        popupVisible={visible}
        onVisibleChange={(next) => !triggerDisabled && setVisible(next)}
        droplist={dropdownContent}
      >
        <button
          ref={triggerRef}
          type='button'
          aria-label={t('conversation.xscienceRuntime.sessionOptions.title')}
          data-testid='x-science-session-options-trigger'
          aria-haspopup='menu'
          aria-expanded={visible}
          aria-controls={visible ? menuId : undefined}
          onKeyDown={(event) => keyboard.onTriggerKeyDown(event, triggerDisabled)}
          disabled={triggerDisabled}
          className='composer-icon-control relative'
        >
          <SettingConfig theme='outline' size={17} strokeWidth={3.6} />
          {value.delegation ||
          value.autoReview ||
          value.memory ||
          value.targetAgent !== 'OPERON' ||
          (permission.available && permission.currentValue !== 'default') ||
          enabledComputeProviders.length > 0 ? (
            <span className='absolute right-5px top-4px h-5px w-5px rounded-full bg-primary' aria-hidden='true' />
          ) : null}
          {permission.controller?.isCurrent() &&
          permission.grants.available &&
          Boolean(permission.grants.snapshot?.grants.length) ? (
            <span
              className='absolute -right-4px -top-4px rounded-full bg-fill-3 px-3px text-10px leading-14px'
              aria-label={t('agentMode.sessionGrants.count', { count: permission.grants.snapshot!.grants.length })}
            >
              {permission.grants.snapshot!.grants.length}
            </span>
          ) : null}
        </button>
      </Dropdown>
      <XScienceFullAccessConfirmation control={permission} />
    </>
  );
};

export default XScienceSessionOptionsMenu;
