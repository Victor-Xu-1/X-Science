import type {
  LiteratureDirection,
  RelatedIssue,
  RelatedRequest,
  RelatedResult,
} from '@/common/adapter/literatureRelated';
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { loadRelatedLiterature } from '@/renderer/services/runtime/literatureRelated';

type RelatedView = {
  source: string;
  direction: LiteratureDirection;
  result?: RelatedResult;
  issue?: RelatedIssue;
  retryAt?: number;
  request?: RelatedRequest;
  loading: boolean;
  cancelled: boolean;
  ownerChanged: boolean;
};

/** Transient query presentation only; the Library and its mutation owner remain authoritative. */
export function useLibraryRelatedLookup(itemId: string, revision: number, initialDirection: LiteratureDirection) {
  const [account] = useState(getRendererAccountScopeToken);
  const source = JSON.stringify([account, itemId, revision]);
  const currentSource = useRef<string | null>(source);
  useLayoutEffect(() => {
    currentSource.current = source;
    return () => {
      currentSource.current = null;
    };
  }, [source]);
  const registrationId = useId();
  const mounted = useRef(false);
  const sequence = useRef(0);
  const active = useRef<AbortController | null>(null);
  const [view, setView] = useState<RelatedView>(() => ({
    source,
    direction: initialDirection,
    loading: true,
    cancelled: false,
    ownerChanged: false,
  }));
  const abort = useCallback(() => {
    sequence.current++;
    active.current?.abort();
    active.current = null;
  }, []);

  const lookup = useCallback(
    async (direction: LiteratureDirection, offset = 0, resolutionToken?: string): Promise<void> => {
      if (!mounted.current || currentSource.current !== source || getRendererAccountScopeToken() !== account) return;
      abort();
      const controller = new AbortController();
      active.current = controller;
      const generation = sequence.current;
      const request: RelatedRequest = {
        item_id: itemId,
        expected_metadata_revision: revision,
        direction,
        offset,
        ...(resolutionToken ? { resolution_token: resolutionToken } : {}),
      };
      const current = () =>
        mounted.current &&
        currentSource.current === source &&
        getRendererAccountScopeToken() === account &&
        sequence.current === generation &&
        !controller.signal.aborted;
      setView((previous) =>
        current()
          ? {
              source,
              direction,
              result:
                offset > 0 && previous.source === source && previous.direction === direction
                  ? previous.result
                  : undefined,
              request,
              loading: true,
              cancelled: false,
              ownerChanged: false,
            }
          : previous
      );
      try {
        const response = await loadRelatedLiterature(request, controller.signal);
        if (!current()) return;
        setView((previous) => {
          // React may evaluate a queued updater after a source/account change or
          // after finally releases the request handle. Only the stable claim applies.
          if (!current()) return previous;
          if (response.state === 'unavailable')
            return {
              ...previous,
              result:
                response.issue === 'reference-changed' || response.issue === 'expired' ? undefined : previous.result,
              issue: response.issue ?? 'unavailable',
              retryAt: response.retry_at,
              loading: false,
            };
          const earlier = offset > 0 && previous.result?.state === 'ready' ? previous.result : undefined;
          const records = earlier
            ? [
                ...earlier.records,
                ...response.records.filter(
                  (record) => !earlier.records.some((old) => old.paper_id === record.paper_id)
                ),
              ]
            : response.records;
          return {
            source,
            direction,
            result: { ...response, records, omitted: (earlier?.omitted ?? 0) + (response.omitted ?? 0) },
            loading: false,
            cancelled: false,
            ownerChanged: false,
          };
        });
      } catch {
        if (current())
          setView((previous) => (current() ? { ...previous, issue: 'unavailable', loading: false } : previous));
      } finally {
        if (active.current === controller) active.current = null;
      }
    },
    [account, itemId, revision, source, abort]
  );

  useEffect(() => {
    mounted.current = true;
    const unregister = registerRendererAccountReset(`library-related:${registrationId}`, () => {
      abort();
      setView({ source, direction: initialDirection, loading: false, cancelled: false, ownerChanged: true });
    });
    void lookup(initialDirection);
    return () => {
      mounted.current = false;
      abort();
      unregister();
    };
  }, [abort, initialDirection, lookup, registrationId, source]);

  const cancel = () => {
    abort();
    setView((previous) => ({ ...previous, loading: false, cancelled: true }));
  };
  const retry = () => {
    const restart = view.issue === 'expired' || view.issue === 'reference-changed';
    void lookup(
      view.direction,
      restart ? 0 : view.request?.offset,
      restart ? undefined : view.request?.resolution_token
    );
  };
  const visible: RelatedView =
    view.source === source
      ? view
      : {
          source,
          direction: initialDirection,
          loading: true,
          cancelled: false,
          ownerChanged: getRendererAccountScopeToken() !== account,
        };
  return {
    ...visible,
    switchDirection: (direction: LiteratureDirection): void => {
      void lookup(direction);
    },
    confirmMatch: (): void => {
      void lookup(visible.direction, 0, visible.result?.resolution_token);
    },
    loadMore: () => {
      if (visible.result?.next_offset !== undefined)
        void lookup(visible.direction, visible.result.next_offset, visible.result.resolution_token);
    },
    cancel,
    retry,
  };
}
