import React, { useEffect, useRef, useState } from 'react';
import { Star } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import './libraryRating.css';

type RatingExposure = { identity: string; active: boolean; inFlight: boolean };
type RatingAttempt = { exposure: RatingExposure; value: number; phase: 'saving' | 'confirming' | 'uncertain' };

/** Page owns the immutable field command, retry, receipt and live readback. */
export function LibraryRating({
  item,
  account,
  visible,
  disabled,
  submit,
  reload,
}: {
  item: LiteratureItem;
  account: string;
  visible: boolean;
  disabled: boolean;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  reload?: () => void;
}) {
  const { t } = useTranslation();
  const identity = JSON.stringify([account, item.id, item.metadata_revision]);
  const exposure = useRef<RatingExposure>({ identity, active: true, inFlight: false });
  if (exposure.current.identity !== identity) {
    exposure.current.active = false;
    exposure.current = { identity, active: true, inFlight: false };
  }
  const currentView = useRef({ visible, disabled, deleted: item.deleted });
  currentView.current = { visible, disabled, deleted: item.deleted };
  const [attempt, setAttempt] = useState<RatingAttempt>();
  useEffect(() => {
    const current = exposure.current;
    current.active = true;
    return () => {
      current.active = false;
    };
  }, [identity]);
  if (account !== getRendererAccountScopeToken()) return null;
  const pending = attempt?.exposure === exposure.current ? attempt : undefined;
  const waiting = pending !== undefined && pending.phase !== 'uncertain';
  const projected = item.rating_view;
  const rating = waiting ? pending.value : projected?.known ? projected.value : undefined;
  const blocked = disabled || !visible || item.deleted || waiting;
  const isCurrent = (owner: RatingExposure) =>
    exposure.current === owner &&
    owner.active &&
    currentView.current.visible &&
    account === getRendererAccountScopeToken();

  const commit = async (option: number) => {
    const owner = exposure.current;
    if (blocked || owner.inFlight || !isCurrent(owner) || currentView.current.disabled || currentView.current.deleted)
      return;
    const value = rating === option ? 0 : option;
    owner.inFlight = true;
    setAttempt({ exposure: owner, value, phase: 'saving' });
    try {
      await submit({
        kind: 'edit-metadata',
        operation_id: crypto.randomUUID(),
        item_id: item.id,
        expected_revision: item.metadata_revision,
        metadata_patch: { set: { rating: value } },
      });
      if (isCurrent(owner)) setAttempt({ exposure: owner, value, phase: 'confirming' });
    } catch {
      // A lost ACK can have committed. Only the original controller may retry
      // that operation; refresh through the existing reader to learn live state.
      if (isCurrent(owner)) {
        setAttempt({ exposure: owner, value, phase: 'uncertain' });
        reload?.();
      }
    } finally {
      owner.inFlight = false;
    }
  };

  return (
    <section className='library-rating' aria-label={t('conversation.libraryRating.section')}>
      <div className='library-rating__row'>
        <span className='library-rating__label'>{t('conversation.libraryRating.label')}</span>
        <div
          className='library-rating__stars'
          role='group'
          aria-label={t('conversation.libraryRating.label')}
          aria-busy={waiting}
        >
          {[1, 2, 3, 4, 5].map((option) => {
            const active = rating !== undefined && option <= rating;
            return (
              <button
                key={option}
                type='button'
                disabled={blocked}
                aria-label={t(
                  option === 1 ? 'conversation.libraryRating.oneStar' : 'conversation.libraryRating.stars',
                  { count: option }
                )}
                aria-pressed={active}
                onClick={() => void commit(option)}
              >
                <Star size={14} aria-hidden='true' fill={active ? 'currentColor' : 'none'} />
              </button>
            );
          })}
        </div>
        <span className='library-rating__value'>
          {rating === undefined
            ? t(projected ? 'conversation.libraryRating.unknown' : 'conversation.libraryRating.unavailable')
            : rating === 0
              ? t('conversation.libraryRating.unrated')
              : t('conversation.libraryRating.value', { value: rating })}
        </span>
      </div>
      {pending && (
        <p className='library-rating__feedback' role={pending.phase === 'uncertain' ? 'alert' : 'status'}>
          {t(`conversation.libraryRating.${pending.phase}`)}
        </p>
      )}
      {reload && (pending !== undefined || projected === undefined) && (
        <button
          className='library-rating__refresh'
          type='button'
          disabled={!visible || item.deleted || pending?.phase === 'saving'}
          onClick={() => {
            if (isCurrent(exposure.current) && !currentView.current.deleted && !exposure.current.inFlight) reload();
          }}
        >
          {t('conversation.libraryRating.refresh')}
        </button>
      )}
    </section>
  );
}
