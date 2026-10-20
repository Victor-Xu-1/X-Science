import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  loadXSciencePlanDocument,
  loadXSciencePlanVersions,
  type XSciencePlanContentVersion,
} from '@/renderer/services/xScienceRuntimeOperations';
import type { XSciencePlanDocument, XSciencePlanReference } from './runtimeOperationsModel';
import { planReferenceIdentity } from './runtimeOperationsControllerModel';

type Owner = {
  key: string;
  account: string;
  active: boolean;
  request: number;
  controller: AbortController | null;
  versionsRequest: number;
  versionsController: AbortController | null;
};
type Preview = {
  owner: Owner;
  reference: XSciencePlanReference;
  displayProgress: XSciencePlanReference['progress'];
  document: XSciencePlanDocument | null;
  loading: boolean;
  error: string | null;
  versions: XSciencePlanContentVersion[] | null;
  versionsLoading: boolean;
  versionsError: string | null;
};

/** Extracted preview owner: selected immutable evidence is not the current Plan authority. */
export function useXSciencePlanPreview(conversationId: string, currentReference: XSciencePlanReference | null) {
  const { t } = useTranslation();
  const account = getRendererAccountScopeToken();
  const key = JSON.stringify([account, conversationId]);
  const owner = useMemo<Owner>(
    () => ({
      key,
      account,
      active: false,
      request: 0,
      controller: null,
      versionsRequest: 0,
      versionsController: null,
    }),
    [key, account]
  );
  const ownerRef = useRef(owner);
  ownerRef.current = owner;
  const currentRef = useRef(currentReference);
  currentRef.current = currentReference;
  const selectedRef = useRef<Preview | null>(null);
  const [state, setState] = useState<Preview | null>(null);
  const isOwner = useCallback(
    (target: Owner) =>
      target.active && ownerRef.current === target && getRendererAccountScopeToken() === target.account,
    []
  );
  const publish = useCallback((value: Preview | null) => {
    selectedRef.current = value;
    setState(value);
  }, []);
  const stopRead = useCallback((target: Owner) => {
    target.request += 1;
    target.controller?.abort();
    target.controller = null;
  }, []);
  const stopVersions = useCallback((target: Owner) => {
    target.versionsRequest += 1;
    target.versionsController?.abort();
    target.versionsController = null;
  }, []);

  useLayoutEffect(() => {
    owner.active = true;
    publish(null);
    return () => {
      owner.active = false;
      stopRead(owner);
      stopVersions(owner);
    };
  }, [owner, publish, stopRead, stopVersions]);

  const load = useCallback(
    (reference: XSciencePlanReference) => {
      if (!isOwner(owner)) return;
      stopRead(owner);
      const request = owner.request;
      const controller = new AbortController();
      owner.controller = controller;
      const captured = Object.freeze({
        ...reference,
        ...(reference.progress ? { progress: { ...reference.progress } } : {}),
      });
      const previous = selectedRef.current;
      const sameArtifact = previous?.owner === owner && previous.reference.artifactId === captured.artifactId;
      if (!sameArtifact) stopVersions(owner);
      const displayProgress =
        previous?.owner === owner && planReferenceIdentity(previous.reference) === planReferenceIdentity(captured)
          ? previous.displayProgress
          : captured.progress;
      publish({
        owner,
        reference: captured,
        displayProgress,
        document: null,
        loading: true,
        error: null,
        versions: sameArtifact ? previous.versions : null,
        versionsLoading: sameArtifact && previous.versionsLoading,
        versionsError: sameArtifact ? previous.versionsError : null,
      });
      const currentArtifact = currentRef.current?.artifactId;
      const ownsRead = () =>
        isOwner(owner) &&
        owner.request === request &&
        selectedRef.current?.reference === captured &&
        currentRef.current?.artifactId === currentArtifact;
      void loadXSciencePlanDocument(captured, { signal: controller.signal })
        .then((document) => {
          if (ownsRead()) publish({ ...selectedRef.current!, document });
        })
        .catch(() => {
          if (ownsRead())
            publish({
              ...selectedRef.current!,
              error: t('conversation.xscienceRuntime.runtimeOperations.planLoadFailed'),
            });
        })
        .finally(() => {
          if (!ownsRead()) return;
          if (owner.controller === controller) owner.controller = null;
          publish({ ...selectedRef.current!, loading: false });
        });
    },
    [isOwner, owner, publish, stopRead, stopVersions, t]
  );
  const open = useCallback(() => {
    if (!isOwner(owner) || !currentRef.current?.artifactId) return false;
    load(currentRef.current);
    return true;
  }, [isOwner, load, owner]);
  const close = useCallback(() => {
    if (!isOwner(owner)) return;
    stopRead(owner);
    stopVersions(owner);
    publish(null);
  }, [isOwner, owner, publish, stopRead, stopVersions]);

  const loadVersions = useCallback(() => {
    const selected = selectedRef.current;
    if (
      !isOwner(owner) ||
      selected?.owner !== owner ||
      selected.reference.artifactId !== currentRef.current?.artifactId
    )
      return;
    stopVersions(owner);
    const artifactId = selected.reference.artifactId;
    const request = owner.versionsRequest;
    const controller = new AbortController();
    owner.versionsController = controller;
    publish({ ...selected, versions: null, versionsLoading: true, versionsError: null });
    const ownsRead = () =>
      isOwner(owner) &&
      owner.versionsRequest === request &&
      selectedRef.current?.reference.artifactId === artifactId &&
      currentRef.current?.artifactId === artifactId;
    void loadXSciencePlanVersions(artifactId, { signal: controller.signal })
      .then((versions) => {
        if (ownsRead()) publish({ ...selectedRef.current!, versions });
      })
      .catch(() => {
        if (ownsRead())
          publish({
            ...selectedRef.current!,
            versionsError: t('conversation.xscienceRuntime.runtimeOperations.planVersionsFailed'),
          });
      })
      .finally(() => {
        if (!ownsRead()) return;
        if (owner.versionsController === controller) owner.versionsController = null;
        publish({ ...selectedRef.current!, versionsLoading: false });
      });
  }, [isOwner, owner, publish, stopVersions, t]);
  const selectVersion = useCallback(
    (versionId: string) => {
      const selected = selectedRef.current;
      const current = currentRef.current;
      if (!isOwner(owner) || selected?.owner !== owner || selected.reference.artifactId !== current?.artifactId) return;
      const version = selected.versions?.find((item) => item.versionId === versionId);
      if (version) load(version.versionId === current.versionId ? current : version);
    },
    [isOwner, load, owner]
  );

  const preview = state?.owner === owner ? state : null;
  const identity = planReferenceIdentity(preview?.reference);
  const currentIdentity = planReferenceIdentity(currentReference);
  const legacyChanged = Boolean(preview && preview.reference.versionId === null && identity !== currentIdentity);
  const currentArtifact = currentReference?.artifactId;
  useEffect(() => {
    const selected = selectedRef.current;
    if (!isOwner(owner) || selected?.owner !== owner || selected.reference.artifactId === currentArtifact) return;
    stopRead(owner);
    stopVersions(owner);
    publish({
      ...selected,
      loading: false,
      versions: null,
      versionsLoading: false,
      versionsError: null,
      error: selected.loading ? t('conversation.xscienceRuntime.runtimeOperations.planChanged') : selected.error,
    });
  }, [currentArtifact, isOwner, owner, publish, stopRead, stopVersions, t]);
  useEffect(() => {
    const selected = selectedRef.current;
    if (
      !isOwner(owner) ||
      selected?.owner !== owner ||
      planReferenceIdentity(selected.reference) !== planReferenceIdentity(currentRef.current) ||
      JSON.stringify(selected.displayProgress) === JSON.stringify(currentRef.current?.progress)
    )
      return;
    // Same-version presentation may advance. The immutable retry/decision source does not.
    const progress = currentRef.current?.progress;
    publish({ ...selected, displayProgress: progress ? { ...progress } : undefined });
  }, [currentReference, identity, isOwner, owner, publish]);
  useEffect(() => {
    const selected = selectedRef.current;
    if (
      !legacyChanged ||
      !isOwner(owner) ||
      selected?.owner !== owner ||
      selected.reference.versionId !== null ||
      planReferenceIdentity(selected.reference) === planReferenceIdentity(currentRef.current)
    )
      return;
    // A genuinely versionless source cannot become an immutable historical preview.
    stopRead(owner);
    publish({
      ...selected,
      document: null,
      loading: false,
      error: t('conversation.xscienceRuntime.runtimeOperations.planChanged'),
    });
  }, [isOwner, legacyChanged, owner, publish, stopRead, t]);
  const retry = useCallback(() => {
    const selected = selectedRef.current;
    if (!isOwner(owner) || selected?.owner !== owner) return;
    if (
      selected.reference.versionId === null &&
      planReferenceIdentity(selected.reference) !== planReferenceIdentity(currentRef.current)
    )
      return;
    load(selected.reference);
  }, [isOwner, load, owner]);
  const isSelected = useCallback(
    (reference: XSciencePlanReference | null | undefined) =>
      isOwner(owner) &&
      selectedRef.current?.owner === owner &&
      Boolean(reference) &&
      planReferenceIdentity(selectedRef.current.reference) === planReferenceIdentity(reference),
    [isOwner, owner]
  );
  const ownsPreview = useCallback(
    (reference: XSciencePlanReference) =>
      isOwner(owner) && selectedRef.current?.owner === owner && selectedRef.current.reference === reference,
    [isOwner, owner]
  );
  const captureDecision = useCallback(
    (current: XSciencePlanReference | null | undefined, approval: XSciencePlanReference | null | undefined) => {
      const selected = selectedRef.current;
      return selected?.document && !selected.loading && !selected.error && isSelected(current) && isSelected(approval)
        ? selected.reference
        : null;
    },
    [isSelected]
  );
  const setError = useCallback(
    (error: string, expected?: XSciencePlanReference) => {
      const selected = selectedRef.current;
      if (selected?.owner === owner && isOwner(owner) && (!expected || ownsPreview(expected)))
        publish({ ...selected, error });
    },
    [isOwner, ownsPreview, owner, publish]
  );
  const replaced = Boolean(preview?.reference.versionId && identity !== currentIdentity);
  const displayReference = preview
    ? replaced
      ? {
          artifactId: preview.reference.artifactId,
          versionId: preview.reference.versionId,
          ...(preview.reference.contentVersion != null ? { contentVersion: preview.reference.contentVersion } : {}),
        }
      : { ...preview.reference }
    : null;
  if (displayReference && !replaced) {
    if (preview!.displayProgress) displayReference.progress = preview!.displayProgress;
    else delete displayReference.progress;
  }
  return {
    visible: Boolean(preview),
    reference: displayReference,
    document: legacyChanged ? null : (preview?.document ?? null),
    loading: !legacyChanged && (preview?.loading ?? false),
    error: preview?.error ?? null,
    replaced,
    versions: preview?.versions ?? null,
    versionsLoading: preview?.versionsLoading ?? false,
    versionsError: preview?.versionsError ?? null,
    canLoadVersions: Boolean(preview && preview.reference.artifactId === currentArtifact),
    loadVersions,
    selectVersion,
    canRetry: !legacyChanged,
    canOpenCurrent: Boolean(currentReference),
    open,
    close,
    retry,
    isSelected,
    ownsPreview,
    captureDecision,
    setError,
  };
}
