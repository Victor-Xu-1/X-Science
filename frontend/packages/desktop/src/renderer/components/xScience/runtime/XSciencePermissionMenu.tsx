import { Dropdown } from '@arco-design/web-react';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import XSciencePermissionIcon from './XSciencePermissionIcon';
import SessionPermissionGrants from './SessionPermissionGrants';
import { XScienceFullAccessConfirmation, XSciencePermissionChoices } from './XSciencePermissionChoices';
import { useXSciencePermissionControl, type XSciencePermissionControlInput } from './useXSciencePermissionControl';

/** Standalone shell retained for the new-conversation consumer. Existing
 * conversation controls use the same content/owner inside their slider. */
const XSciencePermissionMenu: React.FC<XSciencePermissionControlInput> = (props) => {
  const { t } = useTranslation();
  const [popupVisible, setPopupVisible] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const control = useXSciencePermissionControl(props, () => {
    setPopupVisible(false);
    trigger.current?.focus();
  });
  useEffect(() => {
    setPopupVisible(false);
  }, [control.contextKey]);
  if (!control.available) return null;
  const triggerUnavailable = control.unavailable && !control.controller?.isCurrent();
  return (
    <>
      <Dropdown
        key={control.controller?.presentationKey ?? 'permission-mode'}
        trigger='click'
        position='tl'
        popupVisible={popupVisible}
        onVisibleChange={(visible) => !triggerUnavailable && setPopupVisible(visible)}
        droplist={
          <div
            className='app-overlay-menu composer-control-menu box-border overflow-y-auto rd-8px py-6px'
            data-testid='x-science-permission-menu'
            role='menu'
            aria-label={t('agentMode.switchMode')}
            style={{
              width: 'min(288px, calc(100vw - 24px))',
              maxWidth: 'calc(100vw - 24px)',
              maxHeight: 'calc(100vh - 24px)',
              opacity: 1,
            }}
          >
            <XSciencePermissionChoices control={control} />
            {control.controller ? (
              <SessionPermissionGrants controller={control.controller} open={popupVisible} />
            ) : null}
          </div>
        }
      >
        <button
          ref={trigger}
          type='button'
          data-testid='x-science-permission-selector'
          data-current-permission={control.selectedOption?.value}
          aria-label={
            control.selectedOption
              ? `${t('agentMode.permission')} · ${control.selectedOption.label}`
              : t('agentMode.permission')
          }
          title={
            control.selectedOption
              ? `${control.selectedOption.label}: ${control.selectedOption.description}`
              : undefined
          }
          disabled={triggerUnavailable}
          onClick={() => setPopupVisible((visible) => !visible)}
          className='composer-icon-control relative'
        >
          <XSciencePermissionIcon mode={control.selectedOption?.value ?? 'default'} size={17} />
          {control.controller?.isCurrent() &&
          control.grants.available &&
          Boolean(control.grants.snapshot?.grants.length) ? (
            <span
              className='absolute -right-4px -top-4px rounded-full bg-fill-3 px-3px text-10px leading-14px'
              aria-label={t('agentMode.sessionGrants.count', { count: control.grants.snapshot!.grants.length })}
            >
              {control.grants.snapshot!.grants.length}
            </span>
          ) : null}
        </button>
      </Dropdown>
      <XScienceFullAccessConfirmation control={control} />
    </>
  );
};

export default XSciencePermissionMenu;
