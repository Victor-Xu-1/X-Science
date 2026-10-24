export type XScienceSkillLibraryOptions = {
  baseUrl?: string;
  fetchImpl?: typeof fetch;
};

export type XScienceSkillReadOptions = XScienceSkillLibraryOptions & { source?: 'draft' | 'published' };
export type XScienceSkillFileSnapshot = {
  content: string;
  origin: string;
  draftRevision: string | null;
  files: string[] | null;
};
export type XScienceSkillPublishOptions = XScienceSkillLibraryOptions & { expectedDraftRevision: string };

export type XScienceSkillDraft = {
  name: string;
  displayName: string;
  description: string;
  files: string[];
  updatedAt: string | null;
};

export type XScienceSkillSource = {
  slug: string;
  repo: string;
  sha: string;
  license: string | null;
  skills: string[];
  importedAt: string | null;
  removable: boolean;
};

export type XScienceSkillRepoEntry = {
  name: string;
  displayName: string;
  description: string;
  path: string | null;
  selected: boolean;
};

export type XScienceSkillRepoPreview = {
  repo: string;
  slug: string;
  sha: string;
  license: string | null;
  skills: XScienceSkillRepoEntry[];
};

export type XScienceSkillImportResult = {
  imported: string[];
  skipped: Array<{ name: string; reason: string }>;
  slug: string | null;
  sha: string | null;
};

export class XScienceSkillLibraryError extends Error {
  readonly status: number;
  readonly code: string | null;

  constructor(status: number, message: string, code: string | null = null) {
    super(message);
    this.name = 'XScienceSkillLibraryError';
    this.status = status;
    this.code = code;
  }
}

type RecordValue = Record<string, unknown>;

export async function loadXScienceSkillDrafts(
  options: XScienceSkillLibraryOptions = {}
): Promise<XScienceSkillDraft[]> {
  const payload = asRecord(await requestJson('/api/skills/drafts', options));
  const drafts = Array.isArray(payload?.drafts) ? payload.drafts : [];
  return drafts.map(toDraft).filter((draft): draft is XScienceSkillDraft => draft !== null);
}

export async function loadXScienceSkillSources(
  options: XScienceSkillLibraryOptions = {}
): Promise<XScienceSkillSource[]> {
  const payload = asRecord(await requestJson('/api/marketplace/sources', options));
  const sources = Array.isArray(payload?.sources) ? payload.sources : [];
  return sources.map(toSource).filter((source): source is XScienceSkillSource => source !== null);
}

export async function previewXScienceSkillRepository(
  repo: string,
  options: XScienceSkillLibraryOptions = {}
): Promise<XScienceSkillRepoPreview> {
  const payload = asRecord(
    await requestJson('/api/marketplace/preview', options, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({ repo }),
    })
  );
  if (!payload) throw new XScienceSkillLibraryError(502, 'Skill repository preview response is invalid');
  const skills = Array.isArray(payload.skills) ? payload.skills : [];
  return {
    repo: stringValue(payload.repo) || repo,
    slug: stringValue(payload.slug),
    sha: stringValue(payload.sha),
    license: nullableStringValue(payload.license),
    skills: skills.map(toRepoEntry).filter((skill): skill is XScienceSkillRepoEntry => skill !== null),
  };
}

export async function importXScienceRepositorySkills(
  preview: XScienceSkillRepoPreview,
  skillNames: string[],
  options: XScienceSkillLibraryOptions = {}
): Promise<XScienceSkillImportResult> {
  return toImportResult(
    await requestJson('/api/marketplace/import', options, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({
        repo: preview.repo,
        sha: preview.sha,
        skills: skillNames,
        ...(preview.license ? { license: preview.license } : {}),
      }),
    })
  );
}

export async function importXScienceSkillFile(
  file: File,
  name: string | undefined,
  options: XScienceSkillLibraryOptions = {}
): Promise<RecordValue> {
  const body = new FormData();
  body.append('file', file, file.name);
  if (name?.trim()) body.append('name', name.trim());
  const payload = await requestJson('/api/skills/import', options, {
    method: 'POST',
    headers: { Accept: 'application/json' },
    body,
  });
  return asRecord(payload) ?? {};
}

export async function loadXScienceSkillFiles(name: string, options: XScienceSkillReadOptions = {}): Promise<string[]> {
  const source = options.source ? `?source=${options.source}` : '';
  const payload = asRecord(
    await requestJson(`/api/skills/catalog/${encodeURIComponent(name)}/files${source}`, options)
  );
  return Array.isArray(payload?.files)
    ? payload.files.filter((value): value is string => typeof value === 'string')
    : [];
}

export async function loadXScienceSkillFileContent(
  name: string,
  path: string,
  options: XScienceSkillReadOptions = {}
): Promise<string> {
  return (await loadXScienceSkillFileSnapshot(name, path, options)).content;
}

/** A draft identity belongs to these displayed bytes, never a later publish-time read. */
export async function loadXScienceSkillFileSnapshot(
  name: string,
  path: string,
  options: XScienceSkillReadOptions = {}
): Promise<XScienceSkillFileSnapshot> {
  const source = options.source ? `&source=${options.source}` : '';
  const payload = asRecord(
    await requestJson(
      `/api/skills/catalog/${encodeURIComponent(name)}/content?path=${encodeURIComponent(path)}${source}`,
      options
    )
  );
  if (typeof payload?.content !== 'string') {
    throw new XScienceSkillLibraryError(502, 'Skill file response is invalid');
  }
  const origin = typeof payload.origin === 'string' ? payload.origin : '';
  const revision = payload.draft_revision;
  const draftRevision =
    options.source !== 'published' &&
    origin === 'draft' &&
    typeof revision === 'string' &&
    /^[a-f0-9]{64}$/i.test(revision)
      ? revision
      : null;
  const files = snapshotFiles(payload.files, path);
  if (draftRevision && !files) {
    throw new XScienceSkillLibraryError(502, 'Skill draft snapshot inventory is invalid', 'draft_snapshot_invalid');
  }
  return {
    content: payload.content,
    origin,
    draftRevision,
    files,
  };
}

/** The server owns package policy; this checks the frozen receipt's wire shape. */
function snapshotFiles(value: unknown, selectedPath: string): string[] | null {
  // Mirrors the existing maxSkillArchiveFiles wire inventory bound, not a new limit.
  if (!Array.isArray(value) || value.length === 0 || value.length > 512) return null;
  if (
    !value.every(
      (file): file is string =>
        typeof file === 'string' &&
        !file.includes('\0') &&
        file.split('/').every((part) => part !== '' && part !== '.' && part !== '..')
    )
  )
    return null;
  if (new Set(value).size !== value.length || !value.includes(selectedPath)) return null;
  return [...value];
}

export async function saveXScienceSkillDraftFile(
  name: string,
  path: string,
  oldContent: string,
  newContent: string,
  options: XScienceSkillLibraryOptions = {}
): Promise<RecordValue> {
  const payload = await requestJson(`/api/skills/${encodeURIComponent(name)}/edit`, options, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, old_string: oldContent, new_string: newContent }),
  });
  return asRecord(payload) ?? {};
}

export async function duplicateXScienceSkill(
  sourceName: string,
  name: string,
  options: XScienceSkillLibraryOptions = {}
): Promise<RecordValue> {
  const payload = await requestJson(`/api/skills/${encodeURIComponent(name)}/duplicate`, options, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ sourceName }),
  });
  return asRecord(payload) ?? {};
}

export async function publishXScienceSkillDraft(
  name: string,
  overwrite: boolean,
  options: XScienceSkillPublishOptions
): Promise<RecordValue> {
  const revision = options?.expectedDraftRevision;
  if (typeof revision !== 'string' || !/^[a-f0-9]{64}$/i.test(revision)) {
    throw new XScienceSkillLibraryError(
      428,
      'Read and review the current skill draft before publishing.',
      'draft_revision_required'
    );
  }
  const payload = await requestJson(`/api/skills/${encodeURIComponent(name)}/publish`, options, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ overwrite, expected_draft_revision: revision }),
  });
  return asRecord(payload) ?? {};
}

export async function deleteXScienceSkillDraft(name: string, options: XScienceSkillLibraryOptions = {}): Promise<void> {
  await requestJson(`/api/skills/${encodeURIComponent(name)}`, options, { method: 'DELETE' });
}

export async function deleteXSciencePersonalSkill(
  name: string,
  options: XScienceSkillLibraryOptions = {}
): Promise<void> {
  await requestJson(`/api/skills/${encodeURIComponent(name)}/full`, options, { method: 'DELETE' });
}

export async function removeXScienceSkillSource(
  slug: string,
  options: XScienceSkillLibraryOptions = {}
): Promise<void> {
  await requestJson(`/api/marketplace/sources/${encodeURIComponent(slug)}`, options, { method: 'DELETE' });
}

function toDraft(value: unknown): XScienceSkillDraft | null {
  if (typeof value === 'string') {
    const name = value.trim();
    if (!name) return null;
    return {
      name,
      displayName: humanizeSkillName(name),
      description: '',
      files: [],
      updatedAt: null,
    };
  }
  const row = asRecord(value);
  const name = stringValue(row?.name);
  if (!row || !name) return null;
  return {
    name,
    displayName: stringValue(row.displayName ?? row.display_name) || name,
    description: stringValue(row.description),
    files: Array.isArray(row.files) ? row.files.filter((item): item is string => typeof item === 'string') : [],
    updatedAt: nullableStringValue(row.updatedAt ?? row.updated_at),
  };
}

function humanizeSkillName(value: string): string {
  return value
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
}

function toSource(value: unknown): XScienceSkillSource | null {
  const row = asRecord(value);
  const slug = stringValue(row?.slug);
  if (!row || !slug) return null;
  return {
    slug,
    repo: stringValue(row.repo ?? row.url),
    sha: stringValue(row.sha ?? row.pinnedSha ?? row.pinned_sha),
    license: nullableStringValue(row.license),
    skills: toStringArray(row.skills ?? row.skillNames ?? row.skill_names),
    importedAt: nullableStringValue(row.importedAt ?? row.imported_at ?? row.lastImportedAt ?? row.last_imported_at),
    removable: row.removable !== false,
  };
}

function toRepoEntry(value: unknown): XScienceSkillRepoEntry | null {
  const row = asRecord(value);
  const name = stringValue(row?.name);
  if (!row || !name) return null;
  return {
    name,
    displayName: stringValue(row.displayName ?? row.display_name) || name,
    description: stringValue(row.description),
    path: nullableStringValue(row.path),
    selected: row.selected !== false,
  };
}

function toImportResult(value: unknown): XScienceSkillImportResult {
  const row = asRecord(value);
  const skipped = Array.isArray(row?.skipped) ? row.skipped : [];
  return {
    imported: toStringArray(row?.imported),
    skipped: skipped
      .map((entry) => {
        const item = asRecord(entry);
        const name = stringValue(item?.name);
        return item && name ? { name, reason: stringValue(item.reason) } : null;
      })
      .filter((entry): entry is { name: string; reason: string } => entry !== null),
    slug: nullableStringValue(row?.slug),
    sha: nullableStringValue(row?.sha),
  };
}

async function requestJson(
  path: string,
  options: XScienceSkillLibraryOptions,
  init: RequestInit = { headers: { Accept: 'application/json' } }
): Promise<unknown> {
  const response = await (options.fetchImpl ?? fetch)(`${normalizeBaseUrl(options.baseUrl)}${path}`, init);
  const payload = await parsePayload(response);
  if (!response.ok) {
    const row = asRecord(payload);
    throw new XScienceSkillLibraryError(
      response.status,
      stringValue(row?.detail ?? row?.message ?? row?.error) || `Skill library request failed (${response.status})`,
      typeof row?.code === 'string' ? row.code : null
    );
  }
  return payload;
}

async function parsePayload(response: Response): Promise<unknown> {
  if (response.status === 204) return null;
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

function normalizeBaseUrl(value: string | undefined): string {
  return (value ?? '').replace(/\/$/, '');
}

function asRecord(value: unknown): RecordValue | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordValue) : null;
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : value == null ? '' : String(value);
}

function nullableStringValue(value: unknown): string | null {
  const normalized = stringValue(value).trim();
  return normalized || null;
}

function toStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}
