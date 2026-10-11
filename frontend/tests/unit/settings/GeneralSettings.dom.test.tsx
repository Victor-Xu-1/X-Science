import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18next from 'i18next';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import GeneralSettings from '@/renderer/pages/settings/GeneralSettings';
import enSettings from '@/renderer/services/i18n/locales/en-US/settings.json';
import zhSettings from '@/renderer/services/i18n/locales/zh-CN/settings.json';

const testI18n = i18next.createInstance();

const mocks = vi.hoisted(() => ({
  loadContact: vi.fn(),
  setContact: vi.fn(),
  removeContact: vi.fn(),
  selectTheme: vi.fn(),
  loadMessageChannels: vi.fn(),
}));

vi.mock('@/renderer/services/synonBiomedWorkspaceSettings', () => ({
  loadSynonBiomedContactEmail: mocks.loadContact,
  setSynonBiomedContactEmail: mocks.setContact,
  removeSynonBiomedContactEmail: mocks.removeContact,
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return { ...actual, Message: { ...actual.Message, error: vi.fn() } };
});

vi.mock('@/renderer/hooks/context/ThemeContext', () => ({
  useThemeContext: () => ({
    activeTheme: null,
    activeId: 'system',
    selectTheme: mocks.selectTheme,
  }),
}));

vi.mock('@/renderer/services/messageChannels', () => ({
  emptyMessageChannelStatuses: () => ({
    feishu: { configured: false, paired: false },
    wechat: { configured: false, paired: false },
  }),
  loadMessageChannelStatuses: mocks.loadMessageChannels,
  messageChannelErrorMessage: () => 'message channel request failed',
  pollMessageChannelQr: vi.fn(),
  startMessageChannelQr: vi.fn(),
  unpairMessageChannel: vi.fn(),
}));

vi.mock('@/renderer/components/settings/LanguageSwitcher', () => ({
  default: () => <div data-testid='language-switcher'>中文 / English</div>,
}));

vi.mock('@/renderer/pages/settings/components/SettingsPageWrapper', () => ({
  default: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
}));

vi.mock('@/renderer/pages/settings/components/SettingsPageHeader', () => ({
  default: ({ title, description }: { title: string; description?: string }) => (
    <header>
      <h1>{title}</h1>
      <p>{description}</p>
    </header>
  ),
}));

vi.mock('@/renderer/pages/settings/NetworkSettings', () => ({
  SettingsSection: ({
    title,
    actions,
    children,
  }: React.PropsWithChildren<{ title: string; actions?: React.ReactNode }>) => (
    <section>
      <h2>{title}</h2>
      {actions}
      {children}
    </section>
  ),
}));

describe('GeneralSettings', () => {
  beforeAll(async () => {
    await testI18n.init({
      lng: 'zh-CN',
      fallbackLng: 'zh-CN',
      resources: {
        'zh-CN': { translation: { settings: zhSettings } },
        'en-US': { translation: { settings: enSettings } },
      },
      interpolation: { escapeValue: false },
    });
  });

  beforeEach(async () => {
    vi.clearAllMocks();
    await testI18n.changeLanguage('zh-CN');
    mocks.loadContact.mockResolvedValue({
      decision: null,
      email: null,
      noticeText: 'Contact disclosure',
      noticeVersion: 'notice-1',
      noticeStale: false,
    });
    mocks.setContact.mockResolvedValue(undefined);
    mocks.loadMessageChannels.mockResolvedValue({
      feishu: { configured: false, paired: false },
      wechat: { configured: false, paired: false },
    });
  });

  it('keeps general settings focused while model management remains in its own module', async () => {
    renderGeneralSettings();

    expect(screen.getByRole('heading', { name: '通用' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '模型' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新增模型配置' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '语言' })).toBeInTheDocument();
    expect(screen.getByTestId('language-switcher')).toHaveTextContent('中文 / English');
    expect(screen.queryByRole('heading', { name: '会话默认值' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '语音输入' })).not.toBeInTheDocument();
    expect(screen.queryByText('语音转文字')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '授权' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '计算与模型端点' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '打开计算资源' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '外观' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '联系邮箱' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '关于' })).toBeInTheDocument();
    expect(screen.getByText('渠道：本地部署 · 版本更新由部署管理员执行')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '检查更新' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '第三方许可证' })).toBeInTheDocument();

    await waitFor(() => expect(mocks.loadContact).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: '设置' }));
    expect(screen.getByText('Contact disclosure')).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText('you@example.com'), {
      target: { value: 'lab@example.org' },
    });
    fireEvent.click(screen.getByRole('button', { name: '同意并保存' }));
    await waitFor(() => expect(mocks.setContact).toHaveBeenCalledWith('lab@example.org', 'notice-1'));
  });

  it('rejects malformed contact addresses and uses a controlled destructive confirmation', async () => {
    mocks.loadContact.mockResolvedValue({
      decision: 'allowed',
      email: 'lab@example.org',
      noticeText: 'Contact disclosure',
      noticeVersion: 'notice-1',
      noticeStale: false,
    });
    mocks.removeContact.mockResolvedValue(undefined);

    renderGeneralSettings();

    await screen.findByText('lab@example.org');
    fireEvent.click(screen.getByRole('button', { name: '更改' }));
    fireEvent.change(screen.getByPlaceholderText('you@example.com'), {
      target: { value: 'not-an-email' },
    });
    fireEvent.click(screen.getByRole('button', { name: '同意并保存' }));
    expect(await screen.findByText('请输入有效的邮箱地址。')).toHaveAttribute('role', 'alert');
    expect(mocks.setContact).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '移除' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('科研数据服务');
    fireEvent.click(screen.getAllByRole('button', { name: '移除' }).at(-1)!);
    await waitFor(() => expect(mocks.removeContact).toHaveBeenCalledTimes(1));
  });

  it('renders the complete general settings surface in English', async () => {
    await testI18n.changeLanguage('en-US');

    renderGeneralSettings();

    expect(screen.getByRole('heading', { name: 'General' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Language' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Session defaults' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Voice input' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Authorization' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Compute and model endpoints' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Open compute resources' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Appearance' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Contact email' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'About' })).toBeInTheDocument();
    await waitFor(() => {
      expect(mocks.loadContact).toHaveBeenCalledTimes(1);
    });
  });

  it('keeps unread contact data unavailable and retries a failed read without a write', async () => {
    await testI18n.changeLanguage('en-US');
    mocks.loadContact.mockRejectedValueOnce(new Error('offline'));
    renderGeneralSettings();
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load the contact email');
    expect(screen.queryByText('Not set')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Set' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry contact email' }));
    expect(await screen.findByText('Not set')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Set' })).toBeEnabled();
    expect(mocks.setContact).not.toHaveBeenCalled();
  });

  it('coalesces pending contact saves from the button and Enter key', async () => {
    await testI18n.changeLanguage('en-US');
    let finishSave!: () => void;
    mocks.setContact.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        finishSave = resolve;
      })
    );
    renderGeneralSettings();
    await screen.findByText('Not set');
    fireEvent.click(screen.getByRole('button', { name: 'Set' }));
    fireEvent.change(screen.getByPlaceholderText('you@example.com'), { target: { value: 'lab@example.org' } });
    fireEvent.click(screen.getByRole('button', { name: 'Agree and save' }));
    fireEvent.keyDown(screen.getByPlaceholderText('you@example.com'), { key: 'Enter', code: 'Enter' });
    expect(mocks.setContact).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: 'Agree and save' })).toBeDisabled();
    finishSave();
    await waitFor(() => expect(screen.queryByPlaceholderText('you@example.com')).not.toBeInTheDocument());
  });

  it('does not expose an editable empty state while contact metadata is still loading', async () => {
    let finishRead!: (value: unknown) => void;
    mocks.loadContact.mockReturnValueOnce(
      new Promise((resolve) => {
        finishRead = resolve;
      })
    );
    renderGeneralSettings();
    expect(screen.getByRole('status')).toHaveTextContent('正在读取联系邮箱');
    expect(screen.queryByText('未设置')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '设置' })).toBeDisabled();
    await act(async () =>
      finishRead({
        decision: null,
        email: null,
        noticeVersion: 'notice-1',
        noticeText: 'Contact disclosure',
        noticeStale: false,
      })
    );
    expect(screen.getByText('未设置')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '设置' })).toBeEnabled();
  });

  it('retries only the read when contact saving succeeded but refreshing failed', async () => {
    renderGeneralSettings();
    await screen.findByText('未设置');
    fireEvent.click(screen.getByRole('button', { name: '设置' }));
    fireEvent.change(screen.getByPlaceholderText('you@example.com'), { target: { value: 'lab@example.org' } });
    mocks.loadContact.mockRejectedValueOnce(new Error('refresh offline'));
    fireEvent.click(screen.getByRole('button', { name: '同意并保存' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('无法读取联系邮箱');
    fireEvent.click(screen.getByRole('button', { name: '重试读取联系邮箱' }));
    await screen.findByText('未设置');
    expect(mocks.setContact).toHaveBeenCalledTimes(1);
    expect(mocks.loadContact).toHaveBeenCalledTimes(3);
  });
});

function renderGeneralSettings() {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter>
        <GeneralSettings />
      </MemoryRouter>
    </I18nextProvider>
  );
}
