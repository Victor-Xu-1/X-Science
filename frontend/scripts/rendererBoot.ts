export interface BootCopy {
  loading: string;
  slow: string;
  slowDescription: string;
  failed: string;
  failedDescription: string;
  reload: string;
}

function escapeMarkup(value: string): string {
  const entities: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  return value.replace(/[&<>"']/g, (character) => entities[character]);
}

/** Derive the pre-JavaScript shell from the same identity and locale authorities. */
export function renderBootDocument(
  html: string,
  productName: string,
  defaultLanguage: string,
  locales: Record<string, BootCopy>
): string {
  const copy = locales[defaultLanguage];
  const fields: (keyof BootCopy)[] = ['loading', 'slow', 'slowDescription', 'failed', 'failedDescription', 'reload'];
  if (
    !copy ||
    Object.values(locales).some((locale) =>
      fields.some((field) => typeof locale[field] !== 'string' || !locale[field].trim())
    )
  ) {
    throw new Error('Incomplete app startup translations');
  }
  const configuration = JSON.stringify({ productName, defaultLanguage, locales }).replace(/</g, '\\u003c');
  const replacements: Record<string, string> = {
    __APP_BOOT_LOCALE__: escapeMarkup(defaultLanguage),
    __APP_BOOT_LOADING__: escapeMarkup(copy.loading.replaceAll('{{productName}}', productName)),
    __APP_BOOT_RELOAD__: escapeMarkup(copy.reload),
    __APP_BOOT_CONFIGURATION__: configuration,
  };
  for (const marker of Object.keys(replacements)) {
    if (!html.includes(marker)) throw new Error(`Missing app startup projection: ${marker}`);
  }
  return html.replace(/__APP_BOOT_(?:LOCALE|LOADING|RELOAD|CONFIGURATION)__/g, (marker) => replacements[marker]);
}
