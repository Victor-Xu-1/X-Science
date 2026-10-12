import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import enCommon from '@/renderer/services/i18n/locales/en-US/common.json';
import zhCommon from '@/renderer/services/i18n/locales/zh-CN/common.json';
import StorageNamespaceUpgradeNotice from '@/renderer/components/layout/StorageNamespaceUpgradeNotice';

const locale = vi.hoisted(() => ({ language: 'en-US' }));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => {
      const common = locale.language === 'zh-CN' ? zhCommon : enCommon;
      return common[key.replace('common.', '') as keyof typeof common];
    },
  }),
}));

const failedReport = () => ({
  version: 1 as const,
  migrated: 1,
  failures: [{ area: 'localStorage', code: 'value_conflict' }],
  cachePending: false,
});

beforeEach(() => {
  locale.language = 'en-US';
  delete window.__xScienceStorageNamespaceUpgrade;
});
afterEach(() => {
  cleanup();
  delete window.__xScienceStorageNamespaceUpgrade;
});

describe('storage namespace upgrade recovery notice', () => {
  it('does not invent a failure on a fresh or fully upgraded visit', () => {
    render(<StorageNamespaceUpgradeNotice />);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows a localized nonblocking warning without displaying stored values', () => {
    window.__xScienceStorageNamespaceUpgrade = failedReport();
    const { rerender } = render(<StorageNamespaceUpgradeNotice />);
    expect(screen.getByRole('alert')).toHaveTextContent(enCommon.storageNamespaceUpgradeWarning);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('button', { name: enCommon.reload })).toBeInTheDocument();
    locale.language = 'zh-CN';
    rerender(<StorageNamespaceUpgradeNotice />);
    expect(screen.getByRole('alert')).toHaveTextContent(zhCommon.storageNamespaceUpgradeWarning);
    expect(screen.getByRole('button', { name: zhCommon.reload })).toBeInTheDocument();
  });

  it('supports dismissal and exposes a later asynchronous cache failure', () => {
    window.__xScienceStorageNamespaceUpgrade = failedReport();
    render(<StorageNamespaceUpgradeNotice />);
    fireEvent.click(screen.getByRole('button', { name: enCommon.close }));
    expect(screen.queryByRole('alert')).toBeNull();
    act(() => window.dispatchEvent(new Event('x-science:storage-upgrade-status')));
    expect(screen.getByRole('alert')).toBeInTheDocument();
    act(() => {
      window.__xScienceStorageNamespaceUpgrade!.failures = [];
      window.dispatchEvent(new Event('x-science:storage-upgrade-status'));
    });
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
