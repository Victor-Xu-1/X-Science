import { useCallback, useEffect, useRef, useState } from 'react';
import {
  loadSynonBiomedSkillFileContent,
  loadSynonBiomedSkillFiles,
} from '@/renderer/services/skills/synonBiomedSkillLibrary';
import type { SkillDetailSession } from './useSkillDetailSession';

const emptyFile = {
  scope: null as symbol | null,
  files: [] as string[],
  path: '',
  content: '',
  original: '',
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
  mode: string
) {
  const [file, setFile] = useState<FileState>(emptyFile);
  const readRevision = useRef(0);
  const { capture, isCurrent } = session;
  const read = useCallback(
    async (selectedPath?: string) => {
      const ticket = capture();
      if (!ticket) return;
      const request = ++readRevision.current;
      const current = () => isCurrent(ticket) && readRevision.current === request;
      let failedAt: FileState['failedAt'] = selectedPath ? 'content' : 'list';
      setFile((previous) => ({
        ...previous,
        scope: ticket.scope,
        path: selectedPath ?? '',
        content: '',
        original: '',
        status: 'loading',
      }));
      try {
        let nextPath = selectedPath;
        if (!nextPath) {
          const files = await loadSynonBiomedSkillFiles(ticket.name);
          if (!current()) return;
          nextPath = files.includes('SKILL.md') ? 'SKILL.md' : (files[0] ?? '');
          setFile({ ...emptyFile, scope: ticket.scope, files, path: nextPath, status: nextPath ? 'loading' : 'ready' });
          if (!nextPath) return;
          failedAt = 'content';
        }
        const content = await loadSynonBiomedSkillFileContent(ticket.name, nextPath);
        if (current())
          setFile((previous) => ({ ...previous, path: nextPath, content, original: content, status: 'ready' }));
      } catch (error) {
        if (!current()) return;
        console.error('Failed to read skill files:', error);
        setFile((previous) => ({ ...previous, status: 'failed', failedAt }));
      }
    },
    [capture, isCurrent]
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
    retry: () => read(file.failedAt === 'content' ? file.path : undefined),
    setContent: (content: string) => setFile((previous) => ({ ...previous, content })),
    markSaved: (path: string, content: string) =>
      setFile((previous) => (previous.path === path ? { ...previous, original: content } : previous)),
    clear: () => {
      if (!capture()) setFile(emptyFile);
    },
  };
}

export type SkillDetailFiles = ReturnType<typeof useSkillDetailFiles>;
