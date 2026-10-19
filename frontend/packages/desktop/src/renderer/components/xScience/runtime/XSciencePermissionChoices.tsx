import React from 'react';
import { Tooltip } from '@arco-design/web-react';
import { Check } from '@icon-park/react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import XSciencePermissionIcon, { isWarningPermissionMode } from './XSciencePermissionIcon';
import type { XSciencePermissionControl } from './useXSciencePermissionControl';

export function XSciencePermissionChoices({ control }: { control: XSciencePermissionControl }) {
  return (
    <>
      {control.options.map((item) => {
        const warningStyle = isWarningPermissionMode(item.value) ? { color: 'var(--warning)' } : undefined;
        return (
          <Tooltip key={item.value} content={item.description} position='left'>
            <button
              type='button'
              role='menuitemradio'
              aria-checked={item.value === control.currentValue}
              aria-label={`${item.label}: ${item.description}`}
              title={item.description}
              data-testid={`x-science-permission-option-${item.value}`}
              className='composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-12px text-left font-inherit text-13px text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50'
              disabled={control.unavailable}
              onClick={() => control.select(item.value)}
            >
              <XSciencePermissionIcon mode={item.value} size={16} />
              <span className='min-w-0 flex-1 truncate' style={warningStyle}>
                {item.label}
              </span>
              <span className='inline-flex w-16px shrink-0 justify-end' style={warningStyle}>
                {item.value === control.currentValue ? <Check size={14} /> : null}
              </span>
            </button>
          </Tooltip>
        );
      })}
    </>
  );
}

export function XScienceFullAccessConfirmation({ control }: { control: XSciencePermissionControl }) {
  const { t } = useTranslation();
  return (
    <Modal
      visible={control.confirmationVisible}
      title={t('agentMode.fullAccessConfirmTitle')}
      okText={t('agentMode.fullAccessConfirmEnable')}
      cancelText={t('common.cancel')}
      okButtonProps={{ disabled: control.unavailable, status: 'warning' }}
      onOk={control.confirm}
      onCancel={control.cancelConfirmation}
      maskClosable={false}
      focusLock
      autoFocus
      unmountOnExit
      style={{ width: 'min(440px, calc(100vw - 32px))' }}
    >
      <p className='m-0 text-13px leading-22px text-t-secondary'>{t('agentMode.fullAccessConfirmDescription')}</p>
    </Modal>
  );
}
