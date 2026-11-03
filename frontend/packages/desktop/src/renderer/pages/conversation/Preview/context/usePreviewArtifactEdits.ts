import { useCallback, useEffect, useRef, useState } from 'react';
import { loadXScienceArtifact } from '@/renderer/services/xScienceGateway';
import {
  createXScienceTextArtifactVersion,
  getXScienceArtifactVersionContentUrl,
  loadXScienceArtifactVersionText,
} from '@/renderer/services/xScienceArtifacts';
import { isXScienceHttpError } from '@/renderer/services/xScienceHttp';
import { rendererAccountScopedKey } from '@/renderer/services/rendererAccountScope';
import { getPreviewVersionContentType } from '../previewContentPersistence';
import type { PreviewMetadata, PreviewTab, OpenPreviewOptions } from './PreviewContext';

export type PreviewArtifactEditState = {
  status: 'loading' | 'editing' | 'saving' | 'conflict' | 'error';
  restoring: boolean;
  currentHeadVersionId?: string;
};

export type PreviewArtifactSaveOutcome =
  | { kind: 'published'; artifactId: string; versionId: string; originApplied: boolean; draftClean: boolean }
  | { kind: 'conflict' | 'unconfirmed' };

type Baseline = {
  tabId: string;
  artifactId: string;
  parentVersionId: string;
  expectedHeadVersionId: string;
  account: string;
  intent?: { content: string; operationId: string };
  saving?: Promise<PreviewArtifactSaveOutcome>;
  read?: AbortController;
};

async function readEditSource<T>(baseline: Baseline, operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
  baseline.read?.abort();
  const controller = new AbortController();
  baseline.read = controller;
  const timeout = setTimeout(() => controller.abort(), 30_000);
  try {
    return await operation(controller.signal);
  } finally {
    clearTimeout(timeout);
    if (baseline.read === controller) baseline.read = undefined;
  }
}

type Options = {
  tabs: PreviewTab[];
  activeTabId: string | null;
  commit: (
    tabId: string,
    sourceVersionId: string,
    content: string,
    metadata: Partial<PreviewMetadata>,
    account: string
  ) => void;
  open: (
    content: string,
    type: PreviewTab['content_type'],
    metadata: PreviewMetadata,
    options?: OpenPreviewOptions
  ) => void;
  presentation: OpenPreviewOptions['presentation'];
};

// The original PreviewContext owns remote editing for both board and single
// views. A historical parent and a current-head baseline are separate facts.
export function usePreviewArtifactEdits({ tabs, activeTabId, commit, open, presentation }: Options) {
  const latest = useRef(tabs);
  latest.current = tabs;
  const active = useRef(activeTabId);
  active.current = activeTabId;
  const selected = tabs.find((tab) => tab.id === activeTabId);
  const navigationKey = JSON.stringify([
    activeTabId,
    selected?.metadata?.artifactId,
    selected?.metadata?.versionId,
    selected?.metadata?.file_path,
    selected?.content_type,
    rendererAccountScopedKey(''),
  ]);
  const navigation = useRef({ key: navigationKey, generation: 0 });
  if (navigation.current.key !== navigationKey)
    navigation.current = { key: navigationKey, generation: navigation.current.generation + 1 };
  const baselines = useRef(new Map<string, Baseline>());
  const account = rendererAccountScopedKey('');
  const [states, setStates] = useState<Record<string, PreviewArtifactEditState>>({});
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      for (const baseline of baselines.current.values()) baseline.read?.abort();
      baselines.current.clear();
    };
  }, []);

  const owns = useCallback((baseline: Baseline) => {
    const tab = latest.current.find((item) => item.id === baseline.tabId);
    return (
      mounted.current &&
      baselines.current.get(baseline.tabId) === baseline &&
      rendererAccountScopedKey('') === baseline.account &&
      tab?.metadata?.artifactId === baseline.artifactId &&
      (tab.metadata.versionId ?? '') === baseline.parentVersionId
    );
  }, []);
  const publish = useCallback(
    (baseline: Baseline, state: PreviewArtifactEditState) => {
      if (owns(baseline)) setStates((current) => ({ ...current, [baseline.tabId]: state }));
    },
    [owns]
  );
  const cancel = useCallback((tabId: string) => {
    baselines.current.get(tabId)?.read?.abort();
    baselines.current.delete(tabId);
    setStates((current) => {
      const next = { ...current };
      delete next[tabId];
      return next;
    });
  }, []);

  const begin = useCallback(
    async (tabId: string): Promise<boolean> => {
      const tab = latest.current.find((item) => item.id === tabId);
      if (!tab?.metadata?.artifactId) return Boolean(tab);
      const parentVersionId = tab.metadata.versionId ?? '';
      const baseline: Baseline = {
        tabId,
        artifactId: tab.metadata.artifactId,
        parentVersionId,
        expectedHeadVersionId: '',
        account: rendererAccountScopedKey(''),
      };
      baselines.current.get(tabId)?.read?.abort();
      baselines.current.set(tabId, baseline);
      if (!parentVersionId) {
        publish(baseline, { status: 'error', restoring: false });
        return false;
      }
      publish(baseline, { status: 'loading', restoring: false });
      try {
        const artifact = await readEditSource(baseline, (signal) =>
          loadXScienceArtifact(baseline.artifactId, { signal })
        );
        if (!owns(baseline)) return false;
        if (active.current !== tabId) {
          cancel(tabId);
          return false;
        }
        if (artifact.artifactId !== baseline.artifactId || !artifact.versionId)
          throw new Error('Artifact edit baseline is unavailable');
        baseline.expectedHeadVersionId = artifact.versionId;
        publish(baseline, { status: 'editing', restoring: parentVersionId !== artifact.versionId });
        return true;
      } catch {
        publish(baseline, { status: 'error', restoring: false });
        return false;
      }
    },
    [cancel, owns, publish]
  );

  const save = useCallback(
    (tabId: string, content: string): Promise<PreviewArtifactSaveOutcome> => {
      const baseline = baselines.current.get(tabId);
      if (!baseline || !baseline.expectedHeadVersionId || !owns(baseline))
        return Promise.resolve({ kind: 'unconfirmed' });
      if (baseline.saving) return baseline.saving;
      const tab = latest.current.find((item) => item.id === tabId)!;
      // Unknown-response retry retains the same operation. A changed draft is a
      // new intent, still fenced by the original captured head.
      if (baseline.intent?.content !== content) baseline.intent = { content, operationId: crypto.randomUUID() };
      const intent = baseline.intent;
      const restoring = baseline.parentVersionId !== baseline.expectedHeadVersionId;
      publish(baseline, { status: 'saving', restoring });
      const operation = (async (): Promise<PreviewArtifactSaveOutcome> => {
        try {
          const result = await createXScienceTextArtifactVersion({
            artifactId: baseline.artifactId,
            content,
            contentType: getPreviewVersionContentType(tab.content_type, tab.metadata?.file_name || tab.title),
            parentVersionId: baseline.parentVersionId,
            expectedHeadVersionId: baseline.expectedHeadVersionId,
            operationId: intent.operationId,
          });
          if (result.artifactId !== baseline.artifactId || !result.versionId)
            throw new Error('Artifact save identity mismatch');
          if (!owns(baseline))
            return {
              kind: 'published',
              artifactId: baseline.artifactId,
              versionId: result.versionId,
              originApplied: false,
              draftClean: false,
            };
          const draftClean = latest.current.find((item) => item.id === tabId)?.content === content;
          commit(
            tabId,
            baseline.parentVersionId,
            content,
            {
              versionId: result.versionId,
              contentUrl: getXScienceArtifactVersionContentUrl(result.versionId),
            },
            baseline.account
          );
          // Acknowledged publication becomes the baseline for any edits made
          // while the request was in flight; their draft bytes remain untouched.
          baseline.parentVersionId = result.versionId;
          baseline.expectedHeadVersionId = result.versionId;
          baseline.intent = undefined;
          setStates((current) => ({ ...current, [tabId]: { status: 'editing', restoring: false } }));
          return {
            kind: 'published',
            artifactId: baseline.artifactId,
            versionId: result.versionId,
            originApplied: true,
            draftClean,
          };
        } catch (error) {
          if (isXScienceHttpError(error) && error.status === 409 && error.code === 'ARTIFACT_HEAD_CONFLICT') {
            const detail =
              error.details && typeof error.details === 'object' ? (error.details as Record<string, unknown>) : {};
            const currentHeadVersionId =
              detail.artifact_id === baseline.artifactId && typeof detail.current_head_version_id === 'string'
                ? detail.current_head_version_id
                : undefined;
            publish(baseline, { status: 'conflict', restoring, currentHeadVersionId });
            return { kind: 'conflict' };
          }
          publish(baseline, { status: 'error', restoring });
          return { kind: 'unconfirmed' };
        }
      })();
      baseline.saving = operation;
      void operation.finally(() => {
        if (baseline.saving === operation) baseline.saving = undefined;
      });
      return operation;
    },
    [commit, owns, publish]
  );

  const openLatest = useCallback(
    async (tabId: string) => {
      const baseline = baselines.current.get(tabId);
      const versionId = states[tabId]?.currentHeadVersionId;
      if (!baseline || !versionId || !owns(baseline)) return;
      const tab = latest.current.find((item) => item.id === tabId)!;
      const navigationGeneration = navigation.current.generation;
      try {
        const content = await readEditSource(baseline, (signal) =>
          loadXScienceArtifactVersionText(versionId, { signal })
        );
        if (!owns(baseline) || navigation.current.generation !== navigationGeneration) return;
        open(
          content,
          tab.content_type,
          {
            ...tab.metadata,
            artifactId: baseline.artifactId,
            versionId,
            contentUrl: getXScienceArtifactVersionContentUrl(versionId),
          },
          { presentation }
        );
      } catch {
        if (navigation.current.generation === navigationGeneration)
          publish(baseline, {
            status: 'error',
            restoring: baseline.parentVersionId !== baseline.expectedHeadVersionId,
          });
      }
    },
    [open, owns, presentation, publish, states]
  );

  useEffect(() => {
    const retired: string[] = [];
    for (const [id, baseline] of baselines.current)
      if (!owns(baseline)) {
        baseline.read?.abort();
        baselines.current.delete(id);
        retired.push(id);
      }
    if (retired.length)
      setStates((current) => {
        const next = { ...current };
        for (const id of retired) delete next[id];
        return next;
      });
  }, [tabs, owns, account]);
  const visibleStates = Object.fromEntries(
    Object.entries(states).filter(([id]) => {
      const baseline = baselines.current.get(id);
      return baseline && owns(baseline);
    })
  );
  return { states: visibleStates, begin, cancel, save, openLatest };
}
