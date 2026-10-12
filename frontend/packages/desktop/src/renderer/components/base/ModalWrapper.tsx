import type { ModalProps } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { Close } from '@icon-park/react';
import React from 'react';

interface ModalWrapperProps extends Omit<ModalProps, 'title'> {
  children?: React.ReactNode;
  title?: React.ReactNode;
  showCustomClose?: boolean;
}

const ModalWrapper: React.FC<ModalWrapperProps> = ({
  children,
  title,
  showCustomClose = true,
  onCancel,
  className = '',
  ...props
}) => {
  return (
    <Modal {...props} title={null} closable={false} onCancel={onCancel} className={`x-science-modal ${className}`}>
      <div>
        {showCustomClose && title && (
          <div className='x-science-modal-header'>
            <h3 className='x-science-modal-title'>{title}</h3>
            <button onClick={onCancel} className='x-science-modal-close-btn'>
              <Close size={20} fill='#86909c' />
            </button>
          </div>
        )}
        {children}
      </div>
    </Modal>
  );
};

export default ModalWrapper;
