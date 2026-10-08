import { Input } from '@arco-design/web-react';
import { Close, Search } from '@icon-park/react';
import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';

type Props = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  'data-testid'?: string;
};

/** Presentation only: each catalog retains its existing query/filter authority. */
export default function SettingsLibrarySearch({ label, value, onChange, 'data-testid': testId }: Props) {
  const { t } = useTranslation();
  const root = useRef<HTMLDivElement>(null);
  const clear = () => {
    onChange('');
    root.current?.querySelector('input')?.focus();
  };
  return (
    <div ref={root} className='settings-library-search'>
      <Input
        type='search'
        aria-label={label}
        placeholder={label}
        value={value}
        onChange={onChange}
        prefix={<Search size={16} aria-hidden='true' />}
        onKeyDown={(event) => {
          if (event.key !== 'Escape' || !value) return;
          event.preventDefault();
          event.stopPropagation();
          clear();
        }}
        suffix={
          value ? (
            <button
              type='button'
              className='settings-library-search__clear'
              aria-label={`${t('common.clear')} · ${label}`}
              onClick={clear}
            >
              <Close size={14} aria-hidden='true' />
            </button>
          ) : null
        }
        data-testid={testId}
      />
    </div>
  );
}
