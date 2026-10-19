import type { IConversationMcpStatus } from '@/common/config/storage';
import type { AcpModelInfo } from '@/common/types/platform/acpTypes';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import type { AcpDerivedOption, useAcpConfigOptions } from '@/renderer/hooks/xScience/runtime/useAcpConfigOptions';
import {
  type MobileActionSheetEntry,
  type MobileActionSheetOption,
} from '@/renderer/components/chat/MobileActionSheet';
import type { XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import XScienceDelegationControlRow from '@/renderer/components/xScience/runtime/XScienceDelegationControlRow';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import XScienceSpecialistChoices, {
  specialistControlLabel,
  specialistChoiceRows,
} from '@/renderer/components/xScience/runtime/XScienceSpecialistChoices';
import { Message } from '@arco-design/web-react';
import { Attention, Brain, MagicHat, SettingTwo, Shield } from '@icon-park/react';
import type { TFunction, i18n as I18n } from 'i18next';
import React, { useMemo } from 'react';
import { configErrorMessageKey, safeErrorDiagnostic } from './acpSendBoxDiagnostics';
import SessionPermissionGrants from '@/renderer/components/xScience/runtime/SessionPermissionGrants';
import type { SessionPermissionGrantsController } from '@/renderer/services/runtime/sessionPermissionGrantsController';
import { useSessionPermissionGrantsView } from '@/renderer/hooks/xScience/runtime/useSessionPermissionGrants';
import {
  buildSessionMemoryOption,
  type XScienceMemoryControl,
} from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';

type UseAcpMobileActionSheetControllerArgs = {
  isMobile: boolean;
  runtimeMode: AcpDerivedOption | null;
  currentMode?: string;
  runtimeThoughtLevel: AcpDerivedOption | null;
  runtimeConfig: Pick<ReturnType<typeof useAcpConfigOptions>, 'setStatus'>;
  grantsController?: SessionPermissionGrantsController | null;
  canSwitchModel: boolean;
  model_info: AcpModelInfo | null;
  isModelListLoading: boolean;
  modelListError: unknown;
  retryModelList: () => Promise<void>;
  selectModel: (modelId: string) => void;
  xscienceSessionOptions: XScienceSessionOptions;
  sessionOptionsReady: boolean;
  updatePersistentSessionOption: (key: 'autoReview' | 'memory', value: boolean) => Promise<void>;
  memoryControl?: XScienceMemoryControl;
  delegationControl?: XScienceDelegationControl;
  specialistControl?: XScienceSpecialistControl;
  mobileComputeProviders: Array<{ name: string; label: string }>;
  mobileEnabledCompute: string[];
  handleSessionComputeChange: (providerName: string, checked: boolean) => Promise<void>;
  handleSheetModeChange: (mode: string) => Promise<void>;
  handleThoughtLevelSetOption: (optionId: string, value: string) => Promise<boolean>;
  attachEntries: MobileActionSheetEntry[];
  loadedSkills: string[];
  librarySkillNames?: string[];
  loadedMcpStatuses: IConversationMcpStatus[];
  selectedSkillNames: string[];
  selectedMcpServerIds: string[];
  onSelectSkill: (name: string) => void;
  onEnableSkill?: (name: string) => void;
  onSelectMcpServer: (server: IConversationMcpStatus) => void;
  t: TFunction;
  i18n: I18n;
};

export const useAcpMobileActionSheetController = ({
  isMobile,
  runtimeMode,
  currentMode,
  runtimeThoughtLevel,
  runtimeConfig,
  grantsController,
  canSwitchModel,
  model_info,
  isModelListLoading,
  modelListError,
  retryModelList,
  selectModel,
  xscienceSessionOptions,
  sessionOptionsReady,
  updatePersistentSessionOption,
  memoryControl,
  delegationControl,
  specialistControl,
  mobileComputeProviders,
  mobileEnabledCompute,
  handleSessionComputeChange,
  handleSheetModeChange,
  handleThoughtLevelSetOption,
  attachEntries,
  loadedSkills,
  librarySkillNames = [],
  loadedMcpStatuses,
  selectedSkillNames,
  selectedMcpServerIds,
  onSelectSkill,
  onEnableSkill,
  onSelectMcpServer,
  t,
  i18n,
}: UseAcpMobileActionSheetControllerArgs): MobileActionSheetEntry[] => {
  const grantsView = useSessionPermissionGrantsView(grantsController);
  const grantsCount =
    grantsController?.isCurrent() && grantsView.available ? (grantsView.snapshot?.grants.length ?? 0) : 0;
  const sheetEntries = useMemo<MobileActionSheetEntry[]>(() => {
    if (!isMobile) return [];

    const availableModes =
      runtimeMode?.options.map((item) => ({
        value: item.value,
        label: item.label,
        description: item.description ?? undefined,
      })) ?? [];
    const modeOptions: MobileActionSheetOption[] = availableModes.map((mode) => ({
      key: mode.value,
      label: i18n.exists(`agentMode.${mode.value}`) ? t(`agentMode.${mode.value}`) : mode.label,
      description: mode.description,
      active: (runtimeMode?.currentValue ?? currentMode) === mode.value,
      disabled: runtimeConfig.setStatus.state === 'setting',
    }));

    const modelOptions: MobileActionSheetOption[] = canSwitchModel
      ? (model_info?.available_models ?? []).map((model) => ({
          key: model.id,
          label: model.label || model.id,
          description: model.description,
          active: model_info?.current_model_id === model.id,
        }))
      : [];

    const currentModelLabel =
      model_info?.current_model_label || model_info?.current_model_id || t('conversation.welcome.useCliModel');
    const currentModeLabel = modeOptions.find((opt) => opt.active)?.label ?? t('agentMode.default');

    const entries: MobileActionSheetEntry[] = [];

    entries.push({
      key: 'session-options',
      icon: <SettingTwo theme='outline' size='16' />,
      label: t('conversation.xscienceRuntime.sessionOptions.title'),
      meta: [
        delegationControl?.current() && delegationControl.enabled === false
          ? t('conversation.xscienceRuntime.sessionOptions.delegation')
          : '',
        xscienceSessionOptions.autoReview ? t('conversation.xscienceRuntime.sendBox.reviewShort') : '',
      ]
        .filter(Boolean)
        .join(' · '),
      submenu: {
        title: t('conversation.xscienceRuntime.sessionOptions.title'),
        options: [
          {
            key: 'autoReview',
            label: t('conversation.xscienceRuntime.sessionOptions.autoReview'),
            description: t('conversation.xscienceRuntime.sessionOptions.autoReviewDescription'),
            active: xscienceSessionOptions.autoReview,
            disabled: !sessionOptionsReady,
          },
          buildSessionMemoryOption(memoryControl, xscienceSessionOptions.memory, t),
        ],
        onSelect: (key) => {
          if (!sessionOptionsReady) return;
          if (key === 'autoReview' || key === 'memory') {
            if (key === 'memory' && !memoryControl?.canUpdate()) return;
            const nextValue = !xscienceSessionOptions[key];
            void updatePersistentSessionOption(key, nextValue).catch((error) => {
              console.error('[AcpSendBox] Failed to update session option', safeErrorDiagnostic(error));
              Message.error(t('conversation.xscienceRuntime.sessionOptions.updateFailed'));
            });
          }
        },
        footer: (active) => <XScienceDelegationControlRow control={delegationControl} active={active} />,
      },
    });

    entries.push({
      key: 'specialist',
      icon: <Brain theme='outline' size='16' />,
      label: t('conversation.xscienceRuntime.sessionOptions.expert'),
      meta: specialistControlLabel(specialistControl, t),
      submenu: {
        title: t('conversation.xscienceRuntime.sessionOptions.expert'),
        options: specialistChoiceRows(specialistControl, t),
        onSelect: (key) => {
          const choice = specialistChoiceRows(specialistControl, t).find((row) => row.key === key);
          if (choice && !choice.disabled) void specialistControl?.change(choice.name);
        },
        footer: (active) => <XScienceSpecialistChoices control={specialistControl} active={active} statusOnly />,
      },
    });

    if (mobileComputeProviders.length > 0) {
      entries.push({
        key: 'session-compute',
        icon: <SettingTwo theme='outline' size='16' />,
        label: t('conversation.xscienceRuntime.sessionOptions.compute'),
        meta: String(mobileEnabledCompute.length),
        submenu: {
          title: t('conversation.xscienceRuntime.sessionOptions.compute'),
          options: mobileComputeProviders.map((provider) => ({
            key: provider.name,
            label: provider.label,
            active: mobileEnabledCompute.includes(provider.name),
          })),
          onSelect: (providerName) => {
            const checked = !mobileEnabledCompute.includes(providerName);
            void handleSessionComputeChange(providerName, checked);
          },
        },
      });
    }

    if (modelListError) {
      const authenticationError = isBackendHttpError(modelListError) && [401, 403].includes(modelListError.status);
      entries.push({
        key: 'model',
        icon: <Attention theme='outline' size='16' />,
        label: authenticationError
          ? t('conversation.welcome.modelListAuthError')
          : t('conversation.welcome.modelListUnavailable'),
        description: authenticationError
          ? t('conversation.welcome.modelListAuthErrorHint')
          : t('conversation.welcome.modelListUnavailableHint'),
        meta: t('common.retry'),
        onClick: () => void retryModelList().catch(() => {}),
      });
    } else if (isModelListLoading && !model_info) {
      entries.push({
        key: 'model',
        icon: <Brain theme='outline' size='16' />,
        label: t('conversation.welcome.modelListLoading'),
        disabled: true,
      });
    } else if (modelOptions.length > 0) {
      entries.push({
        key: 'model',
        icon: <Brain theme='outline' size='16' />,
        label: t('common.model'),
        meta: currentModelLabel,
        submenu: {
          title: t('common.model'),
          options: modelOptions,
          onSelect: (id) => selectModel(id),
        },
      });
    } else {
      // Keep the model identity visible on compact layouts even when the
      // provider exposes no switchable alternatives. Desktop already shows
      // this same read-only identity; omitting it from mobile made the two
      // layouts disagree about the active runtime.
      entries.push({
        key: 'model',
        icon: <Brain theme='outline' size='16' />,
        label: t('common.model'),
        meta: currentModelLabel,
        disabled: true,
      });
    }

    if (runtimeThoughtLevel) {
      entries.push({
        key: 'thought-level',
        icon: <Brain theme='outline' size='16' />,
        label: t('agent.thoughtLevel.label'),
        meta:
          runtimeThoughtLevel.options.find((item) => item.value === runtimeThoughtLevel.currentValue)?.label ||
          runtimeThoughtLevel.currentValue ||
          '',
        submenu: {
          title: t('agent.thoughtLevel.label'),
          options: runtimeThoughtLevel.options.map((item) => ({
            key: item.value,
            label: item.label,
            description: item.description ?? undefined,
            active: runtimeThoughtLevel.currentValue === item.value,
          })),
          onSelect: (value) => {
            void handleThoughtLevelSetOption(runtimeThoughtLevel.id, value)
              .then((applied) => {
                if (applied) Message.success(t('agent.thoughtLevel.switchSuccess'));
              })
              .catch((error) => Message.error(t(configErrorMessageKey(error))));
          },
        },
      });
    }

    if (modeOptions.length > 0 || grantsController) {
      entries.push({
        key: 'send-mode',
        icon: <Shield theme='outline' size='16' />,
        label: t('conversation.xscienceRuntime.sendBox.sendMode'),
        description: t('conversation.xscienceRuntime.sendBox.sendModeDescription'),
        meta: grantsCount
          ? `${currentModeLabel} · ${t('agentMode.sessionGrants.count', { count: grantsCount })}`
          : currentModeLabel,
        disabled: runtimeConfig.setStatus.state === 'setting' && !grantsController,
        submenu: {
          title: t('conversation.xscienceRuntime.sendBox.sendMode'),
          options: modeOptions,
          onSelect: (key) => void handleSheetModeChange(key),
          footer: grantsController
            ? (active) => <SessionPermissionGrants controller={grantsController} open={active} />
            : undefined,
        },
      });
    }

    attachEntries.forEach((entry, idx) => {
      entries.push({
        ...entry,
        dividerBefore: idx === 0 ? entries.length > 0 : false,
      });
    });

    const admittedSkills = new Set(loadedSkills.map((name) => name.toLowerCase()));
    const allSkills = [...loadedSkills];
    const seenSkills = new Set(admittedSkills);
    if (onEnableSkill) {
      for (const name of librarySkillNames) {
        if (!seenSkills.has(name.toLowerCase())) {
          allSkills.push(name);
          seenSkills.add(name.toLowerCase());
        }
      }
    }
    if (allSkills.length > 0) {
      const skillOptions: MobileActionSheetOption[] = allSkills.map((name) => ({
        key: name,
        label: name,
        active: selectedSkillNames.includes(name),
        description: admittedSkills.has(name.toLowerCase()) ? undefined : t('conversation.attachMenu.enableSkillShort'),
      }));
      entries.push({
        key: 'skills',
        icon: <MagicHat theme='outline' size='16' />,
        label: t('common.skills'),
        variant: 'muted',
        submenu: {
          title: t('common.skills'),
          options: skillOptions,
          onSelect: (name) => {
            if (admittedSkills.has(name.toLowerCase())) onSelectSkill(name);
            else onEnableSkill?.(name);
          },
        },
      });
    }

    if (loadedMcpStatuses.length > 0) {
      const mcpOptions: MobileActionSheetOption[] = loadedMcpStatuses.map((item) => ({
        key: item.id,
        label: item.name,
        active: selectedMcpServerIds.includes(item.id),
        description:
          item.status === 'loaded'
            ? undefined
            : item.reason
              ? `${t(`conversation.mcp.status.${item.status}` as const)} · ${item.reason}`
              : t(`conversation.mcp.status.${item.status}` as const),
      }));
      entries.push({
        key: 'mcp',
        icon: <Shield theme='outline' size='16' />,
        label: t('conversation.mcp.loaded'),
        variant: 'muted',
        submenu: {
          title: t('conversation.mcp.loaded'),
          options: mcpOptions,
          onSelect: (serverId) => {
            const server = loadedMcpStatuses.find((item) => item.id === serverId);
            if (server?.status === 'loaded') onSelectMcpServer(server);
          },
        },
      });
    }

    return entries;
  }, [
    attachEntries,
    canSwitchModel,
    currentMode,
    handleSessionComputeChange,
    handleSheetModeChange,
    handleThoughtLevelSetOption,
    grantsController,
    grantsCount,
    i18n,
    isMobile,
    isModelListLoading,
    loadedMcpStatuses,
    loadedSkills,
    librarySkillNames,
    mobileComputeProviders,
    mobileEnabledCompute,
    specialistControl,
    memoryControl,
    delegationControl,
    model_info,
    modelListError,
    onSelectMcpServer,
    onSelectSkill,
    onEnableSkill,
    retryModelList,
    runtimeConfig.setStatus.state,
    runtimeMode,
    runtimeThoughtLevel,
    selectModel,
    selectedMcpServerIds,
    selectedSkillNames,
    xscienceSessionOptions,
    sessionOptionsReady,
    t,
    updatePersistentSessionOption,
  ]);
  return sheetEntries;
};
