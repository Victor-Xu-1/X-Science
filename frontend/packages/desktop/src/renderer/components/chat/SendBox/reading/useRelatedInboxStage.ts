import { useEffect, useRef, useState } from 'react';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureDirection, RelatedRecord } from '@/common/adapter/literatureRelated';
import {
  decodeLiteratureCandidateCommand,
  type LiteratureCandidateCommand,
} from '@/common/adapter/literatureCandidates';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

type Result = 'pending' | 'accepted' | 'dismissed' | 'failed' | 'invalid';
type StageView = { scope: string; selected: string[]; results: Record<string, Result>; running: boolean };

/** Uses the single durable Library owner; closing a view never discards an admitted request. */
export function useRelatedInboxStage(item: LiteratureItem, direction: LiteratureDirection, records: RelatedRecord[]) {
  const mutation = useLiteratureMutation();
  const account = getRendererAccountScopeToken();
  const scope = JSON.stringify([account, item.id, item.metadata_revision, direction]);
  const owner = useRef({ scope });
  if (owner.current.scope !== scope) owner.current = { scope };
  const mounted = useRef(false);
  const active = useRef(false);
  const [view, setView] = useState<StageView>({ scope, selected: [], results: {}, running: false });
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const visible = view.scope === scope ? view : { scope, selected: [], results: {}, running: false };
  const pending = mutation.view.pending?.kind === 'candidate-stage' ? mutation.view.pending : undefined;
  const blocked =
    mutation.view.mutating || Boolean(mutation.view.pending) || mutation.view.error === 'storage' || active.current;
  const update = (token: { scope: string }, patch: (previous: StageView) => StageView) => {
    if (!mounted.current || owner.current !== token || getRendererAccountScopeToken() !== account) return;
    setView((previous) => {
      if (!mounted.current || owner.current !== token || getRendererAccountScopeToken() !== account) return previous;
      return patch(previous.scope === scope ? previous : { scope, selected: [], results: {}, running: false });
    });
  };
  const toggle = (id: string) => {
    if (blocked) return;
    const token = owner.current;
    update(token, (previous) => ({
      ...previous,
      selected: previous.selected.includes(id)
        ? previous.selected.filter((value) => value !== id)
        : [...previous.selected, id],
    }));
  };
  const makeCommand = (record: RelatedRecord): LiteratureCandidateCommand => {
    const metadata: Record<string, unknown> = {
      identifiers: record.identifiers,
      authors: record.authors,
      year: record.year,
      abstract: record.abstract,
      container_title: record.container_title,
      issued_text: record.issued_text,
      item_type: record.item_type,
      url: record.url,
      volume: record.volume,
      pages: record.pages,
    };
    for (const identifier of record.identifiers) metadata[identifier.scheme] = identifier.value;
    return decodeLiteratureCandidateCommand({
      kind: 'candidate-stage',
      operation_id: crypto.randomUUID(),
      provider: 'semantic-scholar',
      external_id: record.paper_id,
      title: record.title,
      metadata,
      source: { item_id: item.id, metadata_revision: item.metadata_revision, direction },
    });
  };
  const submitSelected = async () => {
    if (blocked || active.current || !visible.selected.length) return;
    const token = owner.current;
    const queue = records.filter((record) => visible.selected.includes(record.paper_id));
    active.current = true;
    update(token, (previous) => ({ ...previous, running: true }));
    try {
      for (const record of queue) {
        if (!mounted.current || owner.current !== token || getRendererAccountScopeToken() !== account) break;
        let command: LiteratureCandidateCommand;
        try {
          command = makeCommand(record);
        } catch {
          update(token, (previous) => ({
            ...previous,
            results: { ...previous.results, [record.paper_id]: 'invalid' },
          }));
          continue;
        }
        try {
          const receipt = await mutation.controller.submit(command);
          update(token, (previous) => ({
            ...previous,
            selected: previous.selected.filter((id) => id !== record.paper_id),
            results: { ...previous.results, [record.paper_id]: receipt.candidate.state },
          }));
        } catch {
          update(token, (previous) => ({ ...previous, results: { ...previous.results, [record.paper_id]: 'failed' } }));
          break;
        }
      }
    } finally {
      active.current = false;
      update(token, (previous) => ({ ...previous, running: false }));
    }
  };
  const retryPending = async () => {
    if (!pending || mutation.view.mutating || active.current) return;
    const token = owner.current;
    active.current = true;
    update(token, (previous) => ({ ...previous, running: true }));
    try {
      const receipt = await mutation.controller.retry();
      if (
        receipt.schema === 'x-science.literature-candidate-receipt.v1' &&
        receipt.action === 'stage' &&
        pending.source?.item_id === item.id &&
        pending.source.metadata_revision === item.metadata_revision &&
        pending.source.direction === direction
      )
        update(token, (previous) => ({
          ...previous,
          selected: previous.selected.filter((id) => id !== pending.external_id),
          results: { ...previous.results, [pending.external_id]: receipt.candidate.state },
        }));
    } catch {
      /* The owner retains the exact uncertain command and projects its error. */
    } finally {
      active.current = false;
      update(token, (previous) => ({ ...previous, running: false }));
    }
  };
  return {
    ...visible,
    pending,
    blocked: blocked || visible.running,
    canRetryPending:
      Boolean(pending) && !mutation.view.mutating && !active.current && mutation.view.error === 'uncertain',
    toggle,
    submitSelected,
    retryPending,
  };
}
