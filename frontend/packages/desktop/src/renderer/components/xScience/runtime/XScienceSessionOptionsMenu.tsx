import {
  loadXScienceComputeProviders,
  loadXScienceSessionComputeProviders,
  setXScienceSessionComputeProvider,
  type XScienceComputeProvider,
} from '@/renderer/services/xScienceCompute';
import { X_SCIENCE_SESSION_DEFAULTS, type XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import { Dropdown, Message } from '@arco-design/web-react';
import { Right, SettingConfig } from '@icon-park/react';
import { Server, Shield, UserRound } from 'lucide-react';
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { useNestedMenuKeyboard } from '@/renderer/hooks/ui/useNestedMenuKeyboard';
import { getMenuItems } from '@/renderer/utils/menuKeyboard';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import XScienceSpecialistChoices, { specialistControlLabel } from './XScienceSpecialistChoices';
import XScienceComputeChoices, { computeSelectionLabel } from './XScienceComputeChoices';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  sessionMemoryPresentation,
  type XScienceMemoryControl,
} from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import XScienceAgentControlToggles from './XScienceAgentControlToggles';
import XSciencePermissionIcon, { isWarningPermissionMode } from './XSciencePermissionIcon';
import SessionPermissionGrants from './SessionPermissionGrants';
import { XScienceFullAccessConfirmation, XSciencePermissionChoices } from './XSciencePermissionChoices';
import { useXSciencePermissionControl, type XSciencePermissionControlInput } from './useXSciencePermissionControl';

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
  memoryControl?: XScienceMemoryControl;
  delegationControl?: XScienceDelegationControl;
  specialistControl?: XScienceSpecialistControl;
};

const rowClass =
  'composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-8px text-left font-inherit text-13px leading-none text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50';
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
  memoryControl,
  delegationControl,
  specialistControl,
}) => {
  const { t } = useTranslation();
  const memoryPresentation = sessionMemoryPresentation(memoryControl, value.memory);
  const hasMemoryCustomization =
    !memoryPresentation.disabled && memoryPresentation.checked !== X_SCIENCE_SESSION_DEFAULTS.memory;
  const navigate = useNavigate();
  const translationRef = useRef(t);
  translationRef.current = t;
  const [visible, setVisible] = useState(false);
  const refreshMemory = memoryControl?.refresh;
  const refreshSpecialist = specialistControl?.refresh;
  useEffect(() => {
    if (visible) {
      refreshMemory?.();
      void refreshSpecialist?.();
    }
  }, [refreshMemory, refreshSpecialist, visible]);
  const [submenu, setSubmenu] = useState<Submenu>(null);
  const account = getRendererAccountScopeToken();
  const computeOwner = useMemo(
    () => ({ account, rootFrameId, contextKey: permissionControl?.contextKey }),
    [account, rootFrameId, permissionControl?.contextKey]
  );
  const currentComputeOwner = useRef(computeOwner);
  currentComputeOwner.current = computeOwner;
  const computeMenuOpen = useRef(visible);
  computeMenuOpen.current = visible;
  const isComputeOwnerCurrent = useCallback(
    () => currentComputeOwner.current === computeOwner && getRendererAccountScopeToken() === account,
    [account, computeOwner]
  );
  const [computeRead, setComputeRead] = useState<{
    owner: typeof computeOwner;
    status: 'loading' | 'ready' | 'error';
    hasSnapshot: boolean;
  } | null>(null);
  const [computeRetry, setComputeRetry] = useState(0);
  const hasComputeSnapshot = computeRead?.owner === computeOwner && computeRead.hasSnapshot;
  const computeStatus = computeRead?.owner === computeOwner ? computeRead.status : 'loading';
  const computeReady = computeRead?.owner === computeOwner && computeRead.status === 'ready';
  const [pendingCompute, setPendingCompute] = useState<{ owner: typeof computeOwner; provider: string } | null>(null);
  const pendingProvider = pendingCompute?.owner === computeOwner ? pendingCompute.provider : null;
  const [pendingOption, setPendingOption] = useState<'autoReview' | 'memory' | null>(null);
  const [computeProviders, setComputeProviders] = useState<XScienceComputeProvider[]>([]);
  const [enabledComputeProviders, setEnabledComputeProviders] = useState<string[]>(
    (computeSelection ?? []).filter((provider) => provider !== 'local')
  );
  // Controlled draft intent is known before a catalog read, but never grants
  // read readiness or makes another owner's cached provider labels current.
  const displayedComputeSelection = useMemo(
    () =>
      computeSelection?.filter((provider) => provider !== 'local') ??
      (hasComputeSnapshot ? enabledComputeProviders : undefined),
    [computeSelection, hasComputeSnapshot, enabledComputeProviders]
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
  const triggerDisabled = disabled && !permission.available && !delegationControl;
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
    setComputeRead((previous) => ({
      owner: computeOwner,
      status: 'loading',
      hasSnapshot: previous?.owner === computeOwner && previous.hasSnapshot,
    }));
    void Promise.all([
      loadXScienceComputeProviders(),
      rootFrameId ? loadXScienceSessionComputeProviders(rootFrameId) : Promise.resolve(computeSelection ?? []),
    ])
      .then(([providers, enabledProviders]) => {
        if (!active || !isComputeOwnerCurrent()) return;
        setComputeProviders(providers.filter((provider) => provider.checked && provider.name !== 'local'));
        setEnabledComputeProviders(enabledProviders.filter((provider) => provider !== 'local'));
        setComputeRead({ owner: computeOwner, status: 'ready', hasSnapshot: true });
      })
      .catch((error) => {
        if (!active || !isComputeOwnerCurrent()) return;
        console.error('Failed to load session options:', error);
        setComputeRead((previous) => ({
          owner: computeOwner,
          status: 'error',
          hasSnapshot: previous?.owner === computeOwner && previous.hasSnapshot,
        }));
        Message.error(translationRef.current('conversation.xscienceRuntime.sessionOptions.computeState.read'));
      });
    return () => {
      active = false;
    };
  }, [computeSelection, rootFrameId, visible, computeOwner, computeRetry, isComputeOwnerCurrent]);

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

  const activeSpecialistLabel = specialistControlLabel(specialistControl, t);

  const computeLabel = computeSelectionLabel(displayedComputeSelection, hasComputeSnapshot ? computeProviders : [], t);

  const updateComputeProvider = async (providerName: string, checked: boolean) => {
    if (disabled || !computeReady || pendingProvider !== null || !isComputeOwnerCurrent()) return;
    setPendingCompute({ owner: computeOwner, provider: providerName });
    try {
      if (rootFrameId) await setXScienceSessionComputeProvider(rootFrameId, providerName, checked);
      if (!isComputeOwnerCurrent()) return;
      setEnabledComputeProviders((current) => {
        const next = checked
          ? Array.from(new Set([...current, providerName]))
          : current.filter((name) => name !== providerName);
        onComputeSelectionChange?.(next);
        return next;
      });
    } catch (error) {
      if (!isComputeOwnerCurrent()) return;
      console.error('Failed to update session compute provider:', error);
      Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
    } finally {
      if (isComputeOwnerCurrent()) setPendingCompute(null);
    }
  };

  const selectLocalCompute = async () => {
    if (
      disabled ||
      !computeReady ||
      enabledComputeProviders.length === 0 ||
      pendingProvider !== null ||
      !isComputeOwnerCurrent()
    )
      return;
    setPendingCompute({ owner: computeOwner, provider: '__local__' });
    try {
      if (rootFrameId) {
        await Promise.all(
          enabledComputeProviders.map((providerName) =>
            setXScienceSessionComputeProvider(rootFrameId, providerName, false)
          )
        );
      }
      if (!isComputeOwnerCurrent()) return;
      setEnabledComputeProviders([]);
      onComputeSelectionChange?.([]);
    } catch (error) {
      if (!isComputeOwnerCurrent()) return;
      console.error('Failed to restore local session compute:', error);
      Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
    } finally {
      if (isComputeOwnerCurrent()) setPendingCompute(null);
    }
  };

  const updateToggle = async (key: 'autoReview' | 'memory', checked: boolean) => {
    if (disabled || (key === 'memory' && !memoryControl?.canUpdate())) return;

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
          <XScienceSpecialistChoices
            control={specialistControl}
            onCreate={() => {
              setVisible(false);
              setSubmenu(null);
              void navigate('/settings/experts?create=1');
            }}
          />
        ) : (
          <XScienceComputeChoices
            providers={hasComputeSnapshot ? computeProviders : []}
            enabled={displayedComputeSelection ?? []}
            status={computeStatus}
            hasSelection={displayedComputeSelection !== undefined}
            disabled={disabled}
            pending={pendingProvider}
            retry={() => {
              if (computeMenuOpen.current && isComputeOwnerCurrent() && computeStatus === 'error') {
                setComputeRetry((attempt) => attempt + 1);
              }
            }}
            selectLocal={() => void selectLocalCompute()}
            change={(name, checked) => void updateComputeProvider(name, checked)}
            manage={() => {
              setVisible(false);
              setSubmenu(null);
              void navigate('/settings/compute');
            }}
          />
        )}
      </div>
    ) : null;

  const dropdownContent = (
    <div
      ref={keyboard.bindMenu}
      className='app-overlay-menu composer-control-menu relative box-border overflow-visible rd-8px py-6px'
      role='menu'
      id={menuId}
      tabIndex={-1}
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
        memoryControl={memoryControl}
        delegationControl={delegationControl}
        active={visible}
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
          {(delegationControl?.current() && delegationControl.enabled === false) ||
          value.autoReview ||
          hasMemoryCustomization ||
          Boolean(specialistControl?.current() && specialistControl.applied && !specialistControl.applied.is_default) ||
          (permission.available && permission.currentValue !== 'default') ||
          Boolean(displayedComputeSelection?.length) ? (
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
