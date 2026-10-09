import WorkspacePanelHeader from './WorkspacePanelHeader';
import { WORKSPACE_HEADER_HEIGHT } from '@/renderer/pages/conversation/utils/layoutCalc';
import { dispatchWorkspaceToggleEvent } from '@/renderer/utils/workspace/workspaceEvents';
import { containTabFocus, focusableElements, restoreScopedFocus } from '@/renderer/utils/focusScope';
import React, { useLayoutEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';

type MobileWorkspaceOverlayProps = {
  rightSiderCollapsed: boolean;
  setRightSiderCollapsed: (collapsed: boolean) => void;
  workspaceWidthPx: number;
  mobileWorkspaceHandleRight: number;
  sider: React.ReactNode;
  workspacePath?: string;
  isTemporaryWorkspace?: boolean;
};

// Full-screen overlay + fixed workspace panel + floating collapse handle for mobile viewports
const MobileWorkspaceOverlay: React.FC<MobileWorkspaceOverlayProps> = ({
  rightSiderCollapsed,
  setRightSiderCollapsed,
  workspaceWidthPx,
  mobileWorkspaceHandleRight,
  sider,
  workspacePath,
  isTemporaryWorkspace,
}) => {
  const { t } = useTranslation();
  const panelRef = useRef<HTMLDivElement>(null);
  const openerRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const root = panelRef.current;
    if (rightSiderCollapsed) {
      // Run after React restores selection for the retained DOM, not during
      // the old effect's mutation-phase cleanup where it can be overwritten.
      restoreScopedFocus(openerRef.current, root);
      openerRef.current = null;
      return;
    }
    const current = document.activeElement;
    openerRef.current =
      current instanceof HTMLElement && current !== document.body && !root?.contains(current) ? current : null;
    (focusableElements(root)[0] ?? root)?.focus();
  }, [rightSiderCollapsed]);
  useLayoutEffect(() => {
    const root = panelRef.current;
    return () => restoreScopedFocus(openerRef.current, root);
  }, []);
  return (
    <>
      {/* Backdrop */}
      {!rightSiderCollapsed && (
        <div
          className='workbench-modal-backdrop fixed inset-0 z-90'
          onClick={() => setRightSiderCollapsed(true)}
          aria-hidden='true'
        />
      )}

      {/* Fixed workspace panel */}
      <div
        ref={panelRef}
        role={rightSiderCollapsed ? undefined : 'dialog'}
        aria-modal={!rightSiderCollapsed || undefined}
        aria-label={t('common.workspace')}
        aria-hidden={rightSiderCollapsed || undefined}
        inert={rightSiderCollapsed || undefined}
        tabIndex={-1}
        className='!bg-1 relative chat-layout-right-sider'
        onKeyDown={(event) => {
          if (rightSiderCollapsed || event.defaultPrevented || !event.currentTarget.contains(event.target as Node))
            return;
          if (event.key === 'Escape') {
            event.preventDefault();
            setRightSiderCollapsed(true);
          } else containTabFocus(panelRef.current, event);
        }}
        style={{
          position: 'fixed',
          right: 0,
          top: 0,
          height: '100dvh',
          width: `${Math.round(workspaceWidthPx)}px`,
          zIndex: 100,
          transform: rightSiderCollapsed ? 'translateX(100%)' : 'none',
          transition: 'none',
          pointerEvents: rightSiderCollapsed ? 'none' : 'auto',
        }}
      >
        <WorkspacePanelHeader
          showToggle
          collapsed={rightSiderCollapsed}
          onToggle={() => dispatchWorkspaceToggleEvent()}
          togglePlacement='left'
          workspacePath={workspacePath}
          isTemporaryWorkspace={isTemporaryWorkspace}
        />
        <div className='arco-layout-content bg-1' style={{ height: `calc(100% - ${WORKSPACE_HEADER_HEIGHT}px)` }}>
          {sider}
        </div>
      </div>

      {/* Floating collapse handle */}
      {!rightSiderCollapsed && (
        <button
          type='button'
          className='fixed z-101 flex items-center justify-center transition-colors workspace-toggle-floating'
          style={{
            top: '50%',
            right: `${mobileWorkspaceHandleRight}px`,
            transform: 'translateY(-50%)',
            width: '28px',
            height: '64px',
            borderTopLeftRadius: '10px',
            borderBottomLeftRadius: '10px',
            borderTopRightRadius: '0',
            borderBottomRightRadius: '0',
            borderRight: 'none',
            backgroundColor: 'var(--bg-2)',
            boxShadow: '0 8px 20px rgba(0, 0, 0, 0.12)',
          }}
          onClick={() => dispatchWorkspaceToggleEvent()}
          aria-label={t('common.collapseWorkspace')}
        >
          <span className='flex flex-col items-center justify-center gap-5px text-t-secondary'>
            <span className='block w-8px h-2px rd-999px bg-current opacity-85'></span>
            <span className='block w-8px h-2px rd-999px bg-current opacity-65'></span>
            <span className='block w-8px h-2px rd-999px bg-current opacity-45'></span>
          </span>
        </button>
      )}
    </>
  );
};

export default MobileWorkspaceOverlay;
