import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { decodeResourceTagCommand, type ResourceTag, type ResourceTagCommand } from '@/common/adapter/resourceTags';
import { resourceTagDefiniteRejection } from '@/renderer/services/runtime/resourceTagMutationQueue';
import { ResourceTagVisual } from '../../library/ResourceTagBadges';

export function TagDeleteConfirm({
  tag,
  count,
  busy,
  submit,
  close,
}: {
  tag: ResourceTag;
  count: number;
  busy: boolean;
  submit(command: ResourceTagCommand): Promise<string | undefined>;
  close(): void;
}) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const intent = useRef<ResourceTagCommand | null>(null);
  const mounted = useRef(true);
  const sending = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const remove = async () => {
    if (sending.current || busy || 'system_key' in tag) return;
    const command =
      intent.current ?? decodeResourceTagCommand({ kind: 'delete', tag_id: tag.id, operation_id: crypto.randomUUID() });
    intent.current = command;
    sending.current = true;
    setSaving(true);
    setFailed(false);
    try {
      await submit(command);
      if (mounted.current) close();
    } catch (error) {
      if (mounted.current) {
        if (resourceTagDefiniteRejection(error)) intent.current = null;
        setFailed(true);
      }
    } finally {
      sending.current = false;
      if (mounted.current) setSaving(false);
    }
  };
  return (
    <Modal
      visible
      title={t('settings.resourceTags.deleteQuestion')}
      style={{ width: 'min(440px, calc(100vw - 32px))' }}
      onCancel={() => {
        if (!saving) close();
      }}
      maskClosable={!saving}
      escToExit={!saving}
      closable={!saving}
      footer={
        <div className='tags-actions tags-actions--end'>
          <button type='button' className='settings-action-button' disabled={saving} onClick={close}>
            {t('common.cancel')}
          </button>
          <button
            type='button'
            className='settings-action-button tags-action--danger'
            disabled={saving || busy}
            onClick={() => void remove()}
          >
            {t(
              saving
                ? 'settings.resourceTags.deleting'
                : failed && intent.current
                  ? 'settings.resourceTags.resumeDelete'
                  : 'settings.resourceTags.delete'
            )}
          </button>
        </div>
      }
    >
      <ResourceTagVisual tag={tag} favorite={t('conversation.resourceTags.favorite')} />
      <p className='tags-muted'>{t('settings.resourceTags.deleteHint')}</p>
      <p>{t('settings.resourceTags.assignmentCount', { count })}</p>
      {failed && (
        <p role='alert' className='tags-notice'>
          {t('settings.resourceTags.deleteFailed')}
        </p>
      )}
    </Modal>
  );
}
