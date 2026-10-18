import type { PdfReadingController, PdfReadingView } from '@/renderer/services/runtime/PdfReadingController';
import { ChevronDown, Link2, Loader2, X } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { ReadingContextPicker } from './ReadingContextPicker';
import './readingContext.css';

export function PdfReadingBar({
  view,
  controller,
  onOpen,
}: {
  view: PdfReadingView;
  controller: PdfReadingController;
  onOpen: (bindingId: string) => void;
}) {
  const { t } = useTranslation();
  const context = view.context;
  const blocked = Boolean(view.pending) || view.blocked;
  const retry = () => {
    void (view.error === 'uncertain' ? controller.retry() : controller.refresh()).catch((): undefined => undefined);
  };
  return (
    <div className='pdf-reading-composer'>
      {context && context.bindings.length > 0 && (
        <div className='pdf-reading-bar' data-testid='pdf-context-bar'>
          <ReadingContextPicker
            context={context}
            pending={blocked}
            onSelect={async (source) => {
              await controller.link([source]);
            }}
          >
            <button
              type='button'
              className='pdf-reading-bar__label'
              disabled={blocked}
              aria-label={t('conversation.reading.choose')}
            >
              <Link2 size={16} strokeWidth={2} aria-hidden='true' />
              {t('conversation.reading.label')}
              <ChevronDown size={12} strokeWidth={2} aria-hidden='true' />
            </button>
          </ReadingContextPicker>
          <div className='pdf-reading-bar__bindings'>
            {context.bindings.map((binding) => (
              <span className='pdf-reading-chip' key={binding.binding_id}>
                <button
                  type='button'
                  className='pdf-reading-chip__open'
                  onClick={() => onOpen(binding.binding_id)}
                  aria-label={t('conversation.reading.openNamed', { name: binding.name })}
                  title={t('conversation.reading.linkedHint')}
                >
                  <span className='pdf-reading-filename'>
                    <span>{binding.name.replace(/\.pdf$/i, '')}</span>
                    <span>{binding.name.match(/\.pdf$/i)?.[0] ?? ''}</span>
                  </span>
                </button>
                <button
                  type='button'
                  className='pdf-reading-chip__remove'
                  disabled={blocked}
                  aria-label={t('conversation.reading.removeNamed', { name: binding.name })}
                  onClick={() => void controller.unlink(binding.binding_id).catch((): undefined => undefined)}
                >
                  {view.mutating && view.pending?.request.binding_id === binding.binding_id ? (
                    <Loader2 size={14} className='pdf-reading-spin' />
                  ) : (
                    <X size={14} strokeWidth={2.2} />
                  )}
                </button>
              </span>
            ))}
          </div>
        </div>
      )}
      {view.error && (
        <div className='pdf-reading-notice' role='alert'>
          <span>
            {t(
              `conversation.reading.${
                view.error === 'uncertain'
                  ? 'uncertain'
                  : view.error === 'conflict'
                    ? 'conflict'
                    : view.error === 'load'
                      ? 'loadFailed'
                      : 'mutationFailed'
              }`
            )}
          </span>
          <button type='button' disabled={view.loading || view.mutating} onClick={retry}>
            {t(
              view.error === 'conflict' || view.error === 'mutation' ? 'conversation.reading.refresh' : 'common.retry'
            )}
          </button>
        </div>
      )}
    </div>
  );
}
