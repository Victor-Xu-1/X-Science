/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { httpRequest, type HttpRequestOptions } from '@/common/adapter/httpBridge';
import type { ClientBusinessSettingKey, ClientBusinessSettingMap } from '@/common/config/clientSettings';

export async function getClientBusinessSetting<K extends ClientBusinessSettingKey>(
  key: K,
  options?: HttpRequestOptions
): Promise<ClientBusinessSettingMap[K] | undefined> {
  const path = `/api/settings/client?keys=${encodeURIComponent(key)}`;
  const data = options
    ? await httpRequest<Record<string, ClientBusinessSettingMap[K] | undefined>>('GET', path, undefined, options)
    : await httpRequest<Record<string, ClientBusinessSettingMap[K] | undefined>>('GET', path);
  return data?.[key];
}

export async function setClientBusinessSetting<K extends ClientBusinessSettingKey>(
  key: K,
  value: ClientBusinessSettingMap[K],
  options?: HttpRequestOptions
): Promise<void> {
  if (options) await httpRequest<void>('PUT', '/api/settings/client', { [key]: value }, options);
  else await httpRequest<void>('PUT', '/api/settings/client', { [key]: value });
}

export async function removeClientBusinessSetting<K extends ClientBusinessSettingKey>(key: K): Promise<void> {
  await httpRequest<void>('PUT', '/api/settings/client', { [key]: null });
}
