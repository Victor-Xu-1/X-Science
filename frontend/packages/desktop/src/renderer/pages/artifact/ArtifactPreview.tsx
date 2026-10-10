import {
  getSynonBiomedArtifactVersionContentUrl,
  type SynonBiomedArtifactLineage,
  type SynonBiomedArtifactVersion,
} from '@/renderer/services/synonBiomedArtifacts';
import {
  getSynonBiomedArtifactContentUrl,
  loadSynonBiomedArtifact,
  type SynonBiomedProjectArtifact,
} from '@/renderer/services/synonBiomedGateway';
import { resolveSynonBiomedArtifactPreviewPlan } from '@/renderer/services/synonBiomedArtifactPreview';
import {
  cachePreviewModule,
  LazyExcelPreview as ExcelPreview,
  LazySynonBiomedGenomeViewer as SynonBiomedGenomeViewer,
  LazySynonBiomedHdf5Viewer as SynonBiomedHdf5Viewer,
  LazySynonBiomedMoleculeViewer as SynonBiomedMoleculeViewer,
  LazySynonBiomedMsaViewer as SynonBiomedMsaViewer,
  LazySynonBiomedNotebookViewer as SynonBiomedNotebookViewer,
  LazyOfficeDocPreview as OfficeDocPreview,
  LazyPptPreview as PptViewer,
  LazySynonBiomedSequenceViewer as SynonBiomedSequenceViewer,
  LazySynonBiomedStructureViewer as SynonBiomedStructureViewer,
  LazySynonBiomedTableViewer as SynonBiomedTableViewer,
  LazyUnsupportedPreview as UnsupportedPreview,
} from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewLoaders';
import PreviewLoadingState from '@/renderer/components/media/PreviewLoadingState';
import SynonBiomedNotesModal from '@/renderer/components/synonBiomed/notes/SynonBiomedNotesModal';
import type { SynonBiomedArtifactTextSelection } from './artifactTextSelection';
import type { SynonBiomedArtifactCanvasSelection } from './artifactCanvasSelection';
import { ArtifactAnnotationsPanel, ArtifactVerificationPanel } from './ArtifactAnnotationPanels';
import { ArtifactEditRefinementPanel } from './ArtifactEditRefinementPanel';
import { ArtifactSelectionAnnotationModal } from './ArtifactSelectionAnnotationModal';
import { ArtifactFileActionModal } from './ArtifactFileActionModal';
import { ArtifactPageState } from './ArtifactPageState';
import { ArtifactMetadataState } from './ArtifactMetadataState';
import { useArtifactMetadataReads } from './useArtifactMetadataReads';
import { useArtifactRecoveryFocus } from './useArtifactRecoveryFocus';
import { ArtifactDetails } from './ArtifactDetails';
import { formatBytes, formatDate } from './artifactPresentation';
import type { ArtifactFileAction } from './useArtifactFileActionEditor';
import {
  loadSynonBiomedArtifactAnnotations,
  type SynonBiomedAppliedArtifactEdit,
  type SynonBiomedArtifactAnnotation,
} from '@/renderer/services/synonBiomedAnnotations';
import { Button, Empty, Message } from '@arco-design/web-react';
import Tabs from '@/renderer/components/base/WorkbenchTabs';
import { Comment, Copy, Download, FileText, FolderOpen, Left, Magic, Notes, Upload } from '@icon-park/react';
import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate, useParams } from 'react-router';

const SynonBiomedMcpAppArtifactViewer = React.lazy(
  cachePreviewModule(
    () => import('@/renderer/pages/conversation/Preview/components/viewers/SynonBiomedMcpAppArtifactViewer')
  )
);
const SynonBiomedLatexArtifactViewer = React.lazy(cachePreviewModule(() => import('./SynonBiomedLatexArtifactViewer')));
const SynonBiomedTextArtifactViewer = React.lazy(cachePreviewModule(() => import('./SynonBiomedTextArtifactViewer')));
const SynonBiomedPdfArtifactViewer = React.lazy(cachePreviewModule(() => import('./SynonBiomedPdfArtifactViewer')));
const SynonBiomedImageArtifactViewer = React.lazy(
  cachePreviewModule(() =>
    import('./SynonBiomedImageArtifactViewer').then((module) => ({
      default: module.SynonBiomedImageArtifactViewer,
    }))
  )
);
const AudioPreview = React.lazy(cachePreviewModule(() => import('./AudioPreview')));
const VideoPreview = React.lazy(cachePreviewModule(() => import('./VideoPreview')));
const ArchiveArtifactPreview = React.lazy(cachePreviewModule(() => import('./ArchiveArtifactPreview')));

type ArtifactSnapshot = {
  artifact: SynonBiomedProjectArtifact;
};

const ArtifactPreview: React.FC = () => {
  const { t } = useTranslation();
  const { artifactId } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const [snapshot, setSnapshot] = useState<ArtifactSnapshot | null>(null);
  const [loading, setLoading] = useState(Boolean(artifactId));
  const [failed, setFailed] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [requestOwnerId, setRequestOwnerId] = useState(artifactId);
  const [selectedVersionId, setSelectedVersionId] = useState<string | null>(null);
  const [pinnedVersion, setPinnedVersion] = useState<SynonBiomedArtifactVersion | null>(null);
  const [savedVersion, setSavedVersion] = useState<SynonBiomedAppliedArtifactEdit | null>(null);
  const [metadataRevision, setMetadataRevision] = useState(0);
  const [activeInspectorTab, setActiveInspectorTab] = useState(() => inspectorTabFromSearch(location.search));
  const [canvasSelection, setCanvasSelection] = useState<SynonBiomedArtifactCanvasSelection | null>(null);
  const [annotationSelection, setAnnotationSelection] = useState<SynonBiomedArtifactCanvasSelection | null>(null);
  const [refinementSelection, setRefinementSelection] = useState<SynonBiomedArtifactTextSelection | null>(null);
  const [previewAnnotations, setPreviewAnnotations] = useState<SynonBiomedArtifactAnnotation[]>([]);
  const [annotationRevision, setAnnotationRevision] = useState(0);
  const [fileAction, setFileAction] = useState<ArtifactFileAction | null>(null);
  const [notesVisible, setNotesVisible] = useState(false);
  const [messageApi, messageContextHolder] = Message.useMessage();
  const previewReturnRef = useRef<HTMLElement | null>(null);
  const resourcesFailureRef = useRef<HTMLDivElement | null>(null);
  const pageHeadingRef = useRef<HTMLHeadingElement | null>(null);
  const retryFocusIntent = useRef(false);
  const versionRefreshRevision = useRef(0);
  const viewOwner = useRef({ artifactId, selectedVersionId });
  useLayoutEffect(() => {
    viewOwner.current = { artifactId, selectedVersionId };
    versionRefreshRevision.current += 1;
    return () => {
      versionRefreshRevision.current += 1;
    };
  }, [artifactId, selectedVersionId]);
  useLayoutEffect(() => {
    setFileAction(null);
    setNotesVisible(false);
    setPinnedVersion(null);
    setSavedVersion(null);
    retryFocusIntent.current = false;
  }, [artifactId]);
  useLayoutEffect(() => {
    if (!retryFocusIntent.current || loading || failed || snapshot?.artifact.artifactId !== artifactId) return;
    retryFocusIntent.current = false;
    pageHeadingRef.current?.focus({ preventScroll: true });
  }, [artifactId, failed, loading, snapshot]);

  useEffect(() => {
    setActiveInspectorTab(inspectorTabFromSearch(location.search));
  }, [location.search]);

  useEffect(() => {
    setRequestOwnerId(artifactId);
    if (!artifactId) {
      setLoading(false);
      setFailed(false);
      setSnapshot(null);
      return;
    }

    let active = true;
    setLoading(true);
    setFailed(false);
    setSelectedVersionId(null);
    setCanvasSelection(null);
    setAnnotationSelection(null);
    setRefinementSelection(null);

    void loadArtifactSnapshot(artifactId)
      .then((nextSnapshot) => {
        if (!active) return;
        setSnapshot(nextSnapshot);
      })
      .catch((error) => {
        console.error('[ArtifactPreview] Failed to load artifact snapshot', error);
        if (active) {
          setSnapshot(null);
          setFailed(true);
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [artifactId, loadAttempt]);

  const metadataArtifact =
    snapshot &&
    artifactId &&
    !loading &&
    !failed &&
    requestOwnerId === artifactId &&
    snapshot.artifact.artifactId === artifactId
      ? snapshot.artifact
      : null;
  const metadata = useArtifactMetadataReads(metadataArtifact, metadataRevision, selectedVersionId);
  const intendResourceRecovery = useArtifactRecoveryFocus(
    JSON.stringify([metadata.owner, selectedVersionId, activeInspectorTab]),
    metadata.resources.status,
    metadata.resources.status === 'failed' ? resourcesFailureRef : previewReturnRef
  );
  const retryResources = () => {
    intendResourceRecovery();
    metadata.retry('resources');
  };
  const selectedVersion = useMemo(
    () =>
      metadata.versions.value.find((version) => version.versionId === selectedVersionId) ??
      (pinnedVersion && pinnedVersion.artifactId === artifactId && pinnedVersion.versionId === selectedVersionId
        ? pinnedVersion
        : null),
    [artifactId, metadata.versions.value, pinnedVersion, selectedVersionId]
  );
  const annotationVersionId = selectedVersionId ?? snapshot?.artifact.versionId ?? null;

  useEffect(() => {
    if (!snapshot || !annotationVersionId) {
      setPreviewAnnotations([]);
      return;
    }
    let active = true;
    void loadSynonBiomedArtifactAnnotations(snapshot.artifact.artifactId, annotationVersionId)
      .then((collection) => {
        if (active) setPreviewAnnotations(collection.annotations);
      })
      .catch(() => {
        if (active) setPreviewAnnotations([]);
      });
    return () => {
      active = false;
    };
  }, [annotationRevision, annotationVersionId, snapshot]);

  const currentLoading = Boolean(artifactId) && (loading || requestOwnerId !== artifactId);
  if (currentLoading || !snapshot || failed) {
    return (
      <ArtifactPageState
        state={currentLoading ? 'loading' : failed ? 'failed' : 'missing'}
        onRetry={() => {
          if (currentLoading || !artifactId) return;
          retryFocusIntent.current = true;
          setLoading(true);
          setLoadAttempt((attempt) => attempt + 1);
        }}
      />
    );
  }

  const { artifact } = snapshot;
  const versions = metadata.versions.value;
  const folders = metadata.folders.value;
  const latexResourceUrls = buildLatexArtifactResourceUrls(metadata.resources.value);
  const contentUrl = selectedVersionId
    ? getSynonBiomedArtifactVersionContentUrl(selectedVersionId)
    : getSynonBiomedArtifactContentUrl(artifact.artifactId);
  const displayMetadata =
    selectedVersion ?? (!selectedVersionId || selectedVersionId === artifact.versionId ? artifact : null);
  const receiptVersion =
    savedVersion?.artifactId === artifactId && savedVersion.versionId === selectedVersionId
      ? savedVersion.versionNumber
      : null;
  const displayVersion = displayMetadata?.versionNumber ?? receiptVersion;
  const displayContentType = displayMetadata?.contentType ?? null;
  const displaySize = displayMetadata?.sizeBytes ?? null;
  const activeVersionId = selectedVersionId ?? artifact.versionId;
  const previewNeedsResources =
    resolveSynonBiomedArtifactPreviewPlan({ filename: artifact.filename, contentType: displayContentType }).type ===
    'latex';
  const folderStateLabel = t('preview.artifact.details.folder');
  const folderActionHint =
    metadata.folders.status === 'ready'
      ? undefined
      : t(`preview.artifact.metadata.${metadata.folders.status}`, { section: folderStateLabel });

  const handleVersionApplied = async (result: SynonBiomedAppliedArtifactEdit) => {
    // A saved version is immutable; displaying it must not override a newer
    // route or an explicit historical-version choice made during the read.
    if (
      viewOwner.current.artifactId !== artifactId ||
      viewOwner.current.selectedVersionId !== selectedVersionId ||
      result.artifactId !== artifactId
    )
      return;
    const refreshRevision = ++versionRefreshRevision.current;
    const nextSnapshot = await loadArtifactSnapshot(result.artifactId);
    if (refreshRevision !== versionRefreshRevision.current) return;
    setSnapshot(nextSnapshot);
    setSavedVersion(result);
    setMetadataRevision((revision) => revision + 1);
    setSelectedVersionId(result.versionId);
    setCanvasSelection(null);
    setAnnotationRevision((revision) => revision + 1);
    messageApi.success(t('preview.artifact.versionCreated', { version: result.versionNumber }));
  };

  return (
    <div className='artifact-page size-full overflow-hidden bg-1'>
      {messageContextHolder}
      <div className='artifact-shell size-full max-w-1600px mx-auto flex flex-col'>
        <header className='artifact-header min-h-68px px-18px md:px-28px py-12px flex flex-wrap items-center gap-12px border-b border-solid border-[var(--color-border-2)]'>
          <Button
            type='text'
            aria-label={t('preview.artifact.backToProject')}
            title={t('preview.artifact.backToProject')}
            icon={<Left theme='outline' size={17} />}
            onClick={() =>
              artifact.projectId
                ? void navigate(`/projects/${encodeURIComponent(artifact.projectId)}`)
                : void navigate(-1)
            }
          />
          <FileText theme='outline' size={21} className='shrink-0 text-t-secondary' />
          <div className='min-w-180px flex-1'>
            <h1
              ref={pageHeadingRef}
              tabIndex={-1}
              className='m-0 truncate text-17px leading-24px font-[600] text-t-primary'
            >
              {artifact.filename}
            </h1>
            <div className='mt-2px text-11px text-t-tertiary'>
              {displayContentType ?? t('preview.artifact.unknownType')} · {formatBytes(displaySize)} ·{' '}
              {displayVersion
                ? t('preview.artifact.versionLabel', { version: displayVersion })
                : t('preview.artifact.metadata.unknownVersion')}
            </div>
          </div>
          <div className='flex items-center gap-6px'>
            {artifact.projectId && (artifact.frameId || artifact.rootFrameId) && (
              <Button icon={<Notes theme='outline' size={15} />} onClick={() => setNotesVisible(true)}>
                {t('preview.artifact.notes')}
              </Button>
            )}
            <Button
              href={contentUrl}
              anchorProps={{ download: artifact.filename }}
              icon={<Download theme='outline' size={15} />}
            >
              {t('preview.artifact.download')}
            </Button>
            <Button
              aria-label={t('preview.artifact.copyFile')}
              disabled={metadata.folders.status !== 'ready'}
              title={folderActionHint}
              icon={<Copy theme='outline' size={15} />}
              onClick={() => setFileAction('copy')}
            >
              {t('preview.artifact.copy')}
            </Button>
            <Button
              aria-label={t('preview.artifact.moveToFolder')}
              disabled={metadata.folders.status !== 'ready'}
              title={folderActionHint}
              icon={<FolderOpen theme='outline' size={15} />}
              onClick={() => setFileAction('move')}
            >
              {t('preview.artifact.move')}
            </Button>
            <Button
              aria-label={t('preview.artifact.exportToCloud')}
              icon={<Upload theme='outline' size={15} />}
              onClick={() => setFileAction('export')}
            >
              {t('preview.artifact.export')}
            </Button>
          </div>
        </header>

        <div className='min-h-0 flex-1 grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_360px]'>
          <section
            ref={previewReturnRef}
            tabIndex={-1}
            className='artifact-preview-pane min-h-360px overflow-hidden bg-fill-1'
            aria-label={t('preview.artifact.filePreview')}
          >
            <React.Suspense fallback={<PreviewLoadingState label={t('preview.artifact.preparingDataViewer')} />}>
              {previewNeedsResources && metadata.resources.status !== 'ready' ? (
                <ArtifactMetadataState
                  state={metadata.resources}
                  label={t('preview.artifact.metadata.resources')}
                  owner={metadata.owner}
                  onRetry={retryResources}
                  regionRef={resourcesFailureRef}
                />
              ) : (
                <ArtifactContent
                  artifactId={artifact.artifactId}
                  versionId={activeVersionId}
                  rootFrameId={artifact.rootFrameId ?? artifact.frameId}
                  filename={artifact.filename}
                  contentType={displayContentType}
                  contentUrl={contentUrl}
                  annotations={previewAnnotations}
                  resourceUrls={latexResourceUrls}
                  onSelectionChange={setCanvasSelection}
                  onAnnotationClick={() => setActiveInspectorTab('annotations')}
                />
              )}
            </React.Suspense>
          </section>
          <aside className='artifact-inspector min-h-0 overflow-y-auto border-t xl:border-t-0 xl:border-l border-solid border-[var(--color-border-2)] bg-1'>
            <Tabs
              activeTab={activeInspectorTab}
              onChange={setActiveInspectorTab}
              className='artifact-inspector-tabs'
              aria-label={`${t('preview.artifact.tabs.details')} · ${artifact.filename}`}
            >
              <Tabs.TabPane key='details' title={t('preview.artifact.tabs.details')}>
                <ArtifactDetails
                  artifact={artifact}
                  displayMetadata={displayMetadata}
                  displayVersion={displayVersion}
                  versionId={activeVersionId}
                  folders={metadata.folders}
                  owner={metadata.owner}
                  onRetryFolders={() => metadata.retry('folders')}
                />
                {metadata.resources.status === 'failed' && !previewNeedsResources && (
                  <ArtifactMetadataState
                    state={metadata.resources}
                    label={t('preview.artifact.metadata.resources')}
                    owner={metadata.owner}
                    onRetry={retryResources}
                    regionRef={resourcesFailureRef}
                  />
                )}
              </Tabs.TabPane>
              <Tabs.TabPane key='versions' title={t('preview.artifact.tabs.versions')}>
                <ArtifactMetadataState
                  state={metadata.versions}
                  label={t('preview.artifact.tabs.versions')}
                  owner={metadata.owner}
                  hasCachedValue={versions.length > 0}
                  onRetry={() => metadata.retry('versions')}
                >
                  <ArtifactVersions
                    versions={versions}
                    selectedVersionId={selectedVersionId ?? artifact.versionId}
                    onSelect={(versionId) => {
                      setPinnedVersion(versions.find((version) => version.versionId === versionId) ?? null);
                      setSelectedVersionId(versionId);
                      setCanvasSelection(null);
                      setAnnotationSelection(null);
                      setRefinementSelection(null);
                    }}
                  />
                </ArtifactMetadataState>
              </Tabs.TabPane>
              <Tabs.TabPane key='annotations' title={t('preview.artifact.tabs.annotations')}>
                {activeVersionId ? (
                  <ArtifactAnnotationsPanel
                    artifactId={artifact.artifactId}
                    versionId={activeVersionId}
                    refreshToken={annotationRevision}
                    onVersionApplied={handleVersionApplied}
                    onAnnotationsChange={setPreviewAnnotations}
                  />
                ) : (
                  <Empty description={t('preview.artifact.noAnnotatableVersion')} />
                )}
              </Tabs.TabPane>
              <Tabs.TabPane key='verification' title={t('preview.artifact.tabs.verification')}>
                {activeVersionId ? (
                  <ArtifactVerificationPanel
                    versionId={activeVersionId}
                    rootFrameId={artifact.rootFrameId ?? artifact.frameId}
                  />
                ) : (
                  <Empty description={t('preview.artifact.noVerificationRecord')} />
                )}
              </Tabs.TabPane>
              <Tabs.TabPane key='lineage' title={t('preview.artifact.tabs.lineage')}>
                <ArtifactMetadataState
                  state={metadata.lineage}
                  label={t('preview.artifact.tabs.lineage')}
                  owner={metadata.lineageOwner}
                  hasCachedValue={metadata.lineage.value !== null}
                  onRetry={() => metadata.retry('lineage')}
                >
                  <ArtifactLineage lineage={metadata.lineage.value} />
                </ArtifactMetadataState>
              </Tabs.TabPane>
            </Tabs>
          </aside>
        </div>
      </div>

      {canvasSelection && activeVersionId && (
        <div
          className='fixed z-9998 flex items-center gap-4px border border-solid border-[var(--color-border-2)] bg-1 p-4px shadow-lg'
          style={selectionToolbarStyle(canvasSelection)}
          role='toolbar'
          aria-label={t('preview.artifact.selectionActions')}
          onMouseDown={(event) => event.preventDefault()}
        >
          <Button
            type='text'
            size='small'
            icon={<Comment theme='outline' size={14} />}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              // The transient toolbar is removed on activation. Capture the
              // persistent source region for native dialog focus restoration.
              previewReturnRef.current?.focus({ preventScroll: true });
              setAnnotationSelection(canvasSelection);
              setCanvasSelection(null);
            }}
          >
            {t('preview.artifact.annotate')}
          </Button>
          {canvasSelection.type === 'text_selection' && (
            <Button
              type='text'
              size='small'
              icon={<Magic theme='outline' size={14} />}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                previewReturnRef.current?.focus({ preventScroll: true });
                setRefinementSelection(canvasSelection);
                setCanvasSelection(null);
              }}
            >
              {t('preview.artifact.refine')}
            </Button>
          )}
        </div>
      )}

      {annotationSelection && activeVersionId && (
        <ArtifactSelectionAnnotationModal
          artifactId={artifact.artifactId}
          versionId={activeVersionId}
          selection={annotationSelection}
          onCancel={() => setAnnotationSelection(null)}
          onCreated={(annotation) => {
            setAnnotationSelection(null);
            setActiveInspectorTab('annotations');
            setPreviewAnnotations((current) => [...current.filter((item) => item.id !== annotation.id), annotation]);
            setAnnotationRevision((revision) => revision + 1);
            messageApi.success(t('preview.artifact.selectionAnnotationAdded'));
          }}
        />
      )}

      {refinementSelection && activeVersionId && (
        <ArtifactEditRefinementPanel
          artifactId={artifact.artifactId}
          versionId={activeVersionId}
          selectedText={refinementSelection.text}
          initialInstruction=''
          onClose={() => setRefinementSelection(null)}
          onApplied={handleVersionApplied}
        />
      )}

      {fileAction && (
        <ArtifactFileActionModal
          key={`${artifact.artifactId}:${fileAction}`}
          action={fileAction}
          artifact={artifact}
          folders={folders}
          onClose={() => setFileAction(null)}
          onCompleted={(result) => {
            if (viewOwner.current.artifactId !== artifact.artifactId) return;
            setFileAction(null);
            if (fileAction === 'move') {
              setSnapshot((current) =>
                current?.artifact.artifactId === artifact.artifactId
                  ? { ...current, artifact: { ...current.artifact, folderId: result.folderId } }
                  : current
              );
            }
            messageApi.success(
              t(
                `preview.artifact.${fileAction === 'copy' ? 'copySucceeded' : fileAction === 'move' ? 'moveSucceeded' : 'exportSucceeded'}`
              )
            );
            if (fileAction === 'copy' && result.copiedArtifactId)
              void navigate(`/artifacts/${encodeURIComponent(result.copiedArtifactId)}`);
          }}
        />
      )}
      {artifact.projectId && (artifact.frameId || artifact.rootFrameId) && (
        <SynonBiomedNotesModal
          visible={notesVisible}
          target={{
            projectId: artifact.projectId,
            targetType: 'artifact',
            targetFrameId: artifact.frameId || artifact.rootFrameId || '',
            targetArtifactId: artifact.artifactId,
          }}
          onClose={() => setNotesVisible(false)}
        />
      )}
    </div>
  );
};

async function loadArtifactSnapshot(artifactId: string): Promise<ArtifactSnapshot> {
  const artifact = await loadSynonBiomedArtifact(artifactId);
  return { artifact };
}

const ArtifactVersions: React.FC<{
  versions: SynonBiomedArtifactVersion[];
  selectedVersionId: string | null;
  onSelect: (versionId: string) => void;
}> = ({ versions, selectedVersionId, onSelect }) => {
  const { t, i18n } = useTranslation();
  if (versions.length === 0) return <Empty description={t('preview.artifact.noVersions')} />;
  return (
    <div className='border-t border-solid border-[var(--color-border-2)]'>
      {versions
        .toSorted((left, right) => right.versionNumber - left.versionNumber)
        .map((version) => (
          <button
            type='button'
            key={version.versionId}
            className={`w-full px-16px py-12px border-x-0 border-t-0 border-b border-solid border-[var(--color-border-2)] bg-transparent text-left hover:bg-fill-1 ${
              selectedVersionId === version.versionId ? 'bg-fill-2' : ''
            }`}
            onClick={() => onSelect(version.versionId)}
          >
            <div className='flex items-center justify-between gap-10px'>
              <span className='text-13px font-[600] text-t-primary'>
                {t('preview.artifact.versionLabel', {
                  version: version.versionNumber,
                })}
              </span>
              <span className='text-11px text-t-tertiary'>{formatBytes(version.sizeBytes)}</span>
            </div>
            <div className='mt-4px text-11px text-t-tertiary'>
              {formatDate(version.createdAt, i18n.language, t('preview.artifact.unknown'))}
            </div>
            {version.agentName && <div className='mt-3px text-11px text-t-secondary'>{version.agentName}</div>}
          </button>
        ))}
    </div>
  );
};

const ArtifactLineage: React.FC<{
  lineage: SynonBiomedArtifactLineage | null;
}> = ({ lineage }) => {
  const { t } = useTranslation();
  if (!lineage) return <Empty description={t('preview.artifact.lineage.empty')} />;
  if (lineage.pending)
    return <div className='px-16px pb-18px text-12px text-t-secondary'>{t('preview.artifact.lineage.pending')}</div>;
  return (
    <div className='px-16px pb-18px flex flex-col gap-16px text-12px'>
      <LineageSection
        title={t('preview.artifact.lineage.description')}
        value={lineage.codeDescription ?? t('preview.artifact.lineage.noDescription')}
      />
      {lineage.code && <LineageSection title={t('preview.artifact.lineage.code')} value={lineage.code} code />}
      <LineageFlags lineage={lineage} />
      {lineage.environmentSnapshot != null && (
        <LineageSection
          title={t('preview.artifact.lineage.environment')}
          value={formatStructuredValue(lineage.environmentSnapshot)}
          code
        />
      )}
      {lineage.dependencyMappings != null && (
        <LineageSection
          title={t('preview.artifact.lineage.dependencies')}
          value={formatStructuredValue(lineage.dependencyMappings)}
          code
        />
      )}
    </div>
  );
};

const LineageSection: React.FC<{
  title: string;
  value: string;
  code?: boolean;
}> = ({ title, value, code }) => (
  <section>
    <h3 className='m-0 mb-6px text-12px font-[600] text-t-primary'>{title}</h3>
    {code ? (
      <pre className='m-0 max-h-240px overflow-auto whitespace-pre-wrap break-all text-11px leading-18px text-t-secondary font-mono bg-fill-1 px-10px py-8px'>
        {value}
      </pre>
    ) : (
      <p className='m-0 whitespace-pre-wrap break-words leading-19px text-t-secondary'>{value}</p>
    )}
  </section>
);

const LineageFlags: React.FC<{ lineage: SynonBiomedArtifactLineage }> = ({ lineage }) => {
  const { t } = useTranslation();
  return (
    <div className='grid grid-cols-3 border border-solid border-[var(--color-border-2)]'>
      <Flag label={t('preview.artifact.lineage.messages')} active={lineage.hasMessages} />
      <Flag label={t('preview.artifact.lineage.environmentShort')} active={lineage.hasEnvironment} />
      <Flag label={t('preview.artifact.lineage.cellSources')} active={lineage.hasCellSources} />
    </div>
  );
};

const Flag: React.FC<{ label: string; active: boolean }> = ({ label, active }) => {
  const { t } = useTranslation();
  return (
    <div className='px-7px py-8px text-center border-r last:border-r-0 border-y-0 border-l-0 border-solid border-[var(--color-border-2)]'>
      <div className='text-11px text-t-tertiary'>{label}</div>
      <div className={`mt-2px text-11px ${active ? 'text-success-6' : 'text-t-tertiary'}`}>
        {active ? t('preview.artifact.lineage.recorded') : t('preview.artifact.lineage.none')}
      </div>
    </div>
  );
};

const ArtifactContent: React.FC<{
  artifactId: string;
  versionId: string;
  rootFrameId: string | null;
  filename: string;
  contentType: string | null;
  contentUrl: string;
  annotations: SynonBiomedArtifactAnnotation[];
  resourceUrls: Readonly<Record<string, string>>;
  onSelectionChange: (selection: SynonBiomedArtifactCanvasSelection | null) => void;
  onAnnotationClick: (annotation: SynonBiomedArtifactAnnotation) => void;
}> = ({
  artifactId,
  versionId,
  rootFrameId,
  filename,
  contentType,
  contentUrl,
  annotations,
  resourceUrls,
  onSelectionChange,
  onAnnotationClick,
}) => {
  const { t } = useTranslation();
  const plan = resolveSynonBiomedArtifactPreviewPlan({ filename, contentType });
  const isTiffArtifact =
    contentType === 'image/tiff' || filename.toLowerCase().endsWith('.tif') || filename.toLowerCase().endsWith('.tiff');

  if (contentType?.startsWith('image/') && !isTiffArtifact) {
    return (
      <SynonBiomedImageArtifactViewer
        filename={filename}
        contentUrl={contentUrl}
        annotations={annotations}
        onSelectionChange={onSelectionChange}
        onAnnotationClick={onAnnotationClick}
      />
    );
  }

  if (contentType === 'application/pdf') {
    return (
      <SynonBiomedPdfArtifactViewer
        filename={filename}
        contentUrl={contentUrl}
        annotations={annotations}
        onSelectionChange={onSelectionChange}
        onAnnotationClick={onAnnotationClick}
      />
    );
  }

  if (contentType?.startsWith('video/')) {
    return <VideoPreview url={contentUrl} filename={filename} />;
  }

  if (contentType?.startsWith('audio/')) {
    return <AudioPreview url={contentUrl} filename={filename} />;
  }

  if (plan.type === 'video') {
    return <VideoPreview url={contentUrl} filename={filename} />;
  }

  if (plan.type === 'archive') {
    const archiveContentUrl = `${getSynonBiomedArtifactContentUrl(artifactId)}/versions/${encodeURIComponent(versionId)}`;
    return <ArchiveArtifactPreview filename={filename} contentUrl={archiveContentUrl} />;
  }

  if (plan.type === 'audio') {
    return <AudioPreview url={contentUrl} filename={filename} />;
  }

  if (plan.type === 'word') {
    return <OfficeDocPreview artifactId={artifactId} versionId={versionId} />;
  }

  if (plan.type === 'ppt') {
    return <PptViewer artifactId={artifactId} versionId={versionId} />;
  }

  if (plan.type === 'table' && ['.xlsx', '.xlsm'].some((extension) => filename.toLowerCase().endsWith(extension))) {
    return <ExcelPreview artifactId={artifactId} versionId={versionId} />;
  }

  if (plan.type === 'structure') {
    // Rendering and derived snapshots must share the displayed version, including
    // the initial current-version view and an explicitly selected history entry.
    const structureContentUrl = `${getSynonBiomedArtifactContentUrl(artifactId)}/versions/${encodeURIComponent(versionId)}`;
    return (
      <SynonBiomedStructureViewer
        filename={filename}
        contentUrl={structureContentUrl}
        rootFrameId={rootFrameId ?? undefined}
      />
    );
  }

  if (plan.type === 'molecule') {
    const interactiveFormat = filename.toLowerCase().match(/\.(ket|rxn)$/)?.[1] as 'ket' | 'rxn' | undefined;
    if (interactiveFormat) {
      return (
        <SynonBiomedMcpAppArtifactViewer
          filename={filename}
          contentUrl={contentUrl}
          contentParam={interactiveFormat}
          rootFrameId={rootFrameId ?? undefined}
          frameId={rootFrameId ?? undefined}
          artifactId={artifactId}
        />
      );
    }
    return <SynonBiomedMoleculeViewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'table') {
    return <SynonBiomedTableViewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'msa') {
    return <SynonBiomedMsaViewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'genome') {
    return <SynonBiomedGenomeViewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'sequence') {
    return <SynonBiomedSequenceViewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'notebook') {
    return <SynonBiomedNotebookViewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'hdf5') {
    return <SynonBiomedHdf5Viewer filename={filename} contentUrl={contentUrl} />;
  }

  if (plan.type === 'unsupported') {
    return <UnsupportedPreview filename={filename} contentType={contentType} downloadUrl={contentUrl} />;
  }

  if (plan.type === 'latex') {
    return (
      <SynonBiomedLatexArtifactViewer
        filename={filename}
        contentUrl={contentUrl}
        annotations={annotations}
        resourceUrls={resourceUrls}
        onSelectionChange={onSelectionChange}
        onAnnotationClick={onAnnotationClick}
      />
    );
  }

  if (plan.fetchText && (plan.type === 'code' || plan.type === 'markdown' || plan.type === 'html')) {
    return (
      <SynonBiomedTextArtifactViewer
        filename={filename}
        contentUrl={contentUrl}
        kind={plan.type}
        language={plan.language}
        annotations={annotations}
        onSelectionChange={onSelectionChange}
        onAnnotationClick={onAnnotationClick}
      />
    );
  }

  return (
    <div className='size-full min-h-360px flex-center flex-col gap-12px text-t-secondary'>
      <FileText theme='outline' size={30} />
      <a href={contentUrl} target='_blank' rel='noreferrer' className='text-13px text-[rgb(var(--primary-6))]'>
        {t('preview.artifact.openOriginal')}
      </a>
    </div>
  );
};

const LATEX_IMAGE_EXTENSIONS = new Set(['avif', 'gif', 'jpeg', 'jpg', 'pdf', 'png', 'svg', 'webp']);

function buildLatexArtifactResourceUrls(artifacts: SynonBiomedProjectArtifact[]): Readonly<Record<string, string>> {
  const resources: Record<string, string> = {};
  for (const artifact of artifacts) {
    const extension = artifact.filename.split('.').at(-1)?.toLowerCase() ?? '';
    if (!artifact.contentType?.startsWith('image/') && !LATEX_IMAGE_EXTENSIONS.has(extension)) continue;
    const contentUrl = getSynonBiomedArtifactContentUrl(artifact.artifactId);
    for (const value of [artifact.filename, artifact.filePath]) {
      if (!value) continue;
      const normalized = value.trim().replaceAll('\\', '/').replace(/^\.\//, '').toLowerCase();
      if (!normalized || normalized.includes('../')) continue;
      const basename = normalized.split('/').at(-1) ?? normalized;
      resources[normalized] = contentUrl;
      resources[basename] = contentUrl;
      resources[basename.replace(/\.[a-z0-9]+$/i, '')] = contentUrl;
    }
  }
  return resources;
}

function selectionToolbarStyle(selection: SynonBiomedArtifactCanvasSelection): React.CSSProperties {
  const left = Math.max(68, Math.min(selection.x, window.innerWidth - 68));
  const top = Math.max(12, Math.min(selection.y + 8, window.innerHeight - 48));
  return { left, top, transform: 'translateX(-50%)' };
}

function formatStructuredValue(value: unknown): string {
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export default ArtifactPreview;

function inspectorTabFromSearch(search: string): string {
  const requested = new URLSearchParams(search).get('tab');
  return requested === 'versions' ||
    requested === 'annotations' ||
    requested === 'verification' ||
    requested === 'lineage'
    ? requested
    : 'details';
}
