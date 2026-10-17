import { Popover } from '@arco-design/web-react';
import React, { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import ComposerCapabilityPicker from '@/renderer/components/media/ComposerCapabilityPicker';

type ConversationSkillConflictRecoveryProps = {
  skillNames: string[];
  selectedSkillNames: string[];
  onSelectSkill: (name: string) => void;
  actionClassName: string;
};

// Mount only for the current conflict. Its popup state must not survive a
// different conversation or a later save state, and never retries the POST.
export function ConversationSkillConflictRecovery({
  skillNames,
  selectedSkillNames,
  onSelectSkill,
  actionClassName,
}: ConversationSkillConflictRecoveryProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const close = () => {
    setOpen(false);
    queueMicrotask(() => buttonRef.current?.focus());
  };

  return (
    <>
      <Popover
        trigger='click'
        position='top'
        popupVisible={open}
        onVisibleChange={setOpen}
        content={
          <div
            className='app-overlay-menu composer-control-menu py-6px'
            style={{ minWidth: 208, maxWidth: 'calc(100vw - 24px)' }}
            role='menu'
            aria-label={t('conversation.attachMenu.saveAsSkillChooseExisting')}
            onKeyDown={(event) => {
              if (event.key !== 'Escape') return;
              event.preventDefault();
              event.stopPropagation();
              close();
            }}
          >
            <ComposerCapabilityPicker
              skillNames={skillNames}
              mcpStatuses={[]}
              selectedSkillNames={selectedSkillNames}
              selectedMcpServerIds={[]}
              onSelectSkill={(name) => {
                close();
                onSelectSkill(name);
              }}
              onOpenMcpSettings={close}
              onRequestClose={close}
            />
          </div>
        }
      >
        <button
          ref={buttonRef}
          type='button'
          className={actionClassName}
          disabled={skillNames.length === 0}
          onClick={() => setOpen(true)}
        >
          {t('conversation.attachMenu.saveAsSkillChooseExisting')}
        </button>
      </Popover>
      {skillNames.length === 0 && <span>{t('conversation.skills.noMatch')}</span>}
    </>
  );
}
