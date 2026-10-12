import { Button, Input, Message, Select } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type {
  XScienceCustomMcpServer,
  XScienceCustomMcpServerInput,
} from '@/renderer/services/xScienceCapabilities';

type Props = {
  visible: boolean;
  server: XScienceCustomMcpServer | null;
  onCancel: () => void;
  onSubmit: (input: XScienceCustomMcpServerInput) => Promise<void>;
};

type FormState = {
  name: string;
  description: string;
  url: string;
  transport: 'sse' | 'streamable_http';
  oauthServerUrl: string;
  clientId: string;
  scopes: string;
  headersHelper: string;
};

type ValidationError = 'nameRequired' | 'nameInvalid' | 'serverUrlInvalid' | 'oauthUrlInvalid';

const EMPTY_FORM: FormState = {
  name: '',
  description: '',
  url: '',
  transport: 'streamable_http',
  oauthServerUrl: '',
  clientId: '',
  scopes: '',
  headersHelper: '',
};

export const McpConnectorEditorModal: React.FC<Props> = ({ visible, server, onCancel, onSubmit }) => {
  const { t } = useTranslation();
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [advanced, setAdvanced] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!visible) return;
    setForm(
      server
        ? {
            name: server.name,
            description: server.description ?? '',
            url: server.url,
            transport: server.transport,
            oauthServerUrl: server.oauthServerUrl ?? '',
            clientId: server.clientId ?? '',
            scopes: server.scopes ?? '',
            headersHelper: server.headersHelper ?? '',
          }
        : EMPTY_FORM
    );
    setAdvanced(Boolean(server?.oauthServerUrl || server?.clientId || server?.scopes || server?.headersHelper));
  }, [server, visible]);

  const validationError = useMemo(() => validate(form), [form]);

  const submit = async () => {
    if (validationError) {
      Message.error(t(`settings.xScienceMcpValidation${capitalize(validationError)}`));
      return;
    }
    setSaving(true);
    try {
      await onSubmit({
        name: form.name.trim(),
        description: emptyToNull(form.description),
        url: form.url.trim(),
        transport: form.transport,
        oauthServerUrl: emptyToNull(form.oauthServerUrl),
        clientId: emptyToNull(form.clientId),
        scopes: emptyToNull(form.scopes),
        headersHelper: emptyToNull(form.headersHelper),
      });
    } catch {
      // The parent owns the user-facing mutation error. Keep the modal open for correction or retry.
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      visible={visible}
      title={server ? t('settings.xScienceMcpEditConnector') : t('settings.xScienceMcpAddConnector')}
      footer={
        <div className='flex justify-end gap-8px'>
          <Button onClick={onCancel}>{t('common.cancel')}</Button>
          <Button
            type='primary'
            loading={saving}
            data-testid='x-science-mcp-editor-submit'
            onClick={() => void submit()}
          >
            {server ? t('common.save') : t('settings.xScienceMcpAdd')}
          </Button>
        </div>
      }
      onCancel={onCancel}
      style={{ width: 640, maxWidth: 'calc(100vw - 24px)' }}
      unmountOnExit
    >
      <div className='flex flex-col gap-14px' data-testid='x-science-mcp-editor'>
        <div className='grid grid-cols-2 gap-8px rounded-6px bg-fill-2 p-4px'>
          <button
            type='button'
            className='h-34px rounded-4px border-0 bg-2 text-13px font-600 text-t-primary shadow-sm'
          >
            {t('settings.xScienceMcpRemoteUrl')}
          </button>
          <button
            type='button'
            disabled
            title={t('settings.xScienceMcpLocalUnavailableDetail')}
            className='h-34px rounded-4px border-0 bg-transparent text-13px text-t-tertiary opacity-60 cursor-not-allowed'
          >
            {t('settings.xScienceMcpLocalCommand')}
          </button>
        </div>

        <Field label={t('common.name')} required>
          <Input
            value={form.name}
            data-testid='x-science-mcp-editor-name'
            placeholder='my-mcp-server'
            onChange={(name) => setForm((current) => ({ ...current, name }))}
          />
          <div className='mt-4px text-11px text-t-tertiary'>{t('settings.xScienceMcpNameHint')}</div>
        </Field>

        <Field label={t('settings.xScienceMcpServerUrl')} required>
          <Input
            value={form.url}
            data-testid='x-science-mcp-editor-url'
            placeholder='https://mcp.example.com/mcp'
            onChange={(url) => setForm((current) => ({ ...current, url }))}
          />
        </Field>

        <Field label={t('settings.xScienceMcpTransport')} required>
          <Select
            value={form.transport}
            data-testid='x-science-mcp-editor-transport'
            onChange={(transport) => setForm((current) => ({ ...current, transport }))}
          >
            <Select.Option value='streamable_http'>Streamable HTTP</Select.Option>
            <Select.Option value='sse'>Server-Sent Events (SSE)</Select.Option>
          </Select>
        </Field>

        <Field label={t('common.description')}>
          <Input.TextArea
            value={form.description}
            autoSize={{ minRows: 2, maxRows: 4 }}
            onChange={(description) => setForm((current) => ({ ...current, description }))}
          />
        </Field>

        <button
          type='button'
          className='w-fit border-0 bg-transparent p-0 text-13px font-600 text-t-primary cursor-pointer'
          aria-expanded={advanced}
          onClick={() => setAdvanced((value) => !value)}
        >
          {advanced ? '−' : '+'} {t('settings.xScienceMcpAdvanced')}
        </button>

        {advanced ? (
          <div className='grid grid-cols-1 gap-12px border-l-2 border-arco-2 pl-12px'>
            <Field label={t('settings.xScienceMcpOauthServerUrl')}>
              <Input
                value={form.oauthServerUrl}
                placeholder='https://auth.example.com'
                onChange={(oauthServerUrl) => setForm((current) => ({ ...current, oauthServerUrl }))}
              />
            </Field>
            <Field label={t('settings.xScienceMcpOauthClientId')}>
              <Input value={form.clientId} onChange={(clientId) => setForm((current) => ({ ...current, clientId }))} />
            </Field>
            <Field label={t('settings.xScienceMcpOauthScopes')}>
              <Input
                value={form.scopes}
                placeholder='read write'
                onChange={(scopes) => setForm((current) => ({ ...current, scopes }))}
              />
            </Field>
            <Field label={t('settings.xScienceMcpHeadersHelper')}>
              <Input
                value={form.headersHelper}
                onChange={(headersHelper) => setForm((current) => ({ ...current, headersHelper }))}
              />
            </Field>
          </div>
        ) : null}

        <div className='rounded-6px border border-warning-3 bg-warning-1 px-12px py-10px text-12px leading-5 text-t-secondary'>
          {t('settings.xScienceMcpTrustWarning')}
        </div>
      </div>
    </Modal>
  );
};

const Field: React.FC<{ label: string; required?: boolean; children: React.ReactNode }> = ({
  label,
  required,
  children,
}) => (
  <label className='block'>
    <span className='mb-6px block text-12px font-600 text-t-primary'>
      {label} {required ? <span className='text-t-primary'>*</span> : null}
    </span>
    {children}
  </label>
);

function validate(form: FormState): ValidationError | null {
  const name = form.name.trim();
  if (!name) return 'nameRequired';
  if (!/^[a-z0-9-]+$/.test(name)) return 'nameInvalid';
  if (!isPublicHttpsUrl(form.url)) return 'serverUrlInvalid';
  if (form.oauthServerUrl.trim() && !isPublicHttpsUrl(form.oauthServerUrl)) return 'oauthUrlInvalid';
  return null;
}

function capitalize(value: ValidationError): string {
  return `${value.charAt(0).toUpperCase()}${value.slice(1)}`;
}

function isPublicHttpsUrl(value: string): boolean {
  try {
    return new URL(value.trim()).protocol === 'https:';
  } catch {
    return false;
  }
}

function emptyToNull(value: string): string | null {
  const normalized = value.trim();
  return normalized || null;
}
