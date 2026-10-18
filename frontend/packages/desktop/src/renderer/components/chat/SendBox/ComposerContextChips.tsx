import {
  composerContextItemKey,
  type ComposerContextItem,
  type ComposerArtifactContext,
} from './composerCompositionModel';
import { CloseSmall, FileText, Lightning, PlugOne } from '@icon-park/react';
import React from 'react';
import { useTranslation } from 'react-i18next';

type ComposerContextChipsProps = {
  items: readonly ComposerContextItem[];
  onRemove: (key: string) => void;
  onOpenArtifact?: (item: ComposerArtifactContext) => void;
};

const contextIcon = (kind: ComposerContextItem['kind']) => {
  switch (kind) {
    case 'artifact':
      return <FileText theme='outline' size={14} />;
    case 'skill':
      return <Lightning theme='outline' size={14} />;
    case 'mcp':
      return <PlugOne theme='outline' size={14} />;
  }
};

const ComposerContextChips: React.FC<ComposerContextChipsProps> = ({ items, onRemove, onOpenArtifact }) => {
  const { t } = useTranslation();
  if (items.length === 0) return null;
  return (
    <div className='composer-context-chips' data-testid='composer-context-chips'>
      {items.map((item) => {
        const key = composerContextItemKey(item);
        return (
          <span className={`composer-context-chip composer-context-chip--${item.kind}`} key={key}>
            <span className='composer-context-chip__icon' aria-hidden='true'>
              {contextIcon(item.kind)}
            </span>
            {item.kind === 'artifact' &&
            onOpenArtifact &&
            (item.label.toLowerCase().endsWith('.pdf') || item.contentType === 'application/pdf') ? (
              <button
                type='button'
                className='composer-context-chip__label border-0 bg-transparent text-left text-inherit cursor-pointer'
                title={item.label}
                aria-label={t('conversation.reading.openNamed', { name: item.label })}
                onClick={() => onOpenArtifact(item)}
              >
                {item.label}
              </button>
            ) : (
              <span className='composer-context-chip__label' title={item.label}>
                {item.label}
              </span>
            )}
            <button
              type='button'
              className='composer-context-chip__remove'
              aria-label={`Remove ${item.label}`}
              onClick={() => onRemove(key)}
            >
              <CloseSmall theme='outline' size={13} />
            </button>
          </span>
        );
      })}
    </div>
  );
};

export default ComposerContextChips;
