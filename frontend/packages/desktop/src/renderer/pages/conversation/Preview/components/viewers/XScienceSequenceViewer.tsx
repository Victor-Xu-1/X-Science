import { PRODUCT_NAME } from '@/common/config/productIdentity';
import { Spin } from '@arco-design/web-react';
import React, { Suspense, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  logScientificPreviewError,
  resolveScientificPreviewError,
  ScientificPreviewError,
  scientificPreviewErrorKey,
} from './scientificPreviewError';
import { parseSequenceDocument, resolveSequenceFormat, type SequenceDocument } from './sequenceModel';

type XScienceSequenceViewerProps = {
  filename: string;
  contentUrl?: string;
  content?: string;
};

type SequenceViewerMode = 'both' | 'circular' | 'linear';

export const MAX_SEQUENCE_BYTES = 10 * 1024 * 1024;
const SEQUENCE_SIZE_LIMIT_LABEL = '10 MB';
const LazySeqViz = React.lazy(async () => {
  const module = await import('seqviz');
  return { default: module.SeqViz };
});

const VIEW_MODES: SequenceViewerMode[] = ['both', 'circular', 'linear'];

const XScienceSequenceViewer: React.FC<XScienceSequenceViewerProps> = ({ filename, contentUrl, content }) => {
  const { i18n, t } = useTranslation();
  const [document, setDocument] = useState<SequenceDocument | null>(null);
  const [viewerMode, setViewerMode] = useState<SequenceViewerMode>('both');
  const [zoom, setZoom] = useState(50);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ScientificPreviewError | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setError(null);
    setDocument(null);

    void loadSequenceContent({ filename, content, contentUrl, signal: controller.signal })
      .then((source) => parseSequenceDocument(source, filename))
      .then((nextDocument) => {
        if (active) setDocument(nextDocument);
      })
      .catch((reason: unknown) => {
        if (!active || controller.signal.aborted) return;
        logScientificPreviewError(`[${PRODUCT_NAME} SequenceViewer] Failed to load sequence`, reason, 'parse-failed');
        setError(resolveScientificPreviewError(reason, 'parse-failed'));
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [content, contentUrl, filename]);

  return (
    <section className='size-full min-h-500px flex flex-col bg-1' aria-label={t('preview.scientific.sequence.preview')}>
      <div className='min-h-46px px-12px py-7px flex flex-wrap items-center gap-10px border-b border-solid border-[var(--color-border-2)] bg-fill-1'>
        <div
          role='radiogroup'
          aria-label={t('preview.scientific.sequence.viewMode')}
          className='h-28px flex overflow-hidden rounded-4px border border-solid border-[var(--color-border-2)] bg-1'
        >
          {VIEW_MODES.map((mode) => (
            <button
              key={mode}
              type='button'
              role='radio'
              aria-checked={viewerMode === mode}
              className={`h-full px-10px border-0 border-r last:border-r-0 border-solid border-[var(--color-border-2)] text-11px cursor-pointer ${
                viewerMode === mode
                  ? 'bg-fill-3 text-t-primary font-[600]'
                  : 'bg-transparent text-t-secondary hover:bg-fill-1'
              }`}
              onClick={() => setViewerMode(mode)}
            >
              {t(`preview.scientific.sequence.modes.${mode}`)}
            </button>
          ))}
        </div>
        <label className='min-w-180px flex items-center gap-8px text-11px text-t-secondary'>
          {t('preview.scientific.zoom')}
          <input
            type='range'
            min={1}
            max={100}
            value={zoom}
            aria-label={t('preview.scientific.sequence.zoom')}
            className='w-120px accent-[rgb(var(--primary-6))]'
            onChange={(event) => setZoom(Number(event.target.value))}
          />
          <span className='w-30px tabular-nums text-right'>{zoom}%</span>
        </label>
        {document ? (
          <span className='ml-auto text-11px text-t-tertiary'>
            {document.format === 'fastq' && document.qualityStats
              ? t('preview.scientific.sequence.fastqStats', {
                  reads: (document.recordCount ?? 0).toLocaleString(i18n.resolvedLanguage),
                  bases: document.seq.length.toLocaleString(i18n.resolvedLanguage),
                  qualityMin: document.qualityStats.min,
                  qualityMax: document.qualityStats.max,
                  qualityMean: document.qualityStats.mean,
                })
              : t('preview.scientific.sequence.stats', {
                  bases: document.seq.length.toLocaleString(i18n.resolvedLanguage),
                  annotations: document.annotations.length.toLocaleString(i18n.resolvedLanguage),
                })}
          </span>
        ) : null}
      </div>

      <div className='relative min-h-500px flex-1 overflow-auto bg-white'>
        {document ? (
          <div data-testid='x-science-sequence-host' className='min-w-720px min-h-500px p-12px'>
            <Suspense fallback={<LoadingOverlay label={t('preview.scientific.sequence.initializing')} />}>
              <LazySeqViz
                name={document.name}
                seq={document.seq}
                seqType={document.type === 'unknown' ? undefined : document.type}
                annotations={document.annotations}
                primers={[]}
                viewer={viewerMode}
                zoom={{ linear: zoom }}
                showComplement
                showIndex
                rotateOnScroll
                disableExternalFonts
                style={{ width: '100%', height: 500 }}
              />
            </Suspense>
          </div>
        ) : null}
        {loading ? <LoadingOverlay label={t('preview.scientific.sequence.loading')} /> : null}
        {error ? (
          <div className='absolute inset-0 flex-center px-24px bg-1'>
            <div className='max-w-560px text-center text-13px text-danger-6'>
              {t(scientificPreviewErrorKey(error), {
                kind: t('preview.scientific.sequence.kind'),
                ...error.details,
              })}
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
};

const LoadingOverlay: React.FC<{ label: string }> = ({ label }) => (
  <div className='absolute inset-0 z-1 flex-center bg-1/90'>
    <Spin tip={label} />
  </div>
);

export async function loadSequenceContent({
  filename,
  content,
  contentUrl,
  signal,
}: {
  filename: string;
  content?: string;
  contentUrl?: string;
  signal: AbortSignal;
}): Promise<string | ArrayBuffer> {
  if (typeof content === 'string' && content.length > 0) {
    assertWithinLimit(new Blob([content]).size);
    return content;
  }
  if (!contentUrl) throw new ScientificPreviewError('missing-content');
  const response = await fetch(contentUrl, {
    credentials: 'include',
    headers: { Accept: 'text/plain, application/octet-stream, chemical/seq-na-genbank' },
    signal,
  });
  if (!response.ok) throw new ScientificPreviewError('request-failed', { status: response.status });
  const transportDecoded = response.headers.get('content-encoding')?.toLowerCase() === 'gzip';
  if (filename.toLowerCase().endsWith('.gz') && !transportDecoded) {
    rejectDeclaredOversize(response);
    const compressed = await readBoundedResponseBody(response, MAX_SEQUENCE_BYTES, signal);
    const decompressed = await decompressGzipBounded(compressed, MAX_SEQUENCE_BYTES, signal);
    if (resolveSequenceFormat(filename) === 'snapgene') return decompressed;
    const source = new TextDecoder('utf-8', { fatal: true }).decode(decompressed);
    assertWithinLimit(new Blob([source]).size);
    return source;
  }
  rejectDeclaredOversize(response);

  if (resolveSequenceFormat(filename) === 'snapgene') {
    return readBoundedResponseBody(response, MAX_SEQUENCE_BYTES, signal);
  }
  const bytes = await readBoundedResponseBody(response, MAX_SEQUENCE_BYTES, signal);
  const source = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  assertWithinLimit(new Blob([source]).size);
  return source;
}

function rejectDeclaredOversize(response: Response): void {
  const declaredLength = Number(response.headers.get('content-length'));
  if (Number.isFinite(declaredLength) && declaredLength > MAX_SEQUENCE_BYTES) {
    void response.body?.cancel();
    throw new ScientificPreviewError('too-large', { limit: SEQUENCE_SIZE_LIMIT_LABEL });
  }
}

function assertWithinLimit(size: number): void {
  if (size > MAX_SEQUENCE_BYTES) throw new ScientificPreviewError('too-large', { limit: SEQUENCE_SIZE_LIMIT_LABEL });
}

async function readBoundedResponseBody(response: Response, limit: number, signal: AbortSignal): Promise<ArrayBuffer> {
  if (!response.body) {
    const data = await response.arrayBuffer();
    if (data.byteLength > limit) throw new ScientificPreviewError('too-large', { limit: SEQUENCE_SIZE_LIMIT_LABEL });
    return data;
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  let complete = false;

  try {
    while (true) {
      signal.throwIfAborted();
      // oxlint-disable-next-line eslint/no-await-in-loop
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) {
        complete = true;
        break;
      }
      total += value.byteLength;
      if (total > limit) {
        throw new ScientificPreviewError('too-large', { limit: SEQUENCE_SIZE_LIMIT_LABEL });
      }
      chunks.push(value);
    }
  } finally {
    try {
      if (!complete) await reader.cancel();
    } finally {
      reader.releaseLock();
    }
  }

  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes.buffer;
}

async function decompressGzipBounded(data: ArrayBuffer, limit: number, signal: AbortSignal): Promise<ArrayBuffer> {
  if (typeof DecompressionStream === 'undefined') {
    throw new ScientificPreviewError('initialize-failed');
  }
  const stream = new Blob([data]).stream().pipeThrough(new DecompressionStream('gzip'));
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  let complete = false;

  try {
    while (true) {
      signal.throwIfAborted();
      // oxlint-disable-next-line eslint/no-await-in-loop
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) {
        complete = true;
        break;
      }
      total += value.byteLength;
      if (total > limit) {
        throw new ScientificPreviewError('too-large', { limit: SEQUENCE_SIZE_LIMIT_LABEL });
      }
      chunks.push(value);
    }
  } finally {
    try {
      if (!complete) await reader.cancel();
    } finally {
      reader.releaseLock();
    }
  }

  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes.buffer;
}

export default XScienceSequenceViewer;
