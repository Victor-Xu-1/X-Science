import React from 'react';
import type { TFunction } from 'i18next';
import { BookMarked, ChartNoAxesCombined, Folder, FolderOpen, Loader2, Shield } from 'lucide-react';
import type { MobileActionSheetEntry } from './types';
import ComposerHostFiles from '../SendBox/hostFiles/ComposerHostFiles';

export type ComposerPlusActions = {
  onSelectProjectFiles?: () => void;
  onLocalFilesAdded?: (files: { path: string }[]) => void;
  onViewPlan?: () => void;
  planProgress?: { completed: number; total: number };
  onRequestReview?: () => void;
  reviewInFlight?: boolean;
  reviewDisabled?: boolean;
  onSaveSkill?: () => void;
  saveAsSkillDisabled?: boolean;
  saveAsSkillInFlight?: boolean;
  onViewContextWindow?: (returnFocus: HTMLButtonElement | null) => void;
};

/** Bind a native opener to its existing composer ref, without owning presentation state. */
export function bindComposerPlusOpener(
  opener: { current: HTMLButtonElement | null },
  open: () => void
): (trigger?: HTMLButtonElement) => void {
  return (trigger) => {
    opener.current = trigger ?? null;
    open();
  };
}

/** Presentation only: every effect remains owned by the current composer. */
export function buildComposerPlusEntries({
  actions,
  contextReturnFocus,
  t,
}: {
  actions: ComposerPlusActions;
  contextReturnFocus: () => HTMLButtonElement | null;
  t: TFunction;
}): MobileActionSheetEntry[] {
  const entries: MobileActionSheetEntry[] = [];
  if (actions.onSelectProjectFiles) {
    entries.push({
      key: 'project-files',
      icon: <FolderOpen size={16} />,
      label: t('conversation.hostFiles.projectFiles'),
      onClick: actions.onSelectProjectFiles,
    });
  }
  if (actions.onLocalFilesAdded) {
    entries.push({
      key: 'your-files',
      icon: <Folder size={16} />,
      label: t('conversation.attachMenu.yourFiles'),
      submenu: {
        title: t('conversation.attachMenu.yourFiles'),
        options: [],
        onSelect: () => {},
        footer: (active) => (
          <ComposerHostFiles
            menuOpen={active}
            presentation='inline'
            onReference={(file) => actions.onLocalFilesAdded?.([file])}
          />
        ),
      },
    });
  }
  if (actions.onViewPlan) {
    entries.push({
      key: 'view-plan',
      label: t('conversation.attachMenu.viewPlan'),
      meta: actions.planProgress ? `${actions.planProgress.completed}/${actions.planProgress.total}` : undefined,
      onClick: actions.onViewPlan,
    });
  }
  if (actions.onRequestReview) {
    entries.push({
      key: 'manual-review',
      icon: <Shield size={16} />,
      label: t(actions.reviewInFlight ? 'conversation.attachMenu.reviewing' : 'conversation.attachMenu.manualReview'),
      description: t(
        actions.reviewInFlight
          ? 'conversation.attachMenu.reviewingDescription'
          : actions.reviewDisabled
            ? 'conversation.attachMenu.reviewUnavailableDuringTask'
            : 'conversation.attachMenu.manualReviewDescription'
      ),
      busy: actions.reviewInFlight,
      disabled: actions.reviewDisabled || actions.reviewInFlight,
      onClick: actions.onRequestReview,
    });
  }
  if (actions.onSaveSkill) {
    entries.push({
      key: 'save-skill',
      icon: actions.saveAsSkillInFlight ? (
        <Loader2 size={16} className='conversation-control-menu__spinner' />
      ) : (
        <BookMarked size={16} />
      ),
      label: t(
        actions.saveAsSkillInFlight
          ? 'conversation.attachMenu.saveAsSkillTaskLabel'
          : 'conversation.attachMenu.saveAsSkill'
      ),
      description: t(
        actions.saveAsSkillInFlight
          ? 'conversation.attachMenu.saveAsSkillTaskRunning'
          : actions.saveAsSkillDisabled
            ? 'conversation.attachMenu.saveAsSkillBusyDescription'
            : 'conversation.attachMenu.saveAsSkillDescription'
      ),
      busy: actions.saveAsSkillInFlight,
      disabled: actions.saveAsSkillDisabled,
      keepDisabledFocusable: true,
      onClick: actions.onSaveSkill,
    });
  }
  if (actions.onViewContextWindow) {
    entries.push({
      key: 'context-window',
      icon: <ChartNoAxesCombined size={16} />,
      label: t('conversation.contextUsage.title'),
      onClick: () => actions.onViewContextWindow?.(contextReturnFocus()),
    });
  }
  return entries;
}
