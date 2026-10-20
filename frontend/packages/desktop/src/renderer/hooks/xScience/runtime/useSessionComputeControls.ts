import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Message } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { isXScienceHttpError } from '@/renderer/services/xScienceHttp';
import {
  loadXScienceComputeProviders,
  setXScienceSessionComputeProvider,
  type XScienceComputeProvider,
} from '@/renderer/services/xScienceCompute';
import {
  loadXScienceSessionComputeState,
  type SessionComputeState,
} from '@/renderer/services/runtime/computeSessionState';

function computeFailureDiagnostic(error: unknown): { kind: 'http' | 'unknown'; status?: number } {
  if (!isXScienceHttpError(error)) return { kind: 'unknown' };
  return Number.isInteger(error.status) && error.status >= 100 && error.status <= 599
    ? { kind: 'http', status: error.status }
    : { kind: 'http' };
}

export function computeConversationRoot(id: string, ownerId: string, conversation?: TChatConversation): string | null {
  const extra = conversation?.type === 'acp' && conversation.id === id ? conversation.extra : null;
  return ownerId &&
    ownerId === getRendererAccountOwnerId() &&
    typeof extra?.frame_incarnation_id === 'string' &&
    extra.frame_incarnation_id.trim() &&
    typeof extra.root_frame_id === 'string' &&
    extra.root_frame_id.trim()
    ? extra.root_frame_id
    : null;
}

export type SessionComputeControl = {
  rootFrameId: string | null | undefined;
  providers: XScienceComputeProvider[];
  enabled: string[];
  selection: string[] | undefined;
  status: 'loading' | 'ready' | 'error';
  hasSnapshot: boolean;
  sessionState?: SessionComputeState;
  pending: string | null;
  disabled: boolean;
  inspect: () => () => void;
  retry: () => void;
  change: (name: string, checked: boolean) => Promise<void>;
  selectLocal: () => Promise<void>;
};

/** One conversation owner survives presentation/viewport changes. Inspection is only a UI lease. */
export function useSessionComputeControls({
  rootFrameId,
  contextKey,
  active = false,
  disabled = false,
  selection,
  onSelectionChange,
}: {
  rootFrameId?: string | null;
  contextKey?: string;
  active?: boolean;
  disabled?: boolean;
  selection?: string[];
  onSelectionChange?: (selection: string[]) => void;
}): SessionComputeControl {
  const { t } = useTranslation();
  const translation = useRef(t);
  translation.current = t;
  const account = getRendererAccountScopeToken();
  const owner = useMemo(() => ({ account, rootFrameId, contextKey }), [account, rootFrameId, contextKey]);
  const currentOwner = useRef(owner);
  currentOwner.current = owner;
  const mounted = useRef(true);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const current = useCallback(
    () => mounted.current && currentOwner.current === owner && getRendererAccountScopeToken() === account,
    [account, owner]
  );
  const generation = useRef(0);
  type Inspection = { owner: typeof owner };
  const inspection = useRef<Inspection | null>(null);
  const [lease, setLease] = useState<Inspection | null>(null);
  const [retry, setRetry] = useState(0);
  const [view, setView] = useState<{
    owner: typeof owner;
    status: 'loading' | 'ready' | 'error';
    hasSnapshot: boolean;
    providers: XScienceComputeProvider[];
    enabled: string[];
    sessionState?: SessionComputeState;
  } | null>(null);
  const [pending, setPending] = useState<{ owner: typeof owner; provider: string } | null>(null);
  const write = useRef<{ owner: typeof owner; settled: Promise<void> } | null>(null);
  const draft = useRef(selection);
  draft.current = selection;
  const inspect = useCallback(() => {
    if (!current()) return () => {};
    const token = { owner };
    inspection.current = token;
    setLease(token);
    return () => {
      if (inspection.current !== token) return;
      inspection.current = null;
      generation.current++;
      if (mounted.current) setLease(null);
    };
  }, [current, owner]);
  useEffect(() => (active ? inspect() : undefined), [active, inspect]);

  useEffect(() => {
    if (!lease || lease.owner !== owner || rootFrameId === null) return;
    let live = true;
    const sequence = ++generation.current;
    const valid = () => live && current() && inspection.current === lease && generation.current === sequence;
    setView((previous) => ({
      owner,
      status: 'loading',
      hasSnapshot: previous?.owner === owner && previous.hasSnapshot,
      providers: previous?.owner === owner ? previous.providers : [],
      enabled: previous?.owner === owner ? previous.enabled : [],
      sessionState: previous?.owner === owner ? previous.sessionState : undefined,
    }));
    const pendingWrite = write.current;
    const settled = pendingWrite?.owner === owner ? pendingWrite.settled : Promise.resolve();
    void settled
      .then(() => {
        if (!valid()) return;
        return Promise.all([
          loadXScienceComputeProviders(),
          rootFrameId ? loadXScienceSessionComputeState(rootFrameId) : Promise.resolve(null),
        ] as const);
      })
      .then((snapshot) => {
        if (!snapshot || !valid()) return;
        const [providers, sessionState] = snapshot;
        if (rootFrameId && !sessionState) throw new Error('Session compute state is unavailable');
        setView({
          owner,
          status: 'ready',
          hasSnapshot: true,
          providers: providers.filter((provider) => provider.checked && provider.name !== 'local'),
          enabled: (rootFrameId ? sessionState!.enabled : (draft.current ?? [])).filter(
            (name: string) => name !== 'local'
          ),
          sessionState: sessionState ?? undefined,
        });
      })
      .catch((error) => {
        if (!valid()) return;
        console.error('Failed to load session compute:', computeFailureDiagnostic(error));
        setView((previous) => ({
          owner,
          status: 'error',
          hasSnapshot: previous?.owner === owner && previous.hasSnapshot,
          providers: previous?.owner === owner ? previous.providers : [],
          enabled: previous?.owner === owner ? previous.enabled : [],
          sessionState: previous?.owner === owner ? previous.sessionState : undefined,
        }));
        Message.error(translation.current('conversation.xscienceRuntime.sessionOptions.computeState.read'));
      });
    return () => {
      live = false;
      generation.current++;
    };
  }, [current, lease, owner, retry, rootFrameId]);

  const owned = view?.owner === owner ? view : null;
  const hasSnapshot = owned?.hasSnapshot === true;
  const enabled =
    rootFrameId === undefined && selection !== undefined
      ? selection.filter((name) => name !== 'local')
      : (owned?.enabled ?? []);
  const status = owned?.status ?? 'loading';
  const pendingProvider = pending?.owner === owner ? pending.provider : null;
  const canChange = () =>
    current() &&
    !disabled &&
    status === 'ready' &&
    rootFrameId !== null &&
    inspection.current?.owner === owner &&
    pendingProvider === null &&
    write.current?.owner !== owner;
  const persist = async (provider: string, mutate: () => Promise<void>) => {
    const sequence = ++generation.current;
    let settle!: () => void;
    const operation = {
      owner,
      settled: new Promise<void>((resolve) => {
        settle = resolve;
      }),
    };
    write.current = operation;
    setPending({ owner, provider });
    setView((previous) => (previous?.owner === owner ? { ...previous, status: 'loading' } : previous));
    const valid = () => current() && inspection.current?.owner === owner && generation.current === sequence;
    try {
      await mutate();
      if (valid()) setRetry((attempt) => attempt + 1);
    } catch (error) {
      // An unknown write is not rollback. Only an explicit GET retry may observe the current state.
      if (valid()) {
        console.error('Session compute write outcome could not be confirmed:', computeFailureDiagnostic(error));
        setView((previous) => (previous?.owner === owner ? { ...previous, status: 'error' } : previous));
        Message.error(translation.current('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
      }
    } finally {
      settle();
      if (write.current === operation) {
        write.current = null;
        if (current()) setPending(null);
      }
    }
  };
  return {
    rootFrameId,
    providers: hasSnapshot ? owned!.providers : [],
    enabled,
    selection:
      rootFrameId === null
        ? undefined
        : rootFrameId === undefined && selection !== undefined
          ? enabled
          : hasSnapshot
            ? enabled
            : undefined,
    status,
    hasSnapshot,
    sessionState: hasSnapshot ? owned?.sessionState : undefined,
    pending: pendingProvider,
    disabled,
    inspect,
    retry: () => {
      if (current() && inspection.current?.owner === owner && status === 'error') setRetry((attempt) => attempt + 1);
    },
    change: async (name, checked) => {
      if (!canChange()) return;
      if (rootFrameId) {
        await persist(name, () => setXScienceSessionComputeProvider(rootFrameId, name, checked));
        return;
      }
      onSelectionChange?.(checked ? Array.from(new Set([...enabled, name])) : enabled.filter((item) => item !== name));
    },
    selectLocal: async () => {
      if (!canChange() || enabled.length === 0) return;
      if (!rootFrameId) {
        onSelectionChange?.([]);
        return;
      }
      await persist('__local__', async () => {
        const results = await Promise.allSettled(
          enabled.map((name) => setXScienceSessionComputeProvider(rootFrameId, name, false))
        );
        const failed = results.find((result) => result.status === 'rejected');
        if (failed?.status === 'rejected') throw failed.reason;
      });
    },
  };
}
