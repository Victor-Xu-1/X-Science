/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useRef, useState } from 'react';
import {
  loadSynonBiomedProject,
  loadSynonBiomedLinkedTask,
  type SynonBiomedProject,
  type SynonBiomedLinkedTask,
} from '@/renderer/services/synonBiomedGateway';
import type { ArtifactReadState } from './useArtifactMetadataReads';

type Values = { project: SynonBiomedProject | null; task: SynonBiomedLinkedTask | null };
type Section = keyof Values;
type Snapshot = { owner: string; sections: { [K in Section]: ArtifactReadState<Values[K]> } };
type Pending = { controller: AbortController; timer: ReturnType<typeof setTimeout> };
type Scope = { owner: string; active: boolean; pending: Map<Section, Pending> };
const READ_TIMEOUT_MS = 15_000;

function initial(owner: string, projectId: string | null, taskId: string | null): Snapshot {
  return {
    owner,
    sections: {
      project: { status: projectId ? 'loading' : 'ready', value: null },
      task: { status: taskId ? 'loading' : 'ready', value: null },
    },
  };
}

/** Optional names never own or block the file, version, or navigation identity. */
export function useArtifactContextReads(projectId: string | null, taskId: string | null) {
  const owner = JSON.stringify([projectId, taskId]);
  const [snapshot, setSnapshot] = useState(() => initial(owner, projectId, taskId));
  const scope = useRef<Scope | null>(null);
  const start = (key: Section) => {
    const captured = scope.current;
    if (
      !captured?.active ||
      captured.owner !== owner ||
      captured.pending.has(key) ||
      (key === 'project' && !projectId) ||
      (key === 'task' && !taskId)
    )
      return;
    const controller = new AbortController();
    const pending = { controller, timer: setTimeout(() => controller.abort(), READ_TIMEOUT_MS) };
    captured.pending.set(key, pending);
    const current = () => captured.active && scope.current === captured && captured.pending.get(key) === pending;
    const update = (status: ArtifactReadState<unknown>['status'], value?: Values[Section]) => {
      if (!current()) return;
      setSnapshot((previous) =>
        previous.owner === owner
          ? {
              ...previous,
              sections: {
                ...previous.sections,
                [key]: { status, value: value === undefined ? previous.sections[key].value : value },
              },
            }
          : previous
      );
    };
    update('loading');
    void Promise.resolve()
      .then(async () => {
        if (!current()) throw new Error('obsolete_artifact_context_read');
        if (key === 'project' && projectId) return loadSynonBiomedProject(projectId, { signal: controller.signal });
        if (!taskId) return null;
        const task = await loadSynonBiomedLinkedTask(taskId, { signal: controller.signal });
        if (projectId && task.projectId && task.projectId !== projectId)
          throw new Error('artifact_context_project_mismatch');
        return task;
      })
      .then((value) => {
        if (controller.signal.aborted) throw new DOMException('The read was aborted', 'AbortError');
        update('ready', value);
      })
      .catch(() => update('failed'))
      .finally(() => {
        clearTimeout(pending.timer);
        if (captured.pending.get(key) === pending) captured.pending.delete(key);
      });
  };
  useLayoutEffect(() => {
    const captured: Scope = { owner, active: true, pending: new Map() };
    scope.current = captured;
    setSnapshot(initial(owner, projectId, taskId));
    start('project');
    start('task');
    return () => {
      captured.active = false;
      for (const pending of captured.pending.values()) {
        clearTimeout(pending.timer);
        pending.controller.abort();
      }
      captured.pending.clear();
    };
  }, [owner]);
  const visible = snapshot.owner === owner ? snapshot : initial(owner, projectId, taskId);
  return { ...visible.sections, owner, retry: start };
}
