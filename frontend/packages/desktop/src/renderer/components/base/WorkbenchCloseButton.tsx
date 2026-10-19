import { Close } from '@icon-park/react';
import React from 'react';
import { useTranslation } from 'react-i18next';

/** Native activation bubbles once to the enclosing Arco cancellation owner. */
export default function WorkbenchCloseButton({ style }: { style?: React.CSSProperties }) {
  const { t } = useTranslation();
  return (
    <button type='button' aria-label={t('common.close')} className='workbench-modal-close' style={style}>
      <Close size={16} aria-hidden='true' />
    </button>
  );
}
