/** Derived by the canonical reader before JavaScript can round opaque numbers. */
export type LiteratureRatingView = { known: true; value: number } | { known: false };

export function decodeLiteratureRatingView(value: unknown): LiteratureRatingView {
  if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
    const fields = value as Record<string, unknown>;
    if (Reflect.ownKeys(fields).every((key) => key === 'known' || key === 'value')) {
      if (fields.known === false && !Object.hasOwn(fields, 'value')) return { known: false };
      if (
        fields.known === true &&
        typeof fields.value === 'number' &&
        Number.isInteger(fields.value) &&
        fields.value >= 0 &&
        fields.value <= 5
      )
        return { known: true, value: fields.value };
    }
  }
  throw new Error('Invalid literature rating projection');
}
