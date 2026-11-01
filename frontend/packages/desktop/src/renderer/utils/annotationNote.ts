/** Canonical note capacity uses JavaScript string.length: UTF16 code units. */
export const ANNOTATION_NOTE_MAX_UTF16_UNITS = 4000;

/** Keep an over-limit draft intact; only persistence is disabled until corrected. */
export function isAnnotationNoteSaveable(text: string): boolean {
  return text.trim().length > 0 && text.length <= ANNOTATION_NOTE_MAX_UTF16_UNITS;
}
