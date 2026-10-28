import React, { useSyncExternalStore } from 'react';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { LibraryInbox } from '@/renderer/pages/library/LibraryInbox';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
  getRendererAccountScopeToken,
} from '@/renderer/services/rendererAccountScope';
import { runLiteratureCandidateCommand } from '@/renderer/services/runtime/literatureCandidates';
import {
  decodeLiteratureCandidateCommand,
  type LiteratureCandidateCommand,
  type LiteratureCandidateReceipt,
} from '@/common/adapter/literatureCandidates';
import { renderWithI18n } from '../../i18nTestUtils';
import { deferredPdf } from '../conversation/pdfReadingFixtures';
import { installLiteratureLocks, literaturePendingStorage } from '../conversation/literatureMutationLockFixtures';
const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.http,
}));
const operation = '00000000-0000-4000-8000-000000000084';
const command: LiteratureCandidateCommand = { kind: 'candidate-accept', operation_id: operation, candidate_id: 'one' };
const candidate = {
  id: 'one',
  title: 'Synthetic paper',
  metadata: { doi: '10.1000/example' },
  provider: 'semantic-scholar',
  external_id: 'paper',
  state: 'pending' as const,
};
const accepted: LiteratureCandidateReceipt = {
  schema: 'x-science.literature-candidate-receipt.v1',
  operation_id: operation,
  action: 'accept',
  candidate: { ...candidate, state: 'accepted', item_id: 'library-item' },
};
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('owner');
  mocks.http.mockReset();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it('shows candidate bibliography and abstract without exposing a raw provider record as the summary', async () => {
  mocks.http.mockResolvedValue({
    schema: 'x-science.literature-candidates.v1',
    candidates: [
      {
        ...candidate,
        metadata: {
          authors: ['Ada Example', 'Bo Example'],
          year: 2024,
          container_title: 'Synthetic Journal',
          abstract: '<script>untrusted()</script> A bibliographic fixture.',
        },
      },
    ],
  });
  const submit = vi.fn().mockResolvedValue(accepted);
  await renderWithI18n(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={submit}
      close={vi.fn()}
      openItem={vi.fn()}
    />,
    'en-US'
  );
  expect(await screen.findByRole('heading', { name: candidate.title })).toBeInTheDocument();
  expect(screen.getByText('Ada Example; Bo Example')).toBeInTheDocument();
  expect(screen.getByText('2024 · Synthetic Journal')).toBeInTheDocument();
  expect(screen.getByText('Found via Semantic Scholar')).toBeInTheDocument();
  expect(screen.getByText('<script>untrusted()</script> A bibliographic fixture.')).toBeInTheDocument();
  expect(document.querySelector('script')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Add to Library' }));
  await waitFor(() =>
    expect(submit).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'candidate-accept', candidate_id: candidate.id })
    )
  );
});
it('sends one strict candidate action to its authenticated endpoint', async () => {
  mocks.http.mockResolvedValueOnce(accepted);
  await expect(runLiteratureCandidateCommand(command)).resolves.toEqual(accepted);
  expect(mocks.http).toHaveBeenCalledWith(
    'POST',
    '/api/literature/candidates/one/accept',
    { operation_id: operation },
    expect.objectContaining({ timeoutMs: 30000 })
  );
});
it('routes restore explicitly and requires a matching pending acknowledgement', async () => {
  const restore: LiteratureCandidateCommand = { ...command, kind: 'candidate-restore' };
  const restored: LiteratureCandidateReceipt = {
    schema: accepted.schema,
    operation_id: operation,
    action: 'restore',
    candidate,
  };
  mocks.http.mockResolvedValueOnce(restored);
  await expect(runLiteratureCandidateCommand(restore)).resolves.toEqual(restored);
  expect(mocks.http).toHaveBeenCalledWith(
    'POST',
    '/api/literature/candidates/one/restore',
    { operation_id: operation },
    expect.objectContaining({ timeoutMs: 30000 })
  );
  for (const invalidCandidate of [{ ...candidate, state: 'dismissed' }, accepted.candidate]) {
    mocks.http.mockResolvedValueOnce({ ...restored, candidate: invalidCandidate });
    await expect(runLiteratureCandidateCommand(restore)).rejects.toThrow('Invalid Literature candidate contract');
  }
  expect(() => decodeLiteratureCandidateCommand({ ...restore, unexpected: true })).toThrow();
});

it('restores a dismissed row through the original owner after a lost acknowledgement and refresh', async () => {
  let state: 'pending' | 'dismissed' = 'dismissed';
  let loseFirstAcknowledgement = true;
  const writes: Array<{ path: string; operation_id: string }> = [];
  mocks.http.mockImplementation(async (method: string, path: string, body?: { operation_id: string }) => {
    if (method === 'GET')
      return {
        schema: 'x-science.literature-candidates.v1',
        candidates: path.includes(`state=${state}`) ? [{ ...candidate, state }] : [],
      };
    if (method !== 'POST' || path !== '/api/literature/candidates/one/restore' || !body)
      throw new Error('Unexpected synthetic candidate request');
    writes.push({ path, operation_id: body.operation_id });
    state = 'pending';
    if (loseFirstAcknowledgement) {
      loseFirstAcknowledgement = false;
      throw new TypeError('Lost restore acknowledgement');
    }
    return { schema: accepted.schema, operation_id: body.operation_id, action: 'restore', candidate };
  });
  const storage = literaturePendingStorage();
  function OwnerView({ controller }: { controller: LiteratureMutationController }) {
    const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
    return (
      <>
        <LibraryInbox
          account={getRendererAccountScopeToken()}
          busy={view.mutating || Boolean(view.pending)}
          receipt={view.receipt?.schema === accepted.schema ? view.receipt : undefined}
          pendingOperationId={view.pending?.operation_id}
          submit={(input) => controller.submit(input)}
          close={vi.fn()}
          openItem={vi.fn()}
        />
        <output aria-label='Original operation status'>{view.error ?? 'ready'}</output>
        <button
          disabled={view.mutating || !view.pending}
          onClick={() => void controller.retry().catch((): undefined => undefined)}
        >
          Retry original operation
        </button>
      </>
    );
  }
  const original = new LiteratureMutationController('owner', { storage });
  const view = await renderWithI18n(<OwnerView controller={original} />, 'en-US');
  fireEvent.click(screen.getByRole('button', { name: 'Dismissed' }));
  fireEvent.click(await screen.findByRole('button', { name: /^Restore$/ }));
  await waitFor(() => expect(screen.getByLabelText('Original operation status')).toHaveTextContent('uncertain'));
  expect(original.getSnapshot().pending).toMatchObject({ kind: 'candidate-restore', candidate_id: 'one' });
  const operationId = writes[0].operation_id;
  view.unmount();
  const resumed = new LiteratureMutationController('owner', { storage });
  await renderWithI18n(<OwnerView controller={resumed} />, 'en-US');
  await screen.findByRole('heading', { name: candidate.title });
  fireEvent.click(screen.getByRole('button', { name: 'Retry original operation' }));
  await waitFor(() => expect(resumed.getSnapshot().pending).toBeNull());
  expect(writes).toEqual([
    { path: '/api/literature/candidates/one/restore', operation_id: operationId },
    { path: '/api/literature/candidates/one/restore', operation_id: operationId },
  ]);
  expect(storage.values.size).toBe(0);
  expect(screen.getByRole('button', { name: 'Add to Library' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Dismiss' })).toBeEnabled();
  expect(screen.queryByRole('button', { name: /^Restore$/ })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Open in Library' })).toBeNull();
});
it('accepts a legitimate stage dedupe receipt from another source', async () => {
  const stage: LiteratureCandidateCommand = {
    kind: 'candidate-stage',
    operation_id: operation,
    provider: 'other-provider',
    external_id: 'other-id',
    title: 'Synthetic paper',
    metadata: { doi: '10.1000/example' },
  };
  mocks.http.mockResolvedValueOnce({
    schema: 'x-science.literature-candidate-receipt.v1',
    operation_id: operation,
    action: 'stage',
    candidate,
  });
  const receipt = await runLiteratureCandidateCommand(stage);
  expect(receipt.candidate.provider).toBe('semantic-scholar');
});
it('recovers the same pending command after a lost acknowledgement', async () => {
  const storage = literaturePendingStorage();
  const runCandidate = vi
    .fn()
    .mockRejectedValueOnce(new TypeError('lost acknowledgement'))
    .mockResolvedValueOnce(accepted);
  const original = new LiteratureMutationController('owner', { storage, runCandidate });
  await expect(original.submit(command)).rejects.toThrow('lost acknowledgement');
  expect(original.getSnapshot().pending).toMatchObject(command);
  const resumed = new LiteratureMutationController('owner', { storage, runCandidate });
  await expect(resumed.retry()).resolves.toEqual(accepted);
  expect(runCandidate.mock.calls.map((args) => args[0])).toEqual([command, command]);
  expect(storage.values.size).toBe(0);
});
it('Inbox reads pending/dismissed, settles through the supplied owner and discards late reads', async () => {
  const pending = deferredPdf<unknown>();
  mocks.http.mockReturnValueOnce(pending.promise).mockResolvedValueOnce({
    schema: 'x-science.literature-candidates.v1',
    candidates: [{ ...candidate, state: 'dismissed' }],
  });
  const submit = vi.fn().mockResolvedValue(accepted);
  await renderWithI18n(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={submit}
      close={vi.fn()}
      openItem={vi.fn()}
    />,
    'en-US'
  );
  fireEvent.click(screen.getByRole('button', { name: 'Dismissed' }));
  await screen.findByText('Synthetic paper');
  expect(screen.queryByRole('button', { name: 'Dismiss' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: /^Restore$/ }));
  expect(submit).toHaveBeenCalledWith(expect.objectContaining({ kind: 'candidate-restore', candidate_id: 'one' }));
  pending.resolve({ schema: 'x-science.literature-candidates.v1', candidates: [candidate] });
  await pending.promise;
  expect(screen.queryByRole('button', { name: 'Dismiss' })).toBeNull();
});

it('does not append an old continuation page after changing Inbox state', async () => {
  const late = deferredPdf<unknown>();
  mocks.http
    .mockResolvedValueOnce({
      schema: 'x-science.literature-candidates.v1',
      candidates: [candidate],
      next_cursor: 'one',
    })
    .mockReturnValueOnce(late.promise)
    .mockResolvedValueOnce({
      schema: 'x-science.literature-candidates.v1',
      candidates: [{ ...candidate, id: 'dismissed', title: 'Dismissed result', state: 'dismissed' }],
    });
  await renderWithI18n(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={vi.fn()}
      close={vi.fn()}
      openItem={vi.fn()}
    />,
    'en-US'
  );
  await screen.findByText('Synthetic paper');
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
  fireEvent.click(screen.getByRole('button', { name: 'Dismissed' }));
  await screen.findByText('Dismissed result');
  late.resolve({
    schema: 'x-science.literature-candidates.v1',
    candidates: [{ ...candidate, id: 'two', title: 'Stale continuation' }],
  });
  await late.promise;
  expect(screen.queryByText('Stale continuation')).toBeNull();
});

it('does not start competing row actions before React publishes busy state', async () => {
  const delayed = deferredPdf<LiteratureCandidateReceipt>();
  const submit = vi.fn().mockReturnValue(delayed.promise);
  mocks.http.mockResolvedValueOnce({ schema: 'x-science.literature-candidates.v1', candidates: [candidate] });
  await renderWithI18n(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={submit}
      close={vi.fn()}
      openItem={vi.fn()}
    />,
    'en-US'
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Add to Library' }));
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(submit).toHaveBeenCalledTimes(1);
  delayed.resolve(accepted);
  await delayed.promise;
});

it('rejects candidate acknowledgements with a different operation identity', async () => {
  mocks.http.mockResolvedValueOnce({ ...accepted, operation_id: '00000000-0000-4000-8000-000000000085' });
  await expect(runLiteratureCandidateCommand(command)).rejects.toThrow('Invalid Literature candidate contract');
});
it('hides a late Inbox response after an account switch', async () => {
  const old = deferredPdf<unknown>();
  mocks.http.mockReturnValueOnce(old.promise).mockResolvedValueOnce({
    schema: 'x-science.literature-candidates.v1',
    candidates: [{ ...candidate, id: 'new', title: 'New owner paper' }],
  });
  const render = await renderWithI18n(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={vi.fn()}
      close={vi.fn()}
      openItem={vi.fn()}
    />,
    'en-US'
  );
  setRendererAccountOwner('another-owner');
  render.rerender(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={vi.fn()}
      close={vi.fn()}
      openItem={vi.fn()}
    />
  );
  await screen.findByText('New owner paper');
  old.resolve({
    schema: 'x-science.literature-candidates.v1',
    candidates: [{ ...candidate, title: 'Old owner paper' }],
  });
  await old.promise;
  expect(screen.queryByText('Old owner paper')).toBeNull();
});

it('uses the server UTF-8 and escaped JSON byte limits for candidate admission', () => {
  const base = {
    kind: 'candidate-stage',
    operation_id: operation,
    provider: '字'.repeat(21),
    external_id: 'x'.repeat(512),
    title: '字'.repeat(1365),
    metadata: { x: '字'.repeat(5458) },
  };
  expect(() => decodeLiteratureCandidateCommand(base)).not.toThrow();
  for (const bad of [
    { ...base, provider: '字'.repeat(22) },
    { ...base, external_id: 'x'.repeat(513) },
    { ...base, title: '字'.repeat(1366) },
    { ...base, metadata: { x: '字'.repeat(5459) } },
    { ...base, metadata: { x: '<'.repeat(2730) } },
    { ...base, metadata: { x: '\u2028'.repeat(2730) } },
  ])
    expect(() => decodeLiteratureCandidateCommand(bad)).toThrow();
});

it('renders the Inbox and actions in Chinese', async () => {
  mocks.http
    .mockResolvedValueOnce({ schema: 'x-science.literature-candidates.v1', candidates: [candidate] })
    .mockResolvedValueOnce({
      schema: 'x-science.literature-candidates.v1',
      candidates: [{ ...candidate, state: 'dismissed' }],
    });
  await renderWithI18n(
    <LibraryInbox
      account={getRendererAccountScopeToken()}
      busy={false}
      submit={vi.fn()}
      close={vi.fn()}
      openItem={vi.fn()}
    />,
    'zh-CN'
  );
  await screen.findByText('Synthetic paper');
  expect(screen.getByRole('button', { name: '加入文献库' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '忽略' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '返回文献库' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '已忽略' }));
  expect(await screen.findByRole('button', { name: /^恢复$/ })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '加入文献库' })).toBeNull();
});

it('does not show a prior accepted receipt on opening but shows the current action', async () => {
  mocks.http.mockResolvedValue({ schema: 'x-science.literature-candidates.v1', candidates: [candidate] });
  const submit = vi.fn().mockResolvedValue(accepted),
    close = vi.fn(),
    openItem = vi.fn();
  const account = getRendererAccountScopeToken();
  const view = await renderWithI18n(
    <LibraryInbox
      account={account}
      busy={false}
      receipt={accepted}
      submit={submit}
      close={close}
      openItem={openItem}
    />,
    'en-US'
  );
  expect(screen.queryByRole('button', { name: 'Open in Library' })).toBeNull();
  fireEvent.click(await screen.findByRole('button', { name: 'Add to Library' }));
  const currentOperation = (submit.mock.calls[0][0] as LiteratureCandidateCommand).operation_id;
  view.rerender(
    <LibraryInbox
      account={account}
      busy={false}
      receipt={{ ...accepted, operation_id: currentOperation }}
      submit={submit}
      close={close}
      openItem={openItem}
    />
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Open in Library' }));
  expect(openItem).toHaveBeenCalledWith('library-item');
});
it('shows the acknowledgement of an explicitly recovered pending candidate action', async () => {
  mocks.http.mockResolvedValue({ schema: 'x-science.literature-candidates.v1', candidates: [candidate] });
  const account = getRendererAccountScopeToken(),
    submit = vi.fn(),
    close = vi.fn(),
    openItem = vi.fn();
  const old = { ...accepted, operation_id: '00000000-0000-4000-8000-000000000085' };
  const view = await renderWithI18n(
    <LibraryInbox
      account={account}
      busy={false}
      receipt={old}
      pendingOperationId={operation}
      submit={submit}
      close={close}
      openItem={openItem}
    />,
    'en-US'
  );
  expect(screen.queryByRole('button', { name: 'Open in Library' })).toBeNull();
  view.rerender(
    <LibraryInbox account={account} busy={false} receipt={accepted} submit={submit} close={close} openItem={openItem} />
  );
  expect(await screen.findByRole('button', { name: 'Open in Library' })).toBeInTheDocument();
});
