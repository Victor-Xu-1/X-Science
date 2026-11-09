import { useCallback, useEffect, useRef, useState } from 'react';
import type { LiteratureStudyBatchCommand, LiteratureStudyBatchView } from './literatureStudyBatchContract';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { requestStudyBatch } from './literatureStudyBatchApi';

type Work = { abort: AbortController; generation: number; wave?: number };
/** One page/account request owner; all durable progress lives in the Store. */
export function useLibraryStudyBatch(account: string, language: string, selectionKey: string) {
  const [view, setView] = useState<LiteratureStudyBatchView>();
  const [busy, setBusy] = useState(false);
  const [pausing, setPausing] = useState(false);
  const [issue, setIssue] = useState<'failed' | 'limit' | undefined>();
  const [open, setOpen] = useState(false);
  const [observation, setObservation] = useState<{ work: Work; batchId: string; wave: number }>();
  const owner = useRef({
    account,
    generation: 0,
    work: undefined as Work | undefined,
    stop: false,
    selectionKey,
    view: undefined as LiteratureStudyBatchView | undefined,
  });
  const intent = useRef<
    { key: string; command: Extract<LiteratureStudyBatchCommand, { action: 'start' }> } | undefined
  >(undefined);
  owner.current.selectionKey = selectionKey;
  const current = (work: Work) =>
    owner.current.account === account &&
    account === getRendererAccountScopeToken() &&
    owner.current.work === work &&
    !work.abort.signal.aborted;
  const publish = useCallback((next: LiteratureStudyBatchView) => {
    const previous = owner.current.view;
    if (
      previous?.batch &&
      next.batch &&
      previous.batch.id === next.batch.id &&
      previous.batch.revision > next.batch.revision
    )
      return;
    const pending = intent.current?.command;
    if (
      pending &&
      next.batch?.id === pending.operation_id &&
      next.batch.rows.length === pending.item_ids.length &&
      next.batch.rows.every(
        (row, index) => row.item_id === pending.item_ids[index] && row.request.language === pending.language
      )
    )
      intent.current = undefined;
    owner.current.view = next;
    setView(next);
  }, []);
  useEffect(() => {
    const state = {
      account,
      generation: owner.current.generation + 1,
      work: undefined as Work | undefined,
      stop: false,
      selectionKey: owner.current.selectionKey,
      view: undefined as LiteratureStudyBatchView | undefined,
    };
    owner.current = state;
    setView(undefined);
    setIssue(undefined);
    setBusy(false);
    setPausing(false);
    setOpen(false);
    setObservation(undefined);
    intent.current = undefined;
    const initialGeneration = state.generation;
    const read = new AbortController();
    void requestStudyBatch(read.signal)
      .then((value) => {
        if (
          owner.current === state &&
          state.generation === initialGeneration &&
          !read.signal.aborted &&
          !state.work &&
          account === getRendererAccountScopeToken()
        )
          publish(value);
      })
      .catch(() => {
        if (owner.current === state && state.generation === initialGeneration && !read.signal.aborted)
          setIssue('failed');
      });
    const cancel = () => {
      read.abort();
      state.work?.abort.abort();
      state.work = undefined;
      state.generation++;
    };
    const unregister = registerRendererAccountReset(`library-study-batch:${account}`, cancel);
    return () => {
      unregister();
      cancel();
    };
  }, [account]);

  // Visible progress only: this GET never advances work or renews its claim.
  // One read is in flight and the exact account/batch/wave owns every response.
  useEffect(() => {
    if (!observation || !open || pausing) return;
    const read = new AbortController();
    let reading = false;
    const timer = setInterval(() => {
      if (reading || read.signal.aborted || owner.current.work !== observation.work) return;
      reading = true;
      void requestStudyBatch(read.signal)
        .then((value) => {
          if (
            !read.signal.aborted &&
            owner.current.work === observation.work &&
            observation.work.wave === observation.wave &&
            account === getRendererAccountScopeToken() &&
            value.batch?.id === observation.batchId
          )
            publish(value);
        })
        .catch(() => {
          /* The active request remains authoritative; a read error never retries work. */
        })
        .finally(() => {
          reading = false;
        });
    }, 1000);
    return () => {
      clearInterval(timer);
      read.abort();
    };
  }, [observation, open, pausing, account, publish]);

  const run = async (prepare: (work: Work) => Promise<LiteratureStudyBatchCommand | undefined>, advance: boolean) => {
    if (owner.current.work || account !== getRendererAccountScopeToken()) return;
    const work: Work = { abort: new AbortController(), generation: ++owner.current.generation };
    owner.current.work = work;
    owner.current.stop = false;
    setBusy(true);
    setPausing(false);
    setIssue(undefined);
    setOpen(true);
    try {
      const command = await prepare(work);
      if (!command || !current(work)) return;
      let value = await requestStudyBatch(work.abort.signal, command);
      if (!current(work)) return;
      publish(value);
      while (current(work) && !owner.current.stop && value.batch && !value.batch.paused) {
        if (!advance) break;
        const rows = value.batch.rows
          .flatMap((row, index) => (row.state === 'pending' || row.state === 'running' ? [index] : []))
          .slice(0, value.concurrency);
        if (!rows.length) break;
        work.wave = (work.wave ?? 0) + 1;
        setObservation({ work, batchId: value.batch.id, wave: work.wave });
        // oxlint-disable-next-line no-await-in-loop -- A wave must settle before the next bounded wave starts.
        value = await requestStudyBatch(work.abort.signal, { action: 'advance', batch_id: value.batch.id, rows });
        if (!current(work)) return;
        setObservation(undefined);
        publish(value);
      }
      if (current(work) && owner.current.stop && value.batch && !value.batch.paused) {
        value = await requestStudyBatch(work.abort.signal, {
          action: 'pause',
          operation_id: crypto.randomUUID(),
          batch_id: value.batch.id,
          expected_revision: value.batch.revision,
        });
        if (current(work)) publish(value);
      }
    } catch {
      if (current(work)) setIssue('failed');
    } finally {
      if (current(work)) {
        owner.current.work = undefined;
        setObservation(undefined);
        setBusy(false);
        setPausing(false);
      }
    }
  };
  const start = (resolveSelection: (signal: AbortSignal) => Promise<readonly string[]>) => {
    const capturedKey = selectionKey;
    return run(async (work) => {
      const ids = await resolveSelection(work.abort.signal);
      if (!current(work) || capturedKey !== owner.current.selectionKey) return;
      if (!ids.length || ids.length > (owner.current.view?.limit ?? 5)) {
        setIssue('limit');
        return;
      }
      const key = JSON.stringify([capturedKey, language, ids]);
      if (intent.current?.key !== key)
        intent.current = {
          key,
          command: { action: 'start', operation_id: crypto.randomUUID(), language, item_ids: [...ids] },
        };
      return intent.current.command;
    }, true);
  };
  const control = (action: 'pause' | 'resume' | 'retry') =>
    run(async () => {
      const latest = await requestStudyBatch(owner.current.work!.abort.signal);
      if (!latest.batch) return;
      return {
        action,
        operation_id: crypto.randomUUID(),
        batch_id: latest.batch.id,
        expected_revision: latest.batch.revision,
      };
    }, action !== 'pause');
  const pause = () => {
    if (owner.current.work) {
      owner.current.stop = true;
      setPausing(true);
      return Promise.resolve();
    }
    return control('pause');
  };
  const refresh = async () => {
    if (owner.current.work || account !== getRendererAccountScopeToken()) return;
    const work = { abort: new AbortController(), generation: ++owner.current.generation };
    owner.current.work = work;
    setBusy(true);
    setIssue(undefined);
    try {
      const next = await requestStudyBatch(work.abort.signal);
      if (current(work)) publish(next);
    } catch {
      if (current(work)) setIssue('failed');
    } finally {
      if (current(work)) {
        owner.current.work = undefined;
        setBusy(false);
      }
    }
  };
  const owned = owner.current.account === account && account === getRendererAccountScopeToken();
  return {
    view: owned ? view : undefined,
    busy: owned && busy,
    pausing: owned && pausing,
    issue: owned ? issue : undefined,
    open: owned && open,
    setOpen,
    start,
    pause,
    resume: () => control('resume'),
    retry: () => control('retry'),
    refresh,
  };
}
