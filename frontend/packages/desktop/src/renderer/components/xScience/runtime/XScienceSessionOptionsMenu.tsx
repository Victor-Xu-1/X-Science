import {
  X_SCIENCE_SESSION_DEFAULTS,
  type XScienceSessionOptions,
  type XScienceSessionPreferenceControl,
} from '@/renderer/services/xScienceSessionOptions';
import { Dropdown, Message } from '@arco-design/web-react';
import { Right, SettingConfig } from '@icon-park/react';
import { Server, Shield, UserRound } from 'lucide-react';
import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { useNestedMenuKeyboard } from '@/renderer/hooks/ui/useNestedMenuKeyboard';
import { getMenuItems } from '@/renderer/utils/menuKeyboard';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import XScienceSpecialistChoices, { specialistControlLabel } from './XScienceSpecialistChoices';
import XScienceComputeChoices, { computeSelectionLabel } from './XScienceComputeChoices';
import type { SessionComputeControl } from '@/renderer/hooks/xScience/runtime/useSessionComputeControls';
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
  value: XScienceSessionOptions;
  onChange: (next: XScienceSessionOptions) => void;
  onPersistentOptionChange?: (key: 'autoReview' | 'memory', value: boolean) => void | Promise<void>;
  computeControl?: SessionComputeControl;
  onClearGoal?: () => void | Promise<void>;
  goalClearInFlight?: boolean;
  disabled?: boolean;
  memoryDisabled?: boolean;
  autoReviewDisabled?: boolean;
  autoReviewControl?: XScienceSessionPreferenceControl;
  stagedAutoReview?: boolean;
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
  value,
  onChange,
  onPersistentOptionChange,
  computeControl,
  onClearGoal,
  goalClearInFlight = false,
  disabled = false,
  memoryDisabled = disabled,
  autoReviewDisabled = disabled,
  autoReviewControl,
  stagedAutoReview,
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
  const [visible, setVisible] = useState(false);
  const refreshMemory = memoryControl?.refresh;
  const refreshReview = autoReviewControl?.refresh;
  const refreshSpecialist = specialistControl?.refresh;
  useEffect(() => {
    if (visible) {
      refreshMemory?.();
      refreshReview?.();
      void refreshSpecialist?.();
    }
  }, [refreshMemory, refreshReview, refreshSpecialist, visible]);
  const [submenu, setSubmenu] = useState<Submenu>(null);
  useEffect(() => {
    if (!visible) setSubmenu(null);
  }, [visible]);
  const [pendingOption, setPendingOption] = useState<'autoReview' | 'memory' | null>(null);
  const inspectCompute = computeControl?.inspect;
  useEffect(() => (visible ? inspectCompute?.() : undefined), [visible, inspectCompute]);
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
  const triggerDisabled =
    disabled &&
    !permission.available &&
    !delegationControl &&
    !memoryControl?.preference?.current() &&
    !autoReviewControl?.current();
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

  const computeLabel = computeSelectionLabel(computeControl?.selection, computeControl?.providers ?? [], t);

  const updateToggle = async (key: 'autoReview' | 'memory', checked: boolean) => {
    if (
      (key === 'memory' ? memoryDisabled : autoReviewDisabled) ||
      (key === 'memory' && !memoryControl?.canUpdate()) ||
      (key === 'autoReview' &&
        autoReviewControl &&
        (!autoReviewControl.current() || autoReviewControl.status !== 'ready'))
    )
      return;

    // The session owner owns Memory operation identity across close/reopen and account changes.
    const ownedPreference = key === 'memory' ? memoryControl?.preference : autoReviewControl;
    if (!ownedPreference) setPendingOption(key);
    try {
      await onPersistentOptionChange?.(key, checked);
      if (!onPersistentOptionChange) onChange({ ...value, [key]: checked });
    } catch (error) {
      console.error('Failed to update session option');
      Message.error(t('conversation.xscienceRuntime.sessionOptions.updateFailed'));
    } finally {
      if (!ownedPreference) setPendingOption(null);
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
            state={computeControl?.sessionState}
            draft={!!computeControl && computeControl.rootFrameId === undefined}
            providers={computeControl?.providers ?? []}
            enabled={computeControl?.enabled ?? []}
            status={computeControl?.status ?? 'loading'}
            hasSelection={computeControl?.selection !== undefined}
            disabled={disabled || !computeControl || computeControl.disabled}
            pending={computeControl?.pending ?? null}
            retry={() => computeControl?.retry()}
            selectLocal={() => void computeControl?.selectLocal()}
            change={(name, checked) => void computeControl?.change(name, checked)}
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
        autoReviewControl={autoReviewControl}
        stagedAutoReview={stagedAutoReview}
        delegationControl={delegationControl}
        active={visible}
        value={value}
        pending={memoryPresentation.pending ? 'memory' : autoReviewControl?.pending ? 'autoReview' : pendingOption}
        disabled={disabled}
        memoryDisabled={memoryDisabled}
        autoReviewDisabled={autoReviewDisabled}
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
          (stagedAutoReview ??
            (autoReviewControl ? autoReviewControl.status === 'ready' && value.autoReview : value.autoReview)) ||
          hasMemoryCustomization ||
          Boolean(specialistControl?.current() && specialistControl.applied && !specialistControl.applied.is_default) ||
          (permission.available && permission.currentValue !== 'default') ||
          Boolean(computeControl?.selection?.length) ? (
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
