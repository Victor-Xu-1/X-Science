import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { loadXSciencePlanDocument } from '@/renderer/services/xScienceRuntimeOperations';
import type { XSciencePlanDocument, XSciencePlanReference } from './runtimeOperationsModel';
import { planReferenceIdentity } from './runtimeOperationsControllerModel';

type Owner = { key: string; account: string; active: boolean; request: number; controller: AbortController | null };
type Preview = {
  owner: Owner;
  reference: XSciencePlanReference;
  displayProgress: XSciencePlanReference['progress'];
  document: XSciencePlanDocument | null;
  loading: boolean;
  error: string | null;
};

/** Extracted preview owner: selected immutable evidence is not the current Plan authority. */
export function useXSciencePlanPreview(conversationId: string, currentReference: XSciencePlanReference | null) {
  const { t } = useTranslation();
  const account = getRendererAccountScopeToken();
  const key = JSON.stringify([account, conversationId]);
  const owner = useMemo<Owner>(() => ({ key, account, active: false, request: 0, controller: null }), [key, account]);
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

  useLayoutEffect(() => {
    owner.active = true;
    publish(null);
    return () => {
      owner.active = false;
      stopRead(owner);
    };
  }, [owner, publish, stopRead]);

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
      const displayProgress =
        previous?.owner === owner && planReferenceIdentity(previous.reference) === planReferenceIdentity(captured)
          ? previous.displayProgress
          : captured.progress;
      publish({ owner, reference: captured, displayProgress, document: null, loading: true, error: null });
      const ownsRead = () => isOwner(owner) && owner.request === request && selectedRef.current?.reference === captured;
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
    [isOwner, owner, publish, stopRead, t]
  );
  const open = useCallback(() => {
    if (!isOwner(owner) || !currentRef.current?.artifactId) return false;
    load(currentRef.current);
    return true;
  }, [isOwner, load, owner]);
  const close = useCallback(() => {
    if (!isOwner(owner)) return;
    stopRead(owner);
    publish(null);
  }, [isOwner, owner, publish, stopRead]);

  const preview = state?.owner === owner ? state : null;
  const identity = planReferenceIdentity(preview?.reference);
  const currentIdentity = planReferenceIdentity(currentReference);
  const legacyChanged = Boolean(preview && preview.reference.versionId === null && identity !== currentIdentity);
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
  const displayReference = preview ? { ...preview.reference } : null;
  if (displayReference) {
    if (preview!.displayProgress) displayReference.progress = preview!.displayProgress;
    else delete displayReference.progress;
  }
  return {
    visible: Boolean(preview),
    reference: displayReference,
    document: legacyChanged ? null : (preview?.document ?? null),
    loading: !legacyChanged && (preview?.loading ?? false),
    error: preview?.error ?? null,
    replaced: Boolean(preview?.reference.versionId && identity !== currentIdentity),
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
