import { Modal as ArcoModal, type ModalProps } from '@arco-design/web-react';
import { Close } from '@icon-park/react';
import React, { useEffect, useId, useLayoutEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { uuid } from '@/common/utils/utils';
import { restoreScopedFocus } from '@/renderer/utils/focusScope';

function LocalizedCloseIcon() {
  const { t } = useTranslation();
  // Arco owns the enclosing cancellation handler. Native activation bubbles
  // to that handler once, including Enter/Space; no nested button-role parent.
  return (
    <button type='button' aria-label={t('common.close')} className='workbench-modal-close'>
      <Close size={16} aria-hidden='true' />
    </button>
  );
}

/** Presentation adapter only; Arco remains the dialog/focus-lock authority. */
function WorkbenchModal(props: React.PropsWithChildren<ModalProps>) {
  const scopeId = useId();
  const selector = `[data-workbench-modal-scope="${scopeId}"]`;
  const opener = useMemo(() => {
    if (!props.visible || typeof document === 'undefined') return null;
    const current = document.activeElement;
    return current instanceof HTMLElement &&
      current !== document.body &&
      !current.closest(`[data-workbench-modal-scope="${scopeId}"]`)
      ? current
      : null;
  }, [props.visible, scopeId]);
  const openerRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (props.visible && opener) openerRef.current = opener;
  }, [props.visible, opener]);
  useEffect(
    () => () => {
      const root = document.querySelector<HTMLElement>(selector);
      const target = openerRef.current;
      // Native portals/focus locks finish disposal before this microtask. Strict
      // effect replay leaves this scope mounted and must not restore the opener.
      queueMicrotask(() => {
        if (document.querySelector(selector)) return;
        restoreScopedFocus(target, root);
      });
    },
    [selector]
  );
  return (
    <ArcoModal
      {...props}
      data-workbench-modal-scope={scopeId}
      closeIcon={props.closeIcon === undefined ? <LocalizedCloseIcon /> : props.closeIcon}
      afterClose={() => {
        const root = document.querySelector(selector);
        const target = openerRef.current;
        openerRef.current = null;
        restoreScopedFocus(target, root as HTMLElement | null);
        props.afterClose?.();
      }}
    />
  );
}

/** Native imperative render/promise/update ownership remains with Arco. */
function presentImperativeModal(method: typeof ArcoModal.confirm): typeof ArcoModal.confirm {
  return (config) => {
    const scopeId = uuid(36);
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const presented = {
      ...config,
      'data-workbench-modal-scope': scopeId,
      closeIcon: config.closeIcon === undefined ? <LocalizedCloseIcon /> : config.closeIcon,
      afterClose: () => {
        const root = document.querySelector<HTMLElement>(`[data-workbench-modal-scope="${scopeId}"]`);
        restoreScopedFocus(opener, root);
        config.afterClose?.();
      },
    };
    return method(presented);
  };
}

// Presentation delegates return the original instances and never implement a
// second confirmation/promise controller. Hook/config/destroy APIs stay native.
export default Object.assign(WorkbenchModal, {
  confirm: presentImperativeModal(ArcoModal.confirm),
  info: presentImperativeModal(ArcoModal.info),
  success: presentImperativeModal(ArcoModal.success),
  warning: presentImperativeModal(ArcoModal.warning),
  error: presentImperativeModal(ArcoModal.error),
  config: ArcoModal.config,
  destroyAll: ArcoModal.destroyAll,
  useModal: ArcoModal.useModal,
});
