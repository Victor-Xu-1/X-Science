import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { libraryReferenceTypes } from '@/renderer/components/chat/SendBox/reading/libraryBibliographicDraft';
import './libraryBibliographicDetails.css';

const fields = [
  ['item_type', 'type'],
  ['container_title', 'publication'],
  ['url', 'url'],
  ['volume', 'volume'],
  ['issue', 'issue'],
  ['pages', 'pages'],
  ['publisher', 'publisher'],
  ['publisher_place', 'place'],
  ['edition', 'edition'],
] as const;

function savedText(metadata: Readonly<LiteratureItem['metadata']>, key: string): string | undefined {
  if (!Object.hasOwn(metadata, key)) return undefined;
  const value = metadata[key];
  return typeof value === 'string' && value.trim() ? value : undefined;
}

function externalUrl(value: string): string | undefined {
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (code < 32 || code === 127) return undefined;
  }
  try {
    const url = new URL(value);
    return (url.protocol === 'http:' || url.protocol === 'https:') && !url.username && !url.password
      ? url.href
      : undefined;
  } catch {
    return undefined;
  }
}

/** The existing detail reader owns identity and persistence; this leaf only presents saved scalars. */
export function LibraryBibliographicDetails({ metadata }: { metadata: Readonly<LiteratureItem['metadata']> }) {
  const { t } = useTranslation();
  const saved = fields.flatMap(([key, label]) => {
    const value = savedText(metadata, key);
    return value === undefined ? [] : [{ key, label, value }];
  });
  if (!saved.length) return null;
  return (
    <dl className='library-bibliographic-details'>
      {saved.map(({ key, label, value }) => {
        const knownType = key === 'item_type' ? libraryReferenceTypes.find((type) => type === value) : undefined;
        const text = knownType ? t(`conversation.libraryBibliographic.types.${knownType}`) : value;
        const href = key === 'url' ? externalUrl(value) : undefined;
        return (
          <div key={key}>
            <dt>{t(`conversation.libraryBibliographic.${label}`)}</dt>
            <dd title={text}>
              {href ? (
                <a
                  href={href}
                  target='_blank'
                  rel='noopener noreferrer'
                  aria-label={`${t('conversation.libraryBibliographic.url')}: ${text}`}
                >
                  {text}
                </a>
              ) : (
                text
              )}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}
