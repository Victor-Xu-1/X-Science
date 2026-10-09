import { Button, Select } from '@arco-design/web-react';
import { Close } from '@icon-park/react';
import React, { useId, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import SettingsLibrarySearch from '../components/SettingsLibrarySearch';

export type ExpertCapabilityOption = { id: string; label: string };
type Props = {
  kind: 'skills' | 'connectors';
  selected: ExpertCapabilityOption[];
  available: ExpertCapabilityOption[];
  disabled: boolean;
  onAdd: (id: string) => void;
  onRemove: (id: string) => void;
};

/** Local review state only. Raw capability identities stay owned by the editor. */
export default function ExpertCapabilityPicker({ kind, selected, available, disabled, onAdd, onRemove }: Props) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const listId = useId();
  const countId = useId();
  const isSkills = kind === 'skills';
  const searchLabel = t(
    isSkills ? 'settings.expertsSettings.searchSelectedSkills' : 'settings.expertsSettings.searchSelectedConnectors'
  );
  const selectedLabel = t(
    isSkills ? 'settings.expertsSettings.selectedSkills' : 'settings.expertsSettings.selectedConnectors'
  );
  const addLabel = t(isSkills ? 'settings.expertsSettings.addSkill' : 'settings.expertsSettings.addConnector');
  const matches = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return selected.filter(
      ({ id, label }) => !normalized || [id, label].some((value) => value.toLowerCase().includes(normalized))
    );
  }, [query, selected]);

  return (
    <div className='expert-capability-picker'>
      <div className='expert-capability-picker__toolbar'>
        <SettingsLibrarySearch label={searchLabel} value={query} onChange={setQuery} aria-controls={listId} />
        <Select
          aria-label={addLabel}
          placeholder={addLabel}
          value={undefined}
          showSearch
          allowClear
          disabled={disabled || available.length === 0}
          onChange={(id: string) => {
            if (!disabled && id && !selected.some((entry) => entry.id === id)) onAdd(id);
          }}
        >
          {available.map(({ id, label }) => (
            <Select.Option key={id} value={id}>
              {label}
            </Select.Option>
          ))}
        </Select>
      </div>
      <p id={countId} className='expert-capability-picker__count' aria-live='polite'>
        {t('settings.expertsSettings.selectedMatches', { count: matches.length, total: selected.length })}
      </p>
      <div
        id={listId}
        role='region'
        aria-label={selectedLabel}
        aria-describedby={countId}
        tabIndex={0}
        className='expert-capability-picker__viewport'
      >
        {selected.length === 0 ? (
          <p className='expert-capability-picker__empty'>
            {t(isSkills ? 'settings.expertsSettings.noSkills' : 'settings.expertsSettings.noConnectors')}
          </p>
        ) : matches.length === 0 ? (
          <p className='expert-capability-picker__empty'>{t('settings.expertsSettings.noSelectedMatches')}</p>
        ) : (
          <ul className='expert-capability-picker__list'>
            {matches.map(({ id, label }) => {
              const identityId = `${listId}-${encodeURIComponent(id)}`;
              return (
                <li key={id} className='expert-capability-picker__row'>
                  <span className='expert-capability-picker__identity'>
                    <span className='expert-capability-picker__label'>{label}</span>
                    {label !== id ? (
                      <code id={identityId} className='expert-capability-picker__id'>
                        {id}
                      </code>
                    ) : null}
                  </span>
                  <Button
                    type='text'
                    aria-label={t('settings.expertsSettings.removeNamed', { name: label })}
                    aria-describedby={label !== id ? identityId : undefined}
                    disabled={disabled}
                    icon={<Close size={14} aria-hidden='true' />}
                    onClick={() => {
                      if (!disabled) onRemove(id);
                    }}
                  />
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
