import React, { useId, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  libraryAdvancedScalarKeys,
  libraryReferenceTypes,
  type LibraryScalarDraft,
  type LibraryScalarKey,
} from './libraryBibliographicDraft';
import './libraryBibliographicFields.css';

type FieldsProps = {
  metadata: Record<string, unknown>;
  draft: LibraryScalarDraft;
  disabled: boolean;
  change: (key: LibraryScalarKey, value: string) => void;
};

const labelKeys = {
  item_type: 'type',
  container_title: 'publication',
  url: 'url',
  abstract: 'abstract',
  volume: 'volume',
  issue: 'issue',
  pages: 'pages',
  publisher: 'publisher',
  publisher_place: 'place',
  edition: 'edition',
} as const;

function ScalarField({ field, metadata, draft, disabled, change }: FieldsProps & { field: LibraryScalarKey }) {
  const { t } = useTranslation();
  const id = useId();
  const label =
    field === 'abstract'
      ? t('conversation.libraryAbstract.title')
      : t(`conversation.libraryBibliographic.${labelKeys[field]}`);
  const nonText = Object.hasOwn(metadata, field) && typeof metadata[field] !== 'string';
  const savedType = typeof metadata.item_type === 'string' ? metadata.item_type : '';
  const legacyType = savedType && !libraryReferenceTypes.some((type) => type === savedType) ? savedType : '';
  return (
    <div className='library-bibliographic__field'>
      <label htmlFor={id}>{label}</label>
      {field === 'item_type' ? (
        <select
          id={id}
          disabled={disabled}
          value={draft.item_type}
          onChange={(event) => change(field, event.target.value)}
        >
          <option value=''>—</option>
          {legacyType && <option value={legacyType}>{legacyType}</option>}
          {libraryReferenceTypes.map((type) => (
            <option key={type} value={type}>
              {t(`conversation.libraryBibliographic.types.${type}`)}
            </option>
          ))}
        </select>
      ) : field === 'abstract' ? (
        <textarea
          id={id}
          disabled={disabled}
          rows={5}
          value={draft[field]}
          onChange={(event) => change(field, event.target.value)}
        />
      ) : (
        <input
          id={id}
          disabled={disabled}
          value={draft[field]}
          inputMode={field === 'url' ? 'url' : undefined}
          autoCapitalize={field === 'url' ? 'none' : undefined}
          spellCheck={field === 'url' ? false : undefined}
          onChange={(event) => change(field, event.target.value)}
        />
      )}
      {nonText && (
        <div className='library-bibliographic__saved-value'>
          <span>{t('conversation.libraryBibliographic.nonTextValue')}</span>
          <button
            type='button'
            disabled={disabled}
            aria-label={`${t('common.clear')} ${label}`}
            onClick={() => change(field, '')}
          >
            {t('common.clear')}
          </button>
        </div>
      )}
    </div>
  );
}

export function LibraryReferenceTypeField(props: FieldsProps) {
  return <ScalarField {...props} field='item_type' />;
}

/** Compact scalar fields share the editor's draft and original mutation owner. */
export function LibraryBibliographicFields(props: FieldsProps) {
  const { t } = useTranslation();
  const advancedId = useId();
  const [advanced, setAdvanced] = useState(() =>
    libraryAdvancedScalarKeys.some(
      (key) =>
        Object.hasOwn(props.metadata, key) &&
        (typeof props.metadata[key] !== 'string' || Boolean(props.metadata[key].trim()))
    )
  );
  return (
    <div className='library-bibliographic'>
      <ScalarField {...props} field='container_title' />
      <ScalarField {...props} field='url' />
      <ScalarField {...props} field='abstract' />
      <div className='library-bibliographic__advanced'>
        <button
          type='button'
          disabled={props.disabled}
          className='library-bibliographic__toggle'
          aria-expanded={advanced}
          aria-controls={advancedId}
          onClick={() => setAdvanced((value) => !value)}
        >
          <ChevronDown size={14} aria-hidden='true' className={advanced ? 'is-expanded' : ''} />
          {t('conversation.libraryBibliographic.advanced')}
        </button>
        {advanced && (
          <div id={advancedId} className='library-bibliographic__grid'>
            {libraryAdvancedScalarKeys.map((field) => (
              <ScalarField key={field} {...props} field={field} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
