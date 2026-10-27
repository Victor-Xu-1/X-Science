import React from 'react';
import { Circle, CircleCheck, Contrast } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  decodeLiteratureReadingStatus,
  literatureReadingStatuses,
  type LiteratureReadingStatus,
} from '@/common/adapter/literatureReadingStatus';
import './libraryReadingStatus.css';

const statusIcon = { unread: Circle, reading: Contrast, read: CircleCheck };
const statusKey = (value: LiteratureReadingStatus) => `conversation.libraryReadingStatus.${value}`;

export function LibraryReadingStatusLabel({ value }: { value: LiteratureReadingStatus }) {
  const { t } = useTranslation();
  const Icon = statusIcon[value];
  return (
    <span className='library-reading-status-label' data-reading-status={value}>
      <Icon size={14} aria-hidden='true' />
      <span>{t(statusKey(value))}</span>
    </span>
  );
}

export type LibraryReadingStatusProps = {
  value: LiteratureReadingStatus;
  disabled: boolean;
  onChange: (value: LiteratureReadingStatus) => void;
  accessibleLabel: string;
};

/** Controlled presentation only. The caller owns acknowledgement, errors and persistence. */
export function LibraryReadingStatus({ value, disabled, onChange, accessibleLabel }: LibraryReadingStatusProps) {
  const { t } = useTranslation();
  const Icon = statusIcon[value];
  return (
    <span className='library-reading-status-control' data-reading-status={value} data-disabled={disabled}>
      <Icon size={14} aria-hidden='true' />
      <select
        className='library-reading-status-select'
        aria-label={accessibleLabel}
        value={value}
        disabled={disabled}
        onChange={(event) => {
          if (disabled || event.currentTarget.value === value) return;
          onChange(decodeLiteratureReadingStatus(event.currentTarget.value));
        }}
      >
        {literatureReadingStatuses.map((status) => (
          <option key={status} value={status}>
            {t(statusKey(status))}
          </option>
        ))}
      </select>
    </span>
  );
}
