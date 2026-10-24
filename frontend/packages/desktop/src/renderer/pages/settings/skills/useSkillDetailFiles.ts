import { useCallback, useEffect, useRef, useState } from 'react';
import { loadXScienceSkillFileSnapshot, loadXScienceSkillFiles } from '@/renderer/services/skills/xScienceSkillLibrary';
import type { SkillDetailSession } from './useSkillDetailSession';

const emptyFile = {
  scope: null as symbol | null,
  files: [] as string[],
  path: '',
  content: '',
  original: '',
  draftRevision: null as string | null,
  status: 'idle' as const,
  failedAt: 'list' as const,
};
type FileState = Omit<typeof emptyFile, 'status' | 'failedAt'> & {
  status: 'idle' | 'loading' | 'ready' | 'failed';
  failedAt: 'list' | 'content';
};

export function useSkillDetailFiles(
  session: SkillDetailSession,
  name: string | undefined,
  visible: boolean,
  mode: string,
  draft: boolean
) {
  const [file, setFile] = useState<FileState>(emptyFile);
  const readRevision = useRef(0);
  const { capture, isCurrent } = session;
  const read = useCallback(
    async (selectedPath?: string) => {
      const ticket = capture();
      if (!ticket) return false;
      const request = ++readRevision.current;
      const current = () => isCurrent(ticket) && readRevision.current === request;
      let failedAt: FileState['failedAt'] = selectedPath ? 'content' : 'list';
      setFile((previous) =>
        current()
          ? {
              ...previous,
              scope: ticket.scope,
              path: selectedPath ?? '',
              content: '',
              original: '',
              draftRevision: null,
              status: 'loading',
            }
          : previous
      );
      try {
        let nextPath = selectedPath;
        if (!nextPath) {
          const files = await loadXScienceSkillFiles(ticket.name, { source: draft ? 'draft' : 'published' });
          if (!current()) return false;
          nextPath = files.includes('SKILL.md') ? 'SKILL.md' : (files[0] ?? '');
          const selected = nextPath;
          setFile((previous) =>
            current()
              ? { ...emptyFile, scope: ticket.scope, files, path: selected, status: selected ? 'loading' : 'ready' }
              : previous
          );
          if (!nextPath) return true;
          failedAt = 'content';
        }
        const snapshot = await loadXScienceSkillFileSnapshot(ticket.name, nextPath, {
          source: draft ? 'draft' : 'published',
        });
        const selected = nextPath;
        if (current())
          setFile((previous) =>
            current()
              ? {
                  ...previous,
                  files: snapshot.files ?? previous.files,
                  path: selected,
                  content: snapshot.content,
                  original: snapshot.content,
                  draftRevision: draft ? snapshot.draftRevision : null,
                  status: 'ready',
                }
              : previous
          );
        return current();
      } catch (error) {
        if (!current()) return false;
        console.error('Failed to read skill files:', error);
        setFile((previous) =>
          current() ? { ...previous, draftRevision: null, status: 'failed', failedAt } : previous
        );
        return false;
      }
    },
    [capture, isCurrent, draft]
  );

  useEffect(() => {
    if (!visible || !name) return;
    setFile(emptyFile);
    void read();
    return () => {
      readRevision.current += 1;
    };
  }, [name, visible, mode, read]);

  // A committed replacement must not paint the preceding source while its
  // passive read effect starts. Closing keeps the existing exit presentation.
  const presentation = visible && file.scope !== session.scope ? emptyFile : file;
  return {
    ...presentation,
    loading: presentation.status === 'idle' || presentation.status === 'loading',
    ready: presentation.status === 'ready' && !!presentation.path,
    selectFile: (path: string) => (file.files.includes(path) ? read(path) : Promise.resolve()),
    reload: (path?: string) => read(path),
    retry: () => read(file.failedAt === 'content' ? file.path : undefined),
    setContent: (content: string) => setFile((previous) => ({ ...previous, content })),
    markSaved: (path: string, content: string) => {
      const ticket = capture();
      if (!ticket) return;
      setFile((previous) =>
        isCurrent(ticket) && previous.scope === ticket.scope && previous.path === path
          ? { ...previous, original: content, draftRevision: null }
          : previous
      );
    },
    invalidateDraftRevision: () => {
      const ticket = capture();
      if (!ticket) return;
      readRevision.current += 1;
      setFile((previous) =>
        isCurrent(ticket) && previous.scope === ticket.scope ? { ...previous, draftRevision: null } : previous
      );
    },
    clear: () => {
      if (!capture()) setFile(emptyFile);
    },
  };
}

export type SkillDetailFiles = ReturnType<typeof useSkillDetailFiles>;
