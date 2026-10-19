import { Spin, Tooltip } from '@arco-design/web-react';
import { BrainCircuit, GitFork, ScanEye } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';

type ToggleKey = 'autoReview' | 'memory' | 'delegation';

/** The row owns presentation only; session-option persistence remains in the
 * composer callback. Reference order: Auto-review, Memory, Delegation. */
export default function XScienceAgentControlToggles({
  value,
  pending,
  onChange,
  disabled = false,
}: {
  value: XScienceSessionOptions;
  pending: Exclude<ToggleKey, 'delegation'> | null;
  onChange: (key: ToggleKey, checked: boolean) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
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
    {
      key: 'delegation',
      icon: GitFork,
      label: t('conversation.xscienceRuntime.sessionOptions.delegation'),
      description: t('conversation.xscienceRuntime.sessionOptions.delegationDescription'),
    },
  ] as const;
  return (
    <>
      {rows.map(({ key, icon: Icon, label, description }) => (
        <Tooltip key={key} content={description} position='left'>
          <button
            type='button'
            role='menuitemcheckbox'
            aria-checked={value[key]}
            data-testid={`session-config-row-${key}`}
            className='composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-8px text-left font-inherit text-13px text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50'
            disabled={disabled || pending !== null}
            onClick={() => onChange(key, !value[key])}
          >
            <Icon size={16} strokeWidth={2} aria-hidden='true' className='shrink-0 text-t-secondary' />
            <span className='min-w-0 flex-1 truncate'>{label}</span>
            <span
              aria-hidden='true'
              className={`relative inline-flex h-20px w-36px shrink-0 items-center rounded-full transition-colors ${value[key] ? 'bg-[var(--workspace-accent)]' : 'bg-fill-4'} ${pending !== null ? 'opacity-60' : ''}`}
            >
              {pending === key ? (
                <span className='absolute inset-0 flex items-center justify-center'>
                  <Spin size={12} />
                </span>
              ) : (
                <span
                  className='h-16px w-16px rounded-full transition-transform'
                  style={{
                    transform: `translateX(${value[key] ? 18 : 2}px)`,
                    backgroundColor: value[key]
                      ? 'var(--workspace-accent-contrast)'
                      : 'var(--workspace-overlay-surface)',
                  }}
                />
              )}
            </span>
          </button>
        </Tooltip>
      ))}
    </>
  );
}
