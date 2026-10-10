/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { LanguageDescription, type LanguageSupport } from '@codemirror/language';
import { LARGE_TEXT_VIEWER_THRESHOLD } from '../constants';

type LanguageCatalog = typeof import('@codemirror/language-data').languages;

let languageCatalogPromise: Promise<LanguageCatalog> | null = null;

const loadLanguageCatalog = (): Promise<LanguageCatalog> => {
  languageCatalogPromise ??= import('@codemirror/language-data').then((module) => module.languages);
  return languageCatalogPromise;
};

/**
 * Resolve a CodeMirror language description by an explicit language name first,
 * then by file extension as a fallback. Name matching is fuzzy so common
 * aliases (e.g. "typescript") resolve correctly. Returns null when nothing
 * matches so callers can fall back to plain text.
 */
export const matchLanguageDescription = async (
  languageName?: string,
  fileName?: string
): Promise<LanguageDescription | null> => {
  const languages = await loadLanguageCatalog();
  if (languageName) {
    const byName = LanguageDescription.matchLanguageName(languages, languageName, true);
    if (byName) return byName;
  }
  if (fileName) {
    const byFile = LanguageDescription.matchFilename(languages, fileName);
    if (byFile) return byFile;
  }
  return null;
};

/**
 * Lazily load the {@link LanguageSupport} for the matched language. The dynamic
 * import is wrapped so it never throws: any load failure (or no match) resolves
 * to null, letting the editor degrade gracefully to plain text.
 */
export const loadLanguageSupport = async (
  languageName?: string,
  fileName?: string
): Promise<LanguageSupport | null> => {
  const desc = await matchLanguageDescription(languageName, fileName);
  if (!desc) return null;
  try {
    return await desc.load();
  } catch {
    return null;
  }
};

/**
 * Large-file guard for syntax highlighting. When content exceeds the viewer
 * threshold we disable highlighting to keep the editor responsive. This only
 * turns off highlighting — content is never truncated, because the editor
 * remains fully editable.
 */
export const shouldDisableHighlighting = (length: number): boolean => length > LARGE_TEXT_VIEWER_THRESHOLD;
