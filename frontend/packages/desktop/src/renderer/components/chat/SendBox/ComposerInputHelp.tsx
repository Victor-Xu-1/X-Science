import { Popover } from '@arco-design/web-react';
import { CircleHelp } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import './composerInputHelp.css';

type Props = {
  descriptionId: string;
  promptHint: string;
  sessionReferences: boolean;
  fileReferences?: boolean;
  historyNavigation?: boolean;
  scopeKey?: string;
};

const ComposerInputHelp = ({
  descriptionId,
  promptHint,
  sessionReferences,
  fileReferences = true,
  historyNavigation = true,
  scopeKey,
}: Props) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const label = t('conversation.sendbox.inputHelp.title');
  const syntaxHint = t(fileReferences ? 'conversation.sendbox.hint' : 'conversation.sendbox.inputHelp.commandsOnly');
  const tips = [
    promptHint,
    ...(promptHint.includes(syntaxHint) ? [] : [syntaxHint]),
    ...(sessionReferences ? [t('conversation.sendbox.inputHelp.sessions')] : []),
    t('conversation.sendbox.inputHelp.send'),
    t('conversation.sendbox.inputHelp.newLine'),
    ...(historyNavigation ? [t('conversation.sendbox.inputHelp.history')] : []),
  ].filter(Boolean);

  useEffect(() => setOpen(false), [scopeKey]);

  useEffect(() => {
    if (!open) return;
    const dismiss = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
      if (triggerRef.current?.isConnected) triggerRef.current.focus();
    };
    document.addEventListener('keydown', dismiss, true);
    return () => document.removeEventListener('keydown', dismiss, true);
  }, [open]);

  return (
    <>
      <span id={descriptionId} className='sr-only'>
        {tips.join(' ')}
      </span>
      <Popover
        trigger='click'
        position='tr'
        popupVisible={open}
        onVisibleChange={setOpen}
        unmountOnExit
        content={
          <div role='dialog' aria-label={label} aria-modal='false' className='composer-input-help-content'>
            <p className='composer-input-help-title'>{label}</p>
            {tips.map((tip) => (
              <p key={tip}>{tip}</p>
            ))}
          </div>
        }
      >
        <button
          ref={triggerRef}
          type='button'
          aria-label={label}
          aria-haspopup='dialog'
          aria-expanded={open}
          className='composer-input-help-trigger'
        >
          <CircleHelp size={15} aria-hidden='true' />
        </button>
      </Popover>
    </>
  );
};

export default ComposerInputHelp;
