import type { FileOrFolderItem } from '@/renderer/hooks/chat/useSendBoxDraft';
import ComposerContextChips from '@/renderer/components/chat/SendBox/ComposerContextChips';
import {
  removeComposerContextItem,
  type ComposerContextItem,
  type ComposerArtifactContext,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';
import FilePreview from '@/renderer/components/media/FilePreview';
import HorizontalFileList from '@/renderer/components/media/HorizontalFileList';
import { emitter } from '@/renderer/utils/emitter';
import { Tag } from '@arco-design/web-react';
import React from 'react';

/** Existing attachment/directory presentation, separated from send authority. */
export function AcpComposerAttachments({
  contextItems,
  setContextItems,
  uploadFile,
  setUploadFile,
  atPath,
  setAtPath,
  onOpenArtifact,
}: {
  contextItems: ComposerContextItem[];
  onOpenArtifact?: (item: ComposerArtifactContext) => void;
  setContextItems: (next: ComposerContextItem[] | ((current: ComposerContextItem[]) => ComposerContextItem[])) => void;
  uploadFile: string[];
  setUploadFile: (next: string[]) => void;
  atPath: Array<string | FileOrFolderItem>;
  setAtPath: (next: Array<string | FileOrFolderItem>) => void;
}) {
  return (
    <>
      <ComposerContextChips
        items={contextItems}
        onOpenArtifact={onOpenArtifact}
        onRemove={(key) => setContextItems((current) => removeComposerContextItem(current, key))}
      />
      {uploadFile.length > 0 && (
        <HorizontalFileList>
          {uploadFile.map((path) => (
            <FilePreview
              key={path}
              path={path}
              onRemove={() => setUploadFile(uploadFile.filter((item) => item !== path))}
            />
          ))}
        </HorizontalFileList>
      )}
      {atPath.some((item) => typeof item !== 'string' && !item.isFile) && (
        <div className='flex flex-wrap items-center gap-8px mb-8px'>
          {atPath.map((item) =>
            typeof item === 'string' || item.isFile ? null : (
              <Tag
                key={item.path}
                color='blue'
                closable
                onClose={() => {
                  const next = atPath.filter(
                    (candidate) => typeof candidate === 'string' || candidate.path !== item.path
                  );
                  emitter.emit('acp.selected.file', next);
                  setAtPath(next);
                }}
              >
                {item.name}
              </Tag>
            )
          )}
        </div>
      )}
    </>
  );
}
