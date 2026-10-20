import { Spin, Tooltip } from '@arco-design/web-react';
import { BrainCircuit, ScanEye } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import {
  sessionMemoryPresentation,
  type XScienceMemoryControl,
} from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import XScienceDelegationControlRow from './XScienceDelegationControlRow';

type ToggleKey = 'autoReview' | 'memory';

/** The row owns presentation only; session-option persistence remains in the
 * composer callback. Reference order: Auto-review, Memory, Delegation. */
export default function XScienceAgentControlToggles({
  value,
  pending,
  onChange,
  disabled = false,
  memoryDisabled = disabled,
  memoryControl,
  delegationControl,
  active,
}: {
  value: XScienceSessionOptions;
  pending: Exclude<ToggleKey, 'delegation'> | null;
  onChange: (key: ToggleKey, checked: boolean) => void;
  disabled?: boolean;
  memoryDisabled?: boolean;
  memoryControl?: XScienceMemoryControl;
  delegationControl?: XScienceDelegationControl;
  active?: boolean;
}) {
  const { t } = useTranslation();
  const memory = sessionMemoryPresentation(memoryControl, value.memory);
  const rows = [
    {
      key: 'autoReview',
      icon: ScanEye,
      label: t('conversation.xscienceRuntime.sessionOptions.autoReview'),
      description: t('conversation.xscienceRuntime.sessionOptions.autoReviewDescription'),
    },
    {
      key: 'memory',
      icon: BrainCircuit,
      label: t('conversation.xscienceRuntime.sessionOptions.memory'),
      description: t('conversation.xscienceRuntime.sessionOptions.memoryDescription'),
    },
  ] as const;
  return (
    <>
      {rows.map(({ key, icon: Icon, label, description }) => {
        const checked = key === 'memory' ? memory.checked : value[key];
        const reason = key === 'memory' && memory.reason ? t(memory.reason) : description;
        const locked =
          (key === 'memory' ? memoryDisabled : disabled) || pending !== null || (key === 'memory' && memory.disabled);
        return (
          <Tooltip key={key} content={reason} position='left'>
            <button
              type='button'
              role='menuitemcheckbox'
              aria-checked={checked ?? 'mixed'}
              title={key === 'memory' ? reason : undefined}
              data-testid={`session-config-row-${key}`}
              className='composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-8px text-left font-inherit text-13px text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50'
              disabled={locked}
              onClick={() => onChange(key, !(key === 'memory' ? memory.checked : value[key]))}
            >
              <Icon size={16} strokeWidth={2} aria-hidden='true' className='shrink-0 text-t-secondary' />
              <span className='min-w-0 flex-1 truncate'>{label}</span>
              <span
                aria-hidden='true'
                className={`relative inline-flex h-20px w-36px shrink-0 items-center rounded-full transition-colors ${checked ? 'bg-[var(--workspace-accent)]' : 'bg-fill-4'} ${pending !== null ? 'opacity-60' : ''}`}
              >
                {pending === key ? (
                  <span className='absolute inset-0 flex items-center justify-center'>
                    <Spin size={12} />
                  </span>
                ) : (
                  <span
                    className='h-16px w-16px rounded-full transition-transform'
                    style={{
                      transform: `translateX(${checked === undefined ? 10 : checked ? 18 : 2}px)`,
                      backgroundColor: checked
                        ? 'var(--workspace-accent-contrast)'
                        : 'var(--workspace-overlay-surface)',
                    }}
                  />
                )}
              </span>
            </button>
          </Tooltip>
        );
      })}
      {memory.retry && (
        <button
          type='button'
          role='menuitem'
          className='composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center border-0 bg-transparent px-8px text-left font-inherit text-13px text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50'
          onClick={memory.retry}
          disabled={memoryDisabled || memory.pending}
        >
          {t('conversation.xscienceRuntime.sessionOptions.memoryPreferenceRetry')}
        </button>
      )}
      <XScienceDelegationControlRow control={delegationControl} active={active} />
    </>
  );
}
