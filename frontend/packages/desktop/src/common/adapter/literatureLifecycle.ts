export type LiteratureLifecycle = 'active' | 'deleted';

export function decodeLiteratureLifecycle(value: unknown): LiteratureLifecycle {
  if (value === undefined || value === 'active') return 'active';
  if (value === 'deleted') return value;
  throw new Error('Invalid Library lifecycle');
}
