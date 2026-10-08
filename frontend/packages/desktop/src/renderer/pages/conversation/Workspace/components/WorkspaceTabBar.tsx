/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Dropdown } from '@arco-design/web-react';
import Tabs from '@/renderer/components/base/WorkbenchTabs';
import { handleTabListKeyDown } from '@/renderer/utils/tabListKeyboard';
import { BranchOne, ChartHistogram, FolderClose } from '@icon-park/react';
import type { TFunction } from 'i18next';
import React from 'react';
import type { WorkspaceTab } from '../types';

type WorkspaceTabBarProps = {
  t: TFunction;
  activeTab: WorkspaceTab;
  onTabChange: (tab: WorkspaceTab) => void;
  changeCount: number;
  branch: string | null;
  readOnly?: boolean;
  showCompute?: boolean;
};

const WorkspaceTabBar: React.FC<WorkspaceTabBarProps> = ({
  t,
  activeTab,
  onTabChange,
  changeCount,
  branch,
  readOnly = false,
  showCompute = false,
}) => {
  const changesTitle = (
    <span className='flex items-center'>
      {t('conversation.workspace.changes.tab')}
      {changeCount > 0 && <span className='ml-2px text-t-tertiary'>({changeCount > 99 ? '99+' : changeCount})</span>}
    </span>
  );

  const branchIcon = (
    <span className='flex items-center text-t-tertiary mx-8px hover:text-t-secondary transition-colors cursor-pointer'>
      <BranchOne size={16} className='shrink-0' />
    </span>
  );

  // Branches are read-only (no checkout support yet) — clicking the icon
  // surfaces just the current branch name instead of an unactionable list.
  const branchDropdown = branch ? (
    <Dropdown
      trigger='click'
      position='bl'
      droplist={
        <div
          className='rounded-6px px-12px py-8px shadow-lg text-12px text-t-primary'
          style={{
            maxWidth: 320,
            background: 'var(--color-bg-popup)',
            border: '1px solid var(--color-border)',
          }}
        >
          <div className='text-t-tertiary mb-2px'>{t('conversation.workspace.changes.currentBranchLabel')}</div>
          <div className='font-medium break-all'>{branch}</div>
        </div>
      }
    >
      {branchIcon}
    </Dropdown>
  ) : null;

  if (readOnly && showCompute) {
    const tabs: Array<{
      key: Extract<WorkspaceTab, 'files' | 'compute'>;
      label: string;
      icon: React.ReactNode;
    }> = [
      {
        key: 'files',
        label: t('conversation.workspace.changes.filesTab'),
        icon: <FolderClose theme='outline' size={13} />,
      },
      {
        key: 'compute',
        label: t('conversation.synonRuntime.sessionOptions.compute'),
        icon: <ChartHistogram theme='outline' size={13} />,
      },
    ];
    return (
      <div
        className='synon-workspace-tablist flex h-44px shrink-0 items-center border-b border-solid border-[var(--color-border-2)] px-8px'
        role='tablist'
        aria-label={t('conversation.synonRuntime.computeRuntime.title')}
        onKeyDown={handleTabListKeyDown}
      >
        {tabs.map((tab) => (
          <button
            key={tab.key}
            type='button'
            role='tab'
            aria-selected={activeTab === tab.key}
            tabIndex={activeTab === tab.key ? 0 : -1}
            data-state={activeTab === tab.key ? 'selected' : 'idle'}
            data-testid={`workspace-tab-${tab.key}`}
            className={`synon-workspace-tab ${activeTab === tab.key ? 'synon-workspace-tab--selected' : ''}`}
            onClick={() => onTabChange(tab.key)}
          >
            {tab.icon}
            {tab.label}
          </button>
        ))}
      </div>
    );
  }

  return (
    <Tabs
      aria-label={t('common.workspace')}
      activeTab={activeTab}
      onChange={(key) => onTabChange(key as WorkspaceTab)}
      type='line'
      size='small'
      className='px-12px [&_.arco-tabs-nav]:border-b-0 [&_.arco-tabs-header-title]:!mr-8px'
      extra={readOnly ? null : branchDropdown}
    >
      <Tabs.TabPane key='files' title={t('conversation.workspace.changes.filesTab')} />
      {!readOnly && <Tabs.TabPane key='changes' title={changesTitle} />}
    </Tabs>
  );
};

export default WorkspaceTabBar;
