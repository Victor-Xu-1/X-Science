import { Modal as ArcoModal, type ModalProps } from '@arco-design/web-react';
import { Close } from '@icon-park/react';
import React, { useId, useLayoutEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';

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
  return (
    <ArcoModal
      {...props}
      data-workbench-modal-scope={scopeId}
      closeIcon={props.closeIcon === undefined ? <LocalizedCloseIcon /> : props.closeIcon}
      afterClose={() => {
        const root = document.querySelector(selector);
        const current = document.activeElement;
        const target = openerRef.current;
        openerRef.current = null;
        if (
          target?.isConnected &&
          !target.matches(':disabled') &&
          (current === document.body || root?.contains(current))
        )
          target.focus({ preventScroll: true });
        props.afterClose?.();
      }}
    />
  );
}

// Retain the public imperative API without introducing another implementation.
// Its lifecycle is deliberately separate from the component adapter above.
export default Object.assign(WorkbenchModal, {
  confirm: ArcoModal.confirm,
  info: ArcoModal.info,
  success: ArcoModal.success,
  warning: ArcoModal.warning,
  error: ArcoModal.error,
  config: ArcoModal.config,
  destroyAll: ArcoModal.destroyAll,
  useModal: ArcoModal.useModal,
});
