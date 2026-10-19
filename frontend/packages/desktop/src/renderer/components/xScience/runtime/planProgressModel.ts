export type XSciencePlanProgress = { completed: number; total: number };

/** Counts are optional execution metadata, never a task-completion decision. */
export function normalizeXSciencePlanProgress(
  value: unknown,
  reference: { artifactId: string; versionId: string | null }
): XSciencePlanProgress | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value) || !reference.versionId) return undefined;
  const progress = value as Record<string, unknown>;
  const { completed, total } = progress;
  if (
    progress.artifact_id !== reference.artifactId ||
    progress.version_id !== reference.versionId ||
    typeof completed !== 'number' ||
    typeof total !== 'number' ||
    !Number.isSafeInteger(completed) ||
    !Number.isSafeInteger(total) ||
    completed < 0 ||
    total <= 0 ||
    completed > total
  )
    return undefined;
  return { completed, total };
}
