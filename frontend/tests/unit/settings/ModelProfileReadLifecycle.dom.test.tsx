import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SynonBiomedModelsSettingsContent } from '@/renderer/pages/settings/SynonBiomedModelsSettings';
import type { SynonBiomedLlmProvidersSnapshot } from '@/renderer/services/synonBiomedLlm';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({
  load: vi.fn(),
  save: vi.fn(),
  activate: vi.fn(),
  remove: vi.fn(),
  test: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock('@/renderer/services/synonBiomedLlm', () => ({
  loadSynonBiomedLlmProviders: mocks.load,
  saveSynonBiomedLlmProfile: mocks.save,
  activateSynonBiomedLlmProfile: mocks.activate,
  deleteSynonBiomedLlmProfile: mocks.remove,
  testSynonBiomedLlmProfile: mocks.test,
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return { ...actual, Message: { ...actual.Message, success: mocks.success, error: mocks.error } };
});

const snapshot: SynonBiomedLlmProvidersSnapshot = {
  activeProfileId: 'first',
  templates: [
    { provider: 'custom', label: 'Custom', defaultBaseUrl: '', modelExamples: [], protocol: 'openai' },
    {
      provider: 'deepseek',
      label: 'DeepSeek',
      defaultBaseUrl: 'https://example.invalid/v1',
      modelExamples: ['model-example'],
      protocol: 'openai',
    },
  ],
  profiles: [
    {
      id: 'first',
      name: 'First model',
      provider: 'custom',
      baseUrl: 'https://example.invalid/v1',
      model: 'model-first',
      hasApiKey: true,
      apiKeySource: 'stored',
    },
    {
      id: 'second',
      name: 'Second model',
      provider: 'deepseek',
      baseUrl: 'https://example.invalid/v1',
      model: 'model-second',
      hasApiKey: false,
      apiKeySource: 'missing',
    },
  ],
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

const freshSnapshot = () => structuredClone(snapshot);
const newestSnapshot = () => ({
  ...freshSnapshot(),
  profiles: [{ ...snapshot.profiles[0], name: 'Newest model' }],
});

describe('model profile read and editor ownership', () => {
  beforeEach(() => {
    mocks.load.mockImplementation(async () => freshSnapshot());
    mocks.save.mockResolvedValue({ ok: true });
    mocks.activate.mockResolvedValue({ ok: true });
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.resetAllMocks();
  });

  it('does not report zero/unconfigured or allow editing before the first read resolves', async () => {
    const pending = deferred<SynonBiomedLlmProvidersSnapshot>();
    mocks.load.mockReturnValueOnce(pending.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    expect(screen.getByRole('button', { name: '新增模型配置' })).toBeDisabled();
    expect(screen.queryByText('未配置')).not.toBeInTheDocument();
    expect(document.querySelector('.settings-summary-strip')?.firstElementChild).not.toHaveTextContent('0');
    await act(async () => pending.resolve(freshSnapshot()));
    expect(await screen.findByText('First model')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增模型配置' })).toBeEnabled();
  });

  it('keeps a failed initial read distinct from an empty catalogue and retries only the read', async () => {
    mocks.load.mockRejectedValueOnce(new Error('offline'));
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    expect(await screen.findByRole('alert')).toHaveTextContent('无法加载 X-Science 模型配置');
    expect(screen.queryByText('尚未配置模型')).not.toBeInTheDocument();
    expect(screen.queryByText('未配置')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增模型配置' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('First model')).toBeInTheDocument();
    expect(mocks.load).toHaveBeenCalledTimes(2);
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.test).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('allows a pending metadata read to be superseded without waiting forever', async () => {
    const older = deferred<SynonBiomedLlmProvidersSnapshot>();
    mocks.load.mockReturnValueOnce(older.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    expect(screen.getByRole('button', { name: '刷新' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    expect(await screen.findByText('First model')).toBeInTheDocument();
    await act(async () => older.reject(new Error('superseded read')));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(mocks.load).toHaveBeenCalledTimes(2);
  });

  it('passes abort signals only to metadata fetches and disposes superseded reads', async () => {
    const reads = [deferred<Response>(), deferred<Response>()];
    const signals: AbortSignal[] = [];
    const fetchMetadata = vi.fn((_input: string, init: RequestInit) => {
      signals.push(init.signal as AbortSignal);
      return reads[signals.length - 1].promise;
    });
    vi.stubGlobal('fetch', fetchMetadata);
    mocks.load.mockImplementation(async (fetchImpl: typeof fetchMetadata) => {
      await fetchImpl('/api/llm/providers', {});
      return freshSnapshot();
    });
    const view = await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    expect(signals[0].aborted).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    expect(signals[0].aborted).toBe(true);
    expect(signals[1].aborted).toBe(false);
    view.unmount();
    expect(signals[1].aborted).toBe(true);
    await act(async () => {
      reads[0].resolve(new Response('{}'));
      reads[1].resolve(new Response('{}'));
    });
    expect(mocks.test).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('labels retained data after refresh failure without treating it as fresh actionable state', async () => {
    mocks.load.mockResolvedValueOnce(freshSnapshot()).mockRejectedValueOnce(new Error('refresh failed'));
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('上次成功读取');
    const retained = screen.getByTestId('synon-biomed-model-profile-first');
    expect(retained).toHaveTextContent('First model');
    expect(retained).not.toHaveTextContent('当前使用');
    expect(within(retained).getByRole('button', { name: '测试 First model' })).toBeDisabled();
    expect(within(retained).getByRole('button', { name: '编辑 First model' })).toBeDisabled();
    mocks.load.mockResolvedValueOnce(newestSnapshot());
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('Newest model')).toBeInTheDocument();
    expect(mocks.load).toHaveBeenCalledTimes(3);
    expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it('ignores an older read after the action-triggered newest read has completed', async () => {
    const action = deferred<unknown>();
    const olderRead = deferred<SynonBiomedLlmProvidersSnapshot>();
    const newestRead = deferred<SynonBiomedLlmProvidersSnapshot>();
    mocks.activate.mockReturnValueOnce(action.promise);
    mocks.load
      .mockResolvedValueOnce(freshSnapshot())
      .mockReturnValueOnce(olderRead.promise)
      .mockReturnValueOnce(newestRead.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '将 Second model 设为当前模型' }));
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    await act(async () => action.resolve({ ok: true }));
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(3));
    await act(async () => newestRead.resolve(newestSnapshot()));
    expect(await screen.findByText('Newest model')).toBeInTheDocument();
    await act(async () => olderRead.resolve(freshSnapshot()));
    expect(screen.getByText('Newest model')).toBeInTheDocument();
    expect(screen.queryByText('First model')).not.toBeInTheDocument();
  });

  it('does not publish an obsolete read failure over the latest successful catalogue', async () => {
    const action = deferred<unknown>();
    const olderRead = deferred<SynonBiomedLlmProvidersSnapshot>();
    mocks.activate.mockReturnValueOnce(action.promise);
    mocks.load.mockResolvedValueOnce(freshSnapshot()).mockReturnValueOnce(olderRead.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '将 Second model 设为当前模型' }));
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    await act(async () => action.resolve({ ok: true }));
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(3));
    await act(async () => olderRead.reject(new Error('obsolete read failed')));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('does not notify another route when a disposed read fails', async () => {
    const pending = deferred<SynonBiomedLlmProvidersSnapshot>();
    mocks.load.mockReturnValueOnce(pending.promise);
    const view = await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    view.unmount();
    await act(async () => pending.reject(new Error('disposed read failed')));
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it('uses the same localized provider label in the catalogue and editor', async () => {
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    const profile = await screen.findByTestId('synon-biomed-model-profile-first');
    expect(within(profile).getByText('自定义 / OpenAI 兼容')).toBeInTheDocument();
    expect(within(profile).queryByText('custom')).not.toBeInTheDocument();
  });

  it('presents the existing provider label in English without changing its identity', async () => {
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'en-US');
    const profile = await screen.findByTestId('synon-biomed-model-profile-first');
    expect(within(profile).getByText('Custom / OpenAI-compatible')).toBeInTheDocument();
    expect(profile).toHaveTextContent('model-first');
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it('keeps an unknown provider identity visible instead of relabeling it as a known platform', async () => {
    mocks.load.mockResolvedValueOnce({
      ...freshSnapshot(),
      profiles: [{ ...snapshot.profiles[0], provider: 'vendor-next' }],
    });
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'en-US');
    const profile = await screen.findByTestId('synon-biomed-model-profile-first');
    expect(within(profile).getByText('vendor-next')).toBeInTheDocument();
    expect(within(profile).queryByText('Custom / OpenAI-compatible')).not.toBeInTheDocument();
  });

  it('changes language without refetching metadata or erasing the current draft', async () => {
    const view = await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Preserved draft' } });
    await act(async () => view.i18n.changeLanguage('en-US'));
    expect(mocks.load).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText('Profile name')).toHaveValue('Preserved draft');
  });

  it('preserves an open draft when a completed action refreshes provider templates', async () => {
    const action = deferred<unknown>();
    mocks.activate.mockReturnValueOnce(action.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '将 Second model 设为当前模型' }));
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Preserved draft' } });
    await act(async () => action.resolve({ ok: true }));
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(2));
    expect(screen.getByLabelText('配置名称')).toHaveValue('Preserved draft');
  });

  it('does not close a newer editor when the cancelled older save completes', async () => {
    const saving = deferred<unknown>();
    mocks.save.mockReturnValueOnce(saving.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Submitted draft' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Newer draft' } });
    await act(async () => saving.resolve({ ok: true }));
    expect(screen.getByRole('dialog', { name: '新增模型配置' })).toBeInTheDocument();
    expect(screen.getByLabelText('配置名称')).toHaveValue('Newer draft');
    expect(mocks.save).toHaveBeenCalledTimes(1);
    expect(mocks.save.mock.calls[0][0]).toMatchObject({ name: 'Submitted draft', provider: 'deepseek' });
  });

  it('does not report an older save failure as a failure of the new editor', async () => {
    const saving = deferred<unknown>();
    mocks.save.mockReturnValueOnce(saving.promise);
    await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Submitted draft' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Newer draft' } });
    await act(async () => saving.reject(new Error('older save failed')));
    expect(mocks.error).not.toHaveBeenCalled();
    expect(screen.getByLabelText('配置名称')).toHaveValue('Newer draft');
    expect(mocks.save).toHaveBeenCalledTimes(1);
  });

  it('does not notify a different route after a disposed editor save fails', async () => {
    const saving = deferred<unknown>();
    mocks.save.mockReturnValueOnce(saving.promise);
    const view = await renderWithI18n(<SynonBiomedModelsSettingsContent />, 'zh-CN');
    await screen.findByText('First model');
    fireEvent.click(screen.getByRole('button', { name: '新增模型配置' }));
    fireEvent.change(screen.getByLabelText('配置名称'), { target: { value: 'Submitted draft' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    view.unmount();
    await act(async () => saving.reject(new Error('disposed save failed')));
    expect(mocks.error).not.toHaveBeenCalled();
  });
});
