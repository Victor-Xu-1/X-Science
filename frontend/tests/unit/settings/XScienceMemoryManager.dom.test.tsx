import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Message } from '@arco-design/web-react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { XScienceMemoryManager } from '@/renderer/pages/settings/components/XScienceMemoryManager';
import { renderWithI18n } from '../i18nTestUtils';
import { createFetchMock, memoryContext, projectMemoryContext, projectCatalog } from './memoryManagerFixtures';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

async function chooseMemoryScope(value: string) {
  const select = await screen.findByTestId('memory-scope-select');
  fireEvent.click(select.closest('.arco-select') ?? select);

  const labels: Record<string, string> = {
    'category:memcat_1': '研究偏好',
    'entity:project:proj_a': 'CRBN 项目',
    'session:frame_1': 'CRBN 结构分析',
  };

  const option = await waitFor(() => {
    const candidate = Array.from(document.querySelectorAll<HTMLElement>('.arco-select-option, [role="option"]')).find(
      (node) =>
        node.getAttribute('data-value') === value ||
        node.getAttribute('data-key') === value ||
        node.textContent?.includes(labels[value])
    );
    if (!candidate) {
      const options = Array.from(document.querySelectorAll<HTMLElement>('.arco-select-option, [role="option"]'))
        .map((node) => node.textContent?.trim())
        .join(', ');
      throw new Error(`Memory scope option not found: ${value}; options: ${options}`);
    }
    return candidate;
  });

  fireEvent.click(option);
}

async function waitForProjectLayer() {
  await waitFor(() => expect(screen.getByRole('heading', { level: 4 })).toHaveTextContent('CRBN 项目'));
}

describe('XScienceMemoryManager', () => {
  beforeEach(() => {
    setRendererAccountOwner('memory-component-fixture');
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.spyOn(Message, 'success').mockReturnValue({ close: () => undefined });
    vi.spyOn(Message, 'warning').mockReturnValue({ close: () => undefined });
    vi.spyOn(Message, 'error').mockReturnValue({ close: () => undefined });
  });

  afterEach(() => {
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('renders native profile, category, project and session scopes from the real context model', async () => {
    const { fetchMock } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager onChanged={() => undefined} />, 'zh-CN');

    await waitFor(() => expect(screen.getByTestId('memory-manager')).toBeInTheDocument());
    await screen.findByTestId('memory-enabled-toggle');
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('memory-scope-select')).toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: '记忆范围' })).not.toBeInTheDocument();
    expect(screen.getByTestId('memory-layer-project')).toHaveTextContent('项目记忆');
    expect(screen.getByTestId('memory-row-mem_profile')).toHaveTextContent('关注肿瘤免疫研究');

    await chooseMemoryScope('category:memcat_1');
    expect(await screen.findByTestId('memory-category-delete-memcat_1')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('memory-layer-project'));
    await waitForProjectLayer();
    await chooseMemoryScope('entity:project:proj_a');
    await chooseMemoryScope('session:frame_1');
    await waitFor(() =>
      expect(screen.getByTestId('memory-row-mem_session')).toHaveTextContent('会话使用 CRBN 结构数据')
    );
  });

  it('shows the project selector only when it offers a real project choice', async () => {
    const { fetchMock: baseFetch } = createFetchMock();
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      if (url === '/api/projects') {
        return Promise.resolve(
          Response.json({
            projects: [
              ...projectCatalog.projects,
              {
                ...projectCatalog.projects[0],
                project_id: 'proj_b',
                name: 'EGFR 项目',
              },
            ],
          })
        );
      }
      return baseFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    fireEvent.click(await screen.findByTestId('memory-layer-project'));

    await waitFor(() => expect(screen.getByTestId('memory-project-select')).toHaveTextContent('CRBN 项目'));
    expect(screen.getByTestId('memory-project-picker')).toBeInTheDocument();
  });

  it('keeps the selected project visible when a refresh removes its rows', async () => {
    let projectLoads = 0;
    let projectEnabled = false;
    const observed: Array<{ url: string; method: string; body?: unknown }> = [];
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      const method = init?.method ?? 'GET';
      observed.push({
        url,
        method,
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      });
      if (url === '/api/projects' && method === 'GET') return Response.json(projectCatalog);
      if (url === '/api/memory/context?project_id=proj_a' && method === 'GET') {
        projectLoads += 1;
        return Response.json({
          ...projectMemoryContext,
          memory_enabled: projectEnabled,
          entities: projectLoads === 1 ? projectMemoryContext.entities : [],
          sessions: projectLoads === 1 ? projectMemoryContext.sessions : [],
        });
      }
      if (url === '/api/memory/context' && method === 'GET') {
        return Response.json(memoryContext);
      }
      if (url === '/api/memory/enabled' && method === 'GET') return Response.json({ enabled: false });
      if (url === '/api/memory/enabled' && method === 'PUT') return new Response(null, { status: 204 });
      if (url === '/api/projects/proj_a/memory/enabled' && method === 'PUT') {
        projectEnabled = (typeof init?.body === 'string' ? JSON.parse(init.body) : {}).enabled === true;
        return Response.json({ enabled: projectEnabled });
      }
      return new Response(null, { status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    fireEvent.click(await screen.findByTestId('memory-layer-project'));
    await waitForProjectLayer();
    await chooseMemoryScope('entity:project:proj_a');
    expect(screen.getByRole('heading', { level: 4 })).toHaveTextContent('CRBN');

    fireEvent.click(screen.getByTestId('memory-disabled-enable'));
    await waitFor(() =>
      expect(observed).toContainEqual({
        url: '/api/projects/proj_a/memory/enabled',
        method: 'PUT',
        body: { enabled: true },
      })
    );
    expect(observed).not.toContainEqual({ url: '/api/memory/enabled', method: 'PUT', body: { enabled: true } });

    await waitForProjectLayer();
    expect(screen.getByRole('heading', { level: 4 })).toHaveTextContent('CRBN 项目');
    expect(screen.getByText('当前项目还没有记忆。添加第一条项目规则、背景或工作要求。')).toBeInTheDocument();
  });

  it('shows one recoverable inline error instead of a misleading disabled-memory state', async () => {
    const { fetchMock: healthyFetch } = createFetchMock();
    let unavailable = true;
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) =>
      unavailable ? Promise.resolve(Response.json({ detail: 'not found' }, { status: 404 })) : healthyFetch(input, init)
    );
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');

    expect(await screen.findByRole('alert')).toHaveTextContent('记忆数据暂时不可用。');
    expect(screen.queryByTestId('memory-enabled-toggle')).not.toBeInTheDocument();
    expect(Message.error).not.toHaveBeenCalled();

    unavailable = false;
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(screen.getByTestId('memory-enabled-toggle')).toBeInTheDocument());
  });

  it('rejects an unknown global-memory setting and recovers without displaying it as global-off', async () => {
    const { fetchMock: healthyFetch } = createFetchMock();
    let malformed = true;
    vi.stubGlobal('fetch', (input: string | URL | Request, init?: RequestInit) =>
      malformed && input === '/api/memory/enabled' && !init?.method
        ? Promise.resolve(Response.json({ enabled: 'false' }))
        : healthyFetch(input, init)
    );
    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    expect(await screen.findByRole('alert')).toHaveTextContent('记忆数据暂时不可用。');
    expect(screen.queryByTestId('memory-enabled-toggle')).not.toBeInTheDocument();
    malformed = false;
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'false'));
  });

  it('toggles memory and creates a note through native X-Science write contracts', async () => {
    const { fetchMock, observed } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();

    await renderWithI18n(<XScienceMemoryManager onChanged={onChanged} />, 'zh-CN');
    await waitFor(() => expect(screen.getByTestId('memory-row-mem_profile')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('memory-disabled-enable'));
    await waitFor(() =>
      expect(observed).toContainEqual({ url: '/api/memory/enabled', method: 'PUT', body: { enabled: true } })
    );

    fireEvent.click(screen.getByTestId('memory-add'));
    fireEvent.change(screen.getByTestId('memory-new-input'), { target: { value: '新的研究偏好' } });
    fireEvent.click(screen.getByTestId('memory-new-save'));

    await waitFor(() =>
      expect(observed).toContainEqual({
        url: '/api/memories',
        method: 'POST',
        body: { text: '新的研究偏好', entity: 'profile' },
      })
    );
    expect(onChanged).toHaveBeenCalled();
  });

  it('writes new memory to the selected project entity', async () => {
    const { fetchMock, observed } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    fireEvent.click(await screen.findByTestId('memory-layer-project'));
    await waitForProjectLayer();
    await chooseMemoryScope('entity:project:proj_a');
    fireEvent.click(screen.getByTestId('memory-add'));
    fireEvent.change(screen.getByTestId('memory-new-input'), {
      target: { value: '项目必须保留可复现实验记录' },
    });
    fireEvent.click(screen.getByTestId('memory-new-save'));

    await waitFor(() =>
      expect(observed).toContainEqual({
        url: '/api/memories',
        method: 'POST',
        body: { text: '项目必须保留可复现实验记录', entity: 'project:proj_a' },
      })
    );
  });

  it('edits the selected project constraint inline', async () => {
    const { fetchMock, observed } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    fireEvent.click(await screen.findByTestId('memory-layer-project'));
    await waitForProjectLayer();
    await chooseMemoryScope('entity:project:proj_a');
    fireEvent.click(await screen.findByTestId('memory-row-edit-mem_project'));

    const editor = document.querySelector<HTMLTextAreaElement>('textarea');
    expect(editor).not.toBeNull();
    fireEvent.change(editor!, { target: { value: '项目规则已更新，保留完整可复现实验记录' } });
    fireEvent.click(screen.getByRole('button', { name: '保存', exact: true }));

    await waitFor(() =>
      expect(observed).toContainEqual({
        url: '/api/memories/mem_project',
        method: 'PUT',
        body: { text: '项目规则已更新，保留完整可复现实验记录' },
      })
    );
  });

  it('updates category auto-recall with the native category contract', async () => {
    const { fetchMock, observed } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    await chooseMemoryScope('category:memcat_1');
    await waitFor(() => expect(screen.queryByTestId('memory-row-mem_project')).toBeNull());
    fireEvent.click(await screen.findByTestId('memory-category-auto-recall-memcat_1'));

    await waitFor(() =>
      expect(observed).toContainEqual({
        url: '/api/memory/categories/memcat_1',
        method: 'PUT',
        body: { name: '研究偏好', guidance: '用于研究规划', auto_recall: false },
      })
    );
  });

  it('names the selected category in the delete confirmation', async () => {
    const { fetchMock } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    await chooseMemoryScope('category:memcat_1');
    fireEvent.click(await screen.findByTestId('memory-category-delete-memcat_1'));

    expect(await screen.findByText('删除“研究偏好”后，可选择保留其中记忆或同时删除。')).toBeInTheDocument();
    expect(screen.queryByText(/\{\{name\}\}/)).not.toBeInTheDocument();
  });

  it('requires category guidance and defaults new categories to automatic recall', async () => {
    const { fetchMock, observed } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    await screen.findByTestId('memory-category-add');
    fireEvent.click(screen.getByTestId('memory-category-add'));

    expect(screen.getByTestId('memory-category-draft-auto-recall')).toHaveAttribute('aria-checked', 'true');
    fireEvent.change(screen.getByTestId('memory-category-name'), { target: { value: 'Methods' } });
    const saveCategory = document.querySelector<HTMLButtonElement>('#memory-category-save');
    expect(saveCategory).not.toBeNull();
    expect(saveCategory).toBeDisabled();

    fireEvent.change(screen.getByTestId('memory-category-guidance'), {
      target: { value: 'Recall only for experimental methods and protocol decisions.' },
    });
    expect(saveCategory).toBeEnabled();
    fireEvent.click(saveCategory!);

    await waitFor(() =>
      expect(observed).toContainEqual({
        url: '/api/memory/categories',
        method: 'POST',
        body: {
          name: 'Methods',
          guidance: 'Recall only for experimental methods and protocol decisions.',
          auto_recall: true,
        },
      })
    );
  });

  it('disables category creation at the workspace limit of ten', async () => {
    const fullContext = {
      ...memoryContext,
      categories: Array.from({ length: 10 }, (_, index) => ({
        id: `memcat_${index + 1}`,
        name: `Category ${index + 1}`,
        guidance: `Guidance ${index + 1}`,
        auto_recall: true,
        row_count: 0,
      })),
    };
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      if (url === '/api/memory/context') return Response.json(fullContext);
      if (url === '/api/memory/enabled') return Response.json({ enabled: true });
      return new Response(null, { status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');

    expect(await screen.findByTestId('memory-category-count')).toHaveTextContent('10/10');
    expect(screen.getByTestId('memory-category-add')).toBeDisabled();
  });

  it('restores the memory toggle and reports an error when the backend write fails', async () => {
    const { fetchMock } = createFetchMock({ failEnable: true });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    await waitFor(() => expect(screen.getByTestId('memory-disabled-enable')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('memory-disabled-enable'));

    await waitFor(() => expect(Message.error).toHaveBeenCalled());
    expect(screen.getByTestId('memory-enabled-toggle')).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('memory-disabled-enable')).toBeInTheDocument();
  });

  it('shows a retryable session error and replaces it with rows after recovery', async () => {
    const { fetchMock: healthyFetch } = createFetchMock();
    let sessionAttempts = 0;
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      if (url === '/api/memory/sessions/frame_1' && sessionAttempts++ === 0) {
        return Promise.resolve(Response.json({ detail: 'temporarily unavailable' }, { status: 503 }));
      }
      return healthyFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    fireEvent.click(await screen.findByTestId('memory-layer-project'));
    await waitForProjectLayer();
    await chooseMemoryScope('entity:project:proj_a');
    await chooseMemoryScope('session:frame_1');

    const error = await screen.findByTestId('memory-session-error');
    expect(error).toHaveTextContent('无法加载会话记忆。');
    fireEvent.click(within(error).getByRole('button', { name: '重试' }));
    expect(await screen.findByTestId('memory-row-mem_session')).toHaveTextContent('会话使用 CRBN 结构数据');
  });

  it('does not report a successful write as failed when post-write refreshes fail', async () => {
    const { fetchMock: healthyFetch } = createFetchMock();
    let contextLoads = 0;
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      if (url === '/api/memory/context' && contextLoads++ > 0) {
        return Promise.resolve(Response.json({ detail: 'refresh unavailable' }, { status: 503 }));
      }
      return healthyFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn().mockRejectedValue(new Error('consumer refresh failed'));

    await renderWithI18n(<XScienceMemoryManager onChanged={onChanged} />, 'zh-CN');
    await screen.findByTestId('memory-row-mem_profile');
    fireEvent.click(screen.getByTestId('memory-add'));
    fireEvent.change(screen.getByTestId('memory-new-input'), { target: { value: '新的研究偏好' } });
    fireEvent.click(screen.getByTestId('memory-new-save'));

    await waitFor(() => expect(Message.success).toHaveBeenCalledWith('记忆已保存。'));
    expect(Message.error).not.toHaveBeenCalled();
    expect(Message.warning).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('memory-new-input')).not.toBeInTheDocument();
  });

  describe('Memory category pending-close admission', () => {
    const draftName = 'Methods';
    const draftGuidance = 'Recall only for experimental methods and protocol decisions.';

    function deferredAck() {
      let release!: () => void;
      const promise = new Promise<void>((resolve) => {
        release = resolve;
      });
      return { promise, release };
    }

    function fillCategory() {
      fireEvent.click(screen.getByTestId('memory-category-add'));
      fireEvent.change(screen.getByTestId('memory-category-name'), { target: { value: draftName } });
      fireEvent.change(screen.getByTestId('memory-category-guidance'), { target: { value: draftGuidance } });
    }

    function expectCategoryDraft() {
      expect(screen.getByTestId('memory-category-name')).toBeVisible();
      expect(screen.getByTestId('memory-category-name')).toHaveValue(draftName);
      expect(screen.getByTestId('memory-category-guidance')).toHaveValue(draftGuidance);
    }

    function expectCategoryInputsEnabled() {
      expect(screen.getByTestId('memory-category-name')).toBeEnabled();
      expect(screen.getByTestId('memory-category-guidance')).toBeEnabled();
    }

    function expectCategoryInputsDisabled() {
      expect(screen.getByTestId('memory-category-name')).toBeDisabled();
      expect(screen.getByTestId('memory-category-guidance')).toBeDisabled();
    }

    function categoryDialog() {
      const dialog = screen.getByTestId('memory-category-name').closest<HTMLElement>('.arco-modal');
      expect(dialog).not.toBeNull();
      return dialog!;
    }

    function clickNativeClose() {
      const close = categoryDialog().querySelector<HTMLButtonElement>('button.workbench-modal-close');
      expect(close).not.toBeNull();
      fireEvent.click(close!); // Real WorkbenchCloseButton -> Arco -> original onCancel.
    }

    function clickNativeCancel() {
      const cancel = categoryDialog().querySelector<HTMLButtonElement>(
        '.arco-modal-footer button:not(#memory-category-save)'
      );
      expect(cancel).not.toBeNull();
      fireEvent.click(cancel!); // Actual footer button; no locale guess or callback stub.
    }

    function pressNativeEscape() {
      const close = categoryDialog().querySelector<HTMLButtonElement>('button.workbench-modal-close');
      expect(close).not.toBeNull();
      close!.focus();
      expect(close).toHaveFocus();
      fireEvent.keyDown(close!, { key: 'Escape', code: 'Escape', keyCode: 27, which: 27 });
    }

    async function expectCategoryClosed() {
      await waitFor(() => expect(screen.queryByTestId('memory-category-name')).not.toBeInTheDocument());
      await screen.findByTestId('memory-category-add');
    }

    it('guards the same category producer during pending close, retry and ACK', async () => {
      const { fetchMock, observed } = createFetchMock();
      const failedAttempt = deferredAck();
      const successfulAttempt = deferredAck();
      let postAttempt = 0;
      let acknowledged = false;
      const writes = () => observed.filter(({ url, method }) => url === '/api/memory/categories' && method === 'POST');
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
          const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
          const method = init?.method ?? 'GET';
          const response = await fetchMock(input, init); // Reuse original fixture/recorder.
          if (url === '/api/memory/categories' && method === 'POST') {
            postAttempt += 1;
            if (postAttempt === 1) {
              await failedAttempt.promise;
              return Response.json({ error: 'category write unavailable' }, { status: 503 });
            }
            if (postAttempt !== 2) throw new Error('Unexpected category write without an explicit retry');
            await successfulAttempt.promise;
            acknowledged = true;
          }
          if (url === '/api/memory/context' && method === 'GET' && acknowledged) {
            return Response.json({
              ...memoryContext,
              categories: [
                ...memoryContext.categories,
                {
                  id: 'memcat_created',
                  name: draftName,
                  guidance: draftGuidance,
                  auto_recall: true,
                  row_count: 0,
                },
              ],
            });
          }
          return response;
        })
      );

      const attemptClosingWhilePending = () => {
        expectCategoryInputsDisabled();
        clickNativeClose();
        // Primary current-source RED: onCancel has already set categoryDraft=null.
        expectCategoryDraft();
        clickNativeCancel();
        expectCategoryDraft();
        pressNativeEscape();
        expectCategoryDraft();
      };

      try {
        const view = await renderWithI18n(<XScienceMemoryManager />, 'en-US');
        await screen.findByTestId('memory-category-add');
        fillCategory();
        expectCategoryInputsEnabled();
        const firstSave = document.querySelector<HTMLButtonElement>('#memory-category-save');
        expect(firstSave).toBeEnabled();
        fireEvent.click(firstSave!);
        await waitFor(() => expect(writes()).toHaveLength(1));

        attemptClosingWhilePending();
        expect(writes()).toHaveLength(1);

        // Failure belongs to this same editor: retain values and permit an
        // explicit retry, without creating another producer or auto-retrying.
        await act(async () => failedAttempt.release());
        await waitFor(() => expect(Message.error).toHaveBeenCalledWith(view.i18n.t('settings.memoryMutationError')));
        expectCategoryDraft();
        expectCategoryInputsEnabled();
        const retrySave = document.querySelector<HTMLButtonElement>('#memory-category-save');
        await waitFor(() => expect(retrySave).toBeEnabled());
        expect(writes()).toHaveLength(1);
        fireEvent.click(retrySave!);
        await waitFor(() => expect(writes()).toHaveLength(2));

        attemptClosingWhilePending();
        expect(writes()).toHaveLength(2);
        await act(async () => successfulAttempt.release());
        await expectCategoryClosed();
        expect(writes()).toHaveLength(2); // Exactly one POST for each explicit Save.

        // Completion unlocks the next operation and proves all three native
        // cancellation paths are reachable when idle, rather than no-ops.
        fillCategory();
        pressNativeEscape();
        await expectCategoryClosed();
        fillCategory();
        clickNativeCancel();
        await expectCategoryClosed();
        fillCategory();
        clickNativeClose();
        await expectCategoryClosed();
        expect(writes()).toHaveLength(2);
      } finally {
        await act(async () => {
          failedAttempt.release();
          successfulAttempt.release();
        });
      }
    });
  });
});
