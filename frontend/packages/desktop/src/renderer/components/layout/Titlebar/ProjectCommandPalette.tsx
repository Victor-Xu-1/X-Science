import { insertArtifactReferenceIntoActiveComposer } from '@/renderer/components/chat/SendBox/composerReferenceBridge';
import {
  loadSynonBiomedProjectArtifacts,
  loadSynonBiomedProjectBenches,
  loadSynonBiomedProjects,
  type SynonBiomedProject,
} from '@/renderer/services/synonBiomedGateway';
import { Empty, Input, Spin } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { FileCode, FolderOpen, MessageOne, Plus, Search } from '@icon-park/react';
import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';

type PaletteItem = {
  id: string;
  kind: 'command' | 'artifact' | 'session';
  label: string;
  detail: string;
  projectId?: string;
  artifactId?: string;
  versionId?: string | null;
  frameId?: string;
  action?: 'new-session' | 'project';
};

const ProjectCommandPalette: React.FC = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [visible, setVisible] = useState(false);
  const [query, setQuery] = useState('');
  const [items, setItems] = useState<PaletteItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const [partial, setPartial] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const generation = useRef(0);
  const resultsId = useId();
  const resultsRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.altKey) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setVisible((current) => !current);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  const load = useCallback(async () => {
    const current = ++generation.current;
    setLoading(true);
    setError(false);
    setPartial(false);
    setItems([]);
    setActiveIndex(0);
    try {
      const projects = await loadSynonBiomedProjects();
      const projectResults = await Promise.allSettled(
        projects.map(async (project) => {
          const [artifacts, benches] = await Promise.allSettled([
            loadSynonBiomedProjectArtifacts(project.projectId),
            loadSynonBiomedProjectBenches(project.projectId),
          ]);
          return {
            project,
            artifacts: artifacts.status === 'fulfilled' ? artifacts.value : [],
            benches: benches.status === 'fulfilled' ? benches.value : [],
            partial: artifacts.status === 'rejected' || benches.status === 'rejected',
          };
        })
      );
      const projectData = projectResults.map((result, index) =>
        result.status === 'fulfilled'
          ? result.value
          : { project: projects[index], artifacts: [], benches: [], partial: true }
      );
      if (generation.current !== current) return;
      setPartial(projectData.some((result) => result.partial));
      setItems([
        {
          id: 'command:new-session',
          kind: 'command',
          label: t('conversation.projectCommandPalette.newTask'),
          detail: t('conversation.projectCommandPalette.newTaskDescription'),
          action: 'new-session',
        },
        ...projectData.flatMap(({ project, artifacts, benches }) =>
          toPaletteItems(project, artifacts, benches, {
            openProject: t('conversation.projectCommandPalette.openProject'),
            session: t('conversation.projectCommandPalette.session'),
            file: t('conversation.projectCommandPalette.file'),
          })
        ),
      ]);
    } catch (loadError) {
      console.warn(
        '[ProjectCommandPalette] Failed to load project commands:',
        redactErrorText(loadError instanceof Error ? loadError.message : String(loadError || 'unknown error'))
      );
      if (generation.current === current) {
        setItems([]);
        setError(true);
      }
    } finally {
      if (generation.current === current) setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    if (!visible) return;
    setQuery('');
    setActiveIndex(0);
    void load();
    return () => {
      generation.current += 1;
    };
  }, [load, visible]);

  const filtered = useMemo(() => {
    const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length) return items.slice(0, 40);
    return items
      .filter((item) => {
        const haystack = `${item.label} ${item.detail}`.toLocaleLowerCase();
        return terms.every((term) => haystack.includes(term));
      })
      .slice(0, 40);
  }, [items, query]);

  useEffect(() => {
    resultsRef.current
      ?.querySelector<HTMLElement>(`[data-palette-index='${activeIndex}']`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex]);

  const choose = (item: PaletteItem, mention = false) => {
    if (!visible || loading || error) return;
    if (mention && item.kind === 'artifact' && item.artifactId) {
      const inserted = insertArtifactReferenceIntoActiveComposer({
        filename: item.label,
        artifactId: item.artifactId,
        versionId: item.versionId ?? undefined,
      });
      if (inserted) setVisible(false);
      return;
    }
    if (item.action === 'new-session') navigate('/guid');
    else if (item.action === 'project' && item.projectId) navigate(`/projects/${encodeURIComponent(item.projectId)}`);
    else if (item.kind === 'session' && item.frameId) navigate(`/conversation/${encodeURIComponent(item.frameId)}`);
    else if (item.kind === 'artifact' && item.artifactId) navigate(`/artifacts/${encodeURIComponent(item.artifactId)}`);
    setVisible(false);
  };

  return (
    <Modal
      title={null}
      visible={visible}
      footer={null}
      onCancel={() => setVisible(false)}
      unmountOnExit
      simple
      style={{ width: 680, maxWidth: 'calc(100vw - 24px)' }}
      modalRender={(node) =>
        React.isValidElement(node)
          ? React.cloneElement(node as React.ReactElement<React.HTMLAttributes<HTMLDivElement>>, {
              'aria-label': t('conversation.projectCommandPalette.search'),
            })
          : node
      }
    >
      <div data-testid='project-command-palette' className='project-command-palette'>
        <Input
          autoFocus
          size='large'
          prefix={<Search />}
          value={query}
          onChange={(value) => {
            setQuery(value);
            setActiveIndex(0);
          }}
          placeholder={t('conversation.projectCommandPalette.placeholder')}
          aria-label={t('conversation.projectCommandPalette.search')}
          role='combobox'
          aria-expanded={visible}
          aria-controls={resultsId}
          aria-autocomplete='list'
          aria-activedescendant={
            !loading && !error && filtered[activeIndex] ? `${resultsId}-${activeIndex}` : undefined
          }
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === 'Escape') {
              event.preventDefault();
              setVisible(false);
              return;
            }
            if (event.key === 'Enter') event.preventDefault();
            if (loading || error || !filtered.length) return;
            if (event.key === 'ArrowDown') {
              event.preventDefault();
              setActiveIndex((index) => Math.min(index + 1, Math.max(filtered.length - 1, 0)));
            } else if (event.key === 'ArrowUp') {
              event.preventDefault();
              setActiveIndex((index) => Math.max(index - 1, 0));
            } else if (event.key === 'Home' || event.key === 'End') {
              event.preventDefault();
              setActiveIndex(event.key === 'Home' ? 0 : filtered.length - 1);
            } else if (event.key === 'Enter' && filtered[activeIndex]) {
              event.preventDefault();
              choose(filtered[activeIndex], event.shiftKey);
            }
          }}
        />
        {partial && !loading && (
          <div role='status' className='mt-8px flex items-center gap-8px text-t-secondary'>
            <span>{t('conversation.projectCommandPalette.partialUnavailable')}</span>
            <button
              type='button'
              className='shrink-0 border-none bg-transparent text-t-primary cursor-pointer rd-6px hover:bg-3 px-8px py-4px'
              onClick={() => void load()}
            >
              {t('common.retry')}
            </button>
          </div>
        )}
        <div className='mt-8px max-h-440px overflow-y-auto'>
          {loading ? (
            <div role='status' className='h-160px flex items-center justify-center gap-8px'>
              <Spin />
              <span>{t('common.loading')}</span>
            </div>
          ) : error ? (
            <button
              type='button'
              className='h-120px w-full border-none bg-transparent text-t-secondary cursor-pointer'
              onClick={() => void load()}
            >
              {t('conversation.projectCommandPalette.loadFailed')}
            </button>
          ) : filtered.length === 0 ? (
            <Empty description={t('conversation.projectCommandPalette.empty')} />
          ) : null}
          <div
            id={resultsId}
            ref={resultsRef}
            role='listbox'
            aria-label={t('conversation.projectCommandPalette.search')}
            aria-busy={loading}
          >
            {!loading &&
              !error &&
              filtered.map((item, index) => (
                <button
                  type='button'
                  role='option'
                  id={`${resultsId}-${index}`}
                  data-palette-index={index}
                  aria-selected={activeIndex === index}
                  tabIndex={-1}
                  key={item.id}
                  className={`w-full min-h-52px px-12px py-8px border-none rd-6px flex items-center gap-10px text-left cursor-pointer ${index === activeIndex ? 'bg-3' : 'bg-transparent hover:bg-2'}`}
                  onMouseEnter={() => setActiveIndex(index)}
                  onClick={() => choose(item)}
                >
                  <span className='text-t-secondary leading-none'>
                    {item.kind === 'artifact' ? (
                      <FileCode />
                    ) : item.kind === 'session' ? (
                      <MessageOne />
                    ) : item.action === 'new-session' ? (
                      <Plus />
                    ) : (
                      <FolderOpen />
                    )}
                  </span>
                  <span className='min-w-0 flex-1'>
                    <span className='block truncate text-14px text-t-primary'>{item.label}</span>
                    <span className='block truncate text-12px text-t-secondary'>{item.detail}</span>
                  </span>
                  {item.kind === 'artifact' && (
                    <span className='text-11px text-t-secondary'>
                      {t('conversation.projectCommandPalette.referenceShortcut')}
                    </span>
                  )}
                </button>
              ))}
          </div>
        </div>
      </div>
    </Modal>
  );
};

function toPaletteItems(
  project: SynonBiomedProject,
  artifacts: Awaited<ReturnType<typeof loadSynonBiomedProjectArtifacts>>,
  benches: Awaited<ReturnType<typeof loadSynonBiomedProjectBenches>>,
  labels: { openProject: string; session: string; file: string }
): PaletteItem[] {
  return [
    {
      id: `project:${project.projectId}`,
      kind: 'command' as const,
      label: project.name,
      detail: labels.openProject,
      projectId: project.projectId,
      action: 'project' as const,
    },
    ...benches.map((bench) => ({
      id: `session:${bench.frameId}`,
      kind: 'session' as const,
      label: bench.name,
      detail: `${project.name} · ${labels.session}`,
      projectId: project.projectId,
      frameId: bench.frameId,
    })),
    ...artifacts.map((artifact) => ({
      id: `artifact:${artifact.artifactId}`,
      kind: 'artifact' as const,
      label: artifact.filename,
      detail: `${project.name} · ${labels.file}`,
      projectId: project.projectId,
      artifactId: artifact.artifactId,
      versionId: artifact.versionId,
    })),
  ];
}

export default ProjectCommandPalette;
