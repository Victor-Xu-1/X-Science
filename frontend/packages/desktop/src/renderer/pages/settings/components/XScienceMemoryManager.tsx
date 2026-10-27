import { Button, Input, Message, Select, Spin, Switch, Tag, Tooltip } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { Check, Copy, Delete, Edit, Plus, Refresh } from '@icon-park/react';
import React, { useCallback, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import {
  clearXScienceMemories,
  createXScienceMemory,
  deleteXScienceMemory,
  deleteXScienceMemoryCategory,
  loadXScienceAutoMemoryEnabled,
  loadXScienceMemoryContext,
  loadXScienceMemoryEnabled,
  loadXScienceSessionMemories,
  setXScienceAutoMemoryEnabled,
  setXScienceMemoryEnabled,
  setXScienceProjectMemoryEnabled,
  updateXScienceMemory,
  updateXScienceMemoryCategory,
  type XScienceMemoryCategory,
  type XScienceMemoryContext,
  type XScienceMemoryRow,
  type XScienceForkedViewRecovery,
  type XScienceMemoryOptions,
} from '@/renderer/services/xScienceMemory';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { loadXScienceProjects, type XScienceProject } from '@/renderer/services/xScienceGateway';
import { SettingsGeneratedIcon, type SettingsGeneratedIconId } from './SettingsGeneratedAsset';
import { useMemoryCategoryDraft } from './useMemoryCategoryDraft';
import { MemoryRecoveryPolicy } from './MemoryRecoveryPolicy';

type MemoryScope =
  | { kind: 'profile'; key: 'profile' }
  | { kind: 'entity'; key: string; entityKey: string }
  | { kind: 'category'; key: string; categoryId: string }
  | { kind: 'session'; key: string; frameId: string };

type MemoryLayer = 'global' | 'project';

const MAX_MEMORY_CATEGORIES = 10;

const EMPTY_CONTEXT: XScienceMemoryContext = {
  profileMarkdown: '',
  listingMarkdown: '',
  enabled: null,
  totalRows: 0,
  entities: [],
  sessions: [],
  categories: [],
};

export const XScienceMemoryManager: React.FC<{ onChanged?: () => void | Promise<void> }> = ({
  onChanged = () => undefined,
}) => {
  const { t } = useTranslation();
  const viewId = useId();
  const subscribeAccount = useCallback(
    (notify: () => void) => registerRendererAccountReset('memory-manager:' + viewId, notify),
    [viewId]
  );
  const account = useSyncExternalStore(subscribeAccount, getRendererAccountScopeToken, getRendererAccountScopeToken);
  const [context, setContext] = useState(EMPTY_CONTEXT);
  const [layer, setLayer] = useState<MemoryLayer>('global');
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(null);
  const [projects, setProjects] = useState<XScienceProject[]>([]);
  const [projectLoadError, setProjectLoadError] = useState('');
  const [enabled, setEnabled] = useState(false);
  const [autoEnabled, setAutoEnabled] = useState(false);
  const [projectEnabled, setProjectEnabled] = useState<boolean | null>(null);
  const [loading, setLoading] = useState(true);
  // A successful snapshot owns both context and preference: null is global,
  // undefined is not loaded, and a project id never stands for another project.
  const [loadedProjectId, setLoadedProjectId] = useState<string | null | undefined>(undefined);
  const [loadError, setLoadError] = useState('');
  const [localPending, setLocalPending] = useState(false);
  const [scope, setScope] = useState<MemoryScope>({ kind: 'profile', key: 'profile' });
  const [sessionRows, setSessionRows] = useState<XScienceMemoryRow[]>([]);
  const [sessionLoading, setSessionLoading] = useState(false);
  const [sessionError, setSessionError] = useState(false);
  const [sessionReloadToken, setSessionReloadToken] = useState(0);
  const [adding, setAdding] = useState(false);
  const [newMemory, setNewMemory] = useState('');
  const [editingMemoryId, setEditingMemoryId] = useState<string | null>(null);
  const [editingMemoryText, setEditingMemoryText] = useState('');
  const [categoryToDelete, setCategoryToDelete] = useState<XScienceMemoryCategory | null>(null);
  const [deleteCategoryFacts, setDeleteCategoryFacts] = useState(false);
  const loadGenerationRef = useRef(0);
  const [loadedAccount, setLoadedAccount] = useState<string | null>(null);
  const mutationRef = useRef<{ controller: AbortController; account: string } | null>(null);
  const loadControllerRef = useRef<{ controller: AbortController; projectId?: string } | null>(null);
  const hasLoaded = loadedProjectId !== undefined;
  const hasCurrentContext =
    hasLoaded &&
    loadedAccount === account &&
    loadedProjectId === (layer === 'project' ? selectedProjectId : null) &&
    (layer === 'global' || selectedProjectId !== null);
  const awaitingContext = !hasCurrentContext && (layer === 'global' || selectedProjectId !== null);

  const load = useCallback(
    async (projectId?: string, isCurrent: () => boolean = () => true): Promise<boolean> => {
      if (!isCurrent() || account !== getRendererAccountScopeToken()) return true;
      loadControllerRef.current?.controller.abort();
      const controller = new AbortController();
      loadControllerRef.current = { controller, projectId };
      const generation = ++loadGenerationRef.current;
      setLoading(true);
      setLoadError('');
      try {
        const [nextContext, nextEnabled, nextAutoEnabled] = await Promise.all([
          loadXScienceMemoryContext(projectId ? { projectId } : {}, {
            signal: controller.signal,
          }),
          projectId ? Promise.resolve(null) : loadXScienceMemoryEnabled({ signal: controller.signal }),
          projectId ? Promise.resolve(null) : loadXScienceAutoMemoryEnabled({ signal: controller.signal }),
        ]);
        if (
          generation !== loadGenerationRef.current ||
          controller.signal.aborted ||
          !isCurrent() ||
          account !== getRendererAccountScopeToken()
        )
          return true;
        setContext(nextContext);
        if (projectId) {
          setProjectEnabled(nextContext.enabled ?? true);
        } else {
          setEnabled(nextEnabled ?? false);
          setAutoEnabled(nextAutoEnabled ?? false);
          setProjectEnabled(null);
        }
        setLoadedProjectId(projectId ?? null);
        setLoadedAccount(account);
        return true;
      } catch (error) {
        if (
          controller.signal.aborted ||
          generation !== loadGenerationRef.current ||
          !isCurrent() ||
          account !== getRendererAccountScopeToken()
        )
          return true;
        console.error('Failed to load X-Science memory:', error);
        setLoadError(t('settings.memoryLoadError'));
        return false;
      } finally {
        if (generation === loadGenerationRef.current && account === getRendererAccountScopeToken()) setLoading(false);
      }
    },
    [account, t]
  );

  const loadProjects = useCallback(async () => {
    setProjectLoadError('');
    try {
      const nextProjects = await loadXScienceProjects();
      if (account === getRendererAccountScopeToken()) setProjects(nextProjects);
    } catch (error) {
      if (account !== getRendererAccountScopeToken()) return;
      console.error('Failed to load X-Science projects:', error);
      setProjects([]);
      setProjectLoadError(t('settings.memoryProjectLoadError'));
    }
  }, [account, t]);

  const categoryEditor = useMemoryCategoryDraft({
    onCommitted: async (delivery) => {
      if (!delivery.isCurrent()) return;
      let refreshFailed = !(await load(loadControllerRef.current?.projectId, delivery.isCurrent));
      if (!delivery.isCurrent()) return;
      if (delivery.claimChanged()) {
        try {
          await onChanged();
        } catch (error) {
          if (!delivery.isCurrent()) return;
          console.error('Failed to notify current memory consumers after category save:', error);
          refreshFailed = true;
        }
      }
      if (!delivery.isCurrent()) return;
      if (delivery.claimSaved()) {
        if (delivery.outcome === 'missing') Message.warning(t('settings.memoryCategorySavedMissing'));
        else Message.success(t('settings.memoryCategorySaved'));
      }
      if (refreshFailed) Message.warning(t('settings.memoryRefreshError'));
    },
    onFailed: (current, kind) => {
      if (!current()) return;
      if (kind === 'rejected') Message.error(t('settings.memoryMutationError'));
      else Message.warning(t('settings.memoryCategoryUncertain'));
    },
    onRefreshFailed: (current) => {
      if (current()) Message.warning(t('settings.memoryRefreshError'));
    },
  });
  const categoryDraft = categoryEditor.draft;
  const pending = localPending || categoryEditor.busy;

  useEffect(() => {
    setLocalPending(false);
    // A locale refresh must reload the same owned scope, not the global default.
    void load(loadControllerRef.current?.projectId);
    void loadProjects();
    return () => {
      loadGenerationRef.current += 1;
      loadControllerRef.current?.controller.abort();
      mutationRef.current?.controller.abort();
      mutationRef.current = null;
    };
  }, [load, loadProjects]);

  useEffect(() => {
    if (scope.kind !== 'session') {
      setSessionRows([]);
      setSessionLoading(false);
      setSessionError(false);
      return;
    }
    const controller = new AbortController();
    setSessionRows([]);
    setSessionLoading(true);
    setSessionError(false);
    void loadXScienceSessionMemories(scope.frameId, { signal: controller.signal })
      .then((rows) => {
        if (!controller.signal.aborted && account === getRendererAccountScopeToken()) setSessionRows(rows);
      })
      .catch((error) => {
        if (!controller.signal.aborted && account === getRendererAccountScopeToken()) {
          console.error('Failed to load X-Science session memory:', error);
          setSessionError(true);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted && account === getRendererAccountScopeToken()) setSessionLoading(false);
      });
    return () => {
      controller.abort();
    };
  }, [account, scope, sessionReloadToken]);

  const profile = useMemo(() => context.entities.find((entity) => entity.entityKey === 'profile'), [context.entities]);
  const selectedProject = useMemo(
    () => projects.find((project) => project.projectId === selectedProjectId) ?? null,
    [projects, selectedProjectId]
  );
  const selectedCategory = useMemo(
    () => (scope.kind === 'category' ? context.categories.find((category) => category.id === scope.categoryId) : null),
    [context.categories, scope]
  );
  const scopeValue =
    scope.kind === 'profile'
      ? 'profile'
      : scope.kind === 'category'
        ? 'category:' + scope.categoryId
        : scope.kind === 'entity'
          ? 'entity:' + scope.entityKey
          : 'session:' + scope.frameId;
  const projectScopeOptions = useMemo(() => {
    if (layer !== 'project' || !selectedProjectId) return [];
    const projectEntityKey = 'project:' + selectedProjectId;
    const projectEntity = context.entities.find((entity) => entity.entityKey === projectEntityKey);
    return [
      {
        value: 'entity:' + projectEntityKey,
        label: selectedProject?.name ?? projectEntity?.label ?? selectedProjectId,
        projectId: selectedProjectId,
      },
      ...context.entities
        .filter((entity) => entity.projectId === selectedProjectId && entity.entityKey !== projectEntityKey)
        .map((entity) => ({
          value: 'entity:' + entity.entityKey,
          label: entity.label,
          projectId: selectedProjectId,
        })),
      ...context.sessions
        .filter((session) => session.projectId === selectedProjectId)
        .map((session) => ({
          value: 'session:' + session.frameId,
          label: session.label,
          projectId: selectedProjectId,
        })),
    ];
  }, [context.entities, context.sessions, layer, selectedProject, selectedProjectId]);
  const categoryLimitReached = context.categories.length >= MAX_MEMORY_CATEGORIES;
  const rows = useMemo(() => {
    if (layer === 'project' && !selectedProjectId) return [];
    if (layer === 'project' && (scope.kind === 'profile' || scope.kind === 'category')) return [];
    if (layer === 'global' && (scope.kind === 'entity' || scope.kind === 'session')) return [];
    if (scope.kind === 'profile') return (profile?.rows ?? []).filter((row) => !row.categoryId);
    if (scope.kind === 'entity') {
      return context.entities.find((entity) => entity.entityKey === scope.entityKey)?.rows ?? [];
    }
    if (scope.kind === 'category') {
      const categoryRows =
        layer === 'global' ? (profile?.rows ?? []) : context.entities.flatMap((entity) => entity.rows);
      return categoryRows.filter((row) => row.categoryId === scope.categoryId);
    }
    return sessionRows;
  }, [context.entities, layer, profile?.rows, scope, selectedProjectId, sessionRows]);

  const selectedEntity =
    scope.kind === 'entity' ? context.entities.find((entity) => entity.entityKey === scope.entityKey) : null;
  const selectedSession =
    scope.kind === 'session' ? context.sessions.find((session) => session.frameId === scope.frameId) : null;

  useEffect(() => {
    if (!hasCurrentContext || loading || scope.kind === 'profile') return;
    const projectFallback =
      layer === 'project' &&
      scope.kind === 'entity' &&
      selectedProjectId !== null &&
      scope.entityKey === 'project:' + selectedProjectId;
    if (projectFallback) return;
    const scopeExists =
      scope.kind === 'entity'
        ? context.entities.some((entity) => entity.entityKey === scope.entityKey)
        : scope.kind === 'category'
          ? context.categories.some((category) => category.id === scope.categoryId)
          : context.sessions.some((session) => session.frameId === scope.frameId);
    if (!scopeExists) setScope({ kind: 'profile', key: 'profile' });
  }, [
    context.categories,
    context.entities,
    context.sessions,
    hasCurrentContext,
    layer,
    loading,
    scope,
    selectedProject,
    selectedProjectId,
  ]);
  const title =
    layer === 'global'
      ? scope.kind === 'category'
        ? selectedCategory?.name || t('settings.memoryCategory')
        : t('settings.memoryGlobalProfile')
      : scope.kind === 'session'
        ? selectedSession?.label || t('settings.memorySession')
        : scope.kind === 'entity'
          ? selectedEntity?.label || selectedProject?.name || t('settings.memoryProjectSelect')
          : selectedProject?.name || t('settings.memoryProjectSelect');
  const addTarget =
    layer === 'global'
      ? scope.kind === 'category' && selectedCategory
        ? { entity: 'profile', category: selectedCategory.name }
        : { entity: 'profile' }
      : scope.kind === 'entity'
        ? { entity: scope.entityKey }
        : selectedProject
          ? { entity: 'project:' + selectedProject.projectId }
          : null;

  const openGlobalLayer = useCallback(() => {
    setLayer('global');
    setSelectedProjectId(null);
    setProjectEnabled(null);
    setScope({ kind: 'profile', key: 'profile' });
    void load();
  }, [load]);

  const selectProject = useCallback(
    (projectId: string) => {
      const project = projects.find((candidate) => candidate.projectId === projectId);
      if (!project) return;
      setLayer('project');
      setSelectedProjectId(projectId);
      const entityKey = 'project:' + projectId;
      setScope({ kind: 'entity', key: 'entity:' + entityKey, entityKey });
      void load(projectId);
    },
    [load, projects]
  );

  const openProjectLayer = useCallback(() => {
    const projectId = selectedProjectId ?? projects[0]?.projectId;
    setLayer('project');
    if (projectId) {
      selectProject(projectId);
    } else {
      setSelectedProjectId(null);
      setProjectEnabled(null);
      setScope({ kind: 'profile', key: 'profile' });
    }
  }, [projects, selectedProjectId, selectProject]);

  const handleScopeChange = useCallback(
    (value: string) => {
      if (value === 'profile') {
        setScope({ kind: 'profile', key: 'profile' });
        return;
      }
      if (value.startsWith('category:')) {
        const categoryId = value.slice('category:'.length);
        setLayer('global');
        setScope({ kind: 'category', key: value, categoryId });
        return;
      }
      const option = projectScopeOptions.find((candidate) => candidate.value === value);
      if (!option) return;
      setLayer('project');
      setSelectedProjectId(option.projectId);
      if (value.startsWith('entity:')) {
        const entityKey = value.slice('entity:'.length);
        setScope({ kind: 'entity', key: value, entityKey });
      } else {
        const frameId = value.slice('session:'.length);
        setScope({ kind: 'session', key: value, frameId });
      }
    },
    [projectScopeOptions]
  );

  useEffect(() => {
    if (!hasLoaded || loading || layer !== 'project') return;
    if (selectedProjectId && projects.some((project) => project.projectId === selectedProjectId)) return;
    const fallback = projects[0];
    if (fallback) {
      selectProject(fallback.projectId);
    } else {
      setSelectedProjectId(null);
      setScope({ kind: 'profile', key: 'profile' });
    }
  }, [hasLoaded, layer, loading, projects, selectedProjectId, selectProject]);

  const runMutation = useCallback(
    async (
      operation: (options: XScienceMemoryOptions) => Promise<unknown>,
      successMessage?: string
    ): Promise<boolean> => {
      if (!hasCurrentContext || mutationRef.current || account !== getRendererAccountScopeToken()) return false;
      const flight = { controller: new AbortController(), account };
      mutationRef.current = flight;
      const current = () =>
        mutationRef.current === flight &&
        !flight.controller.signal.aborted &&
        account === getRendererAccountScopeToken();
      setLocalPending(true);
      try {
        await operation({ signal: flight.controller.signal });
        if (!current()) return false;
        if (successMessage) Message.success(successMessage);
        const activeProjectId = loadControllerRef.current?.projectId;
        let refreshFailed = !(await load(activeProjectId, current));
        if (!current()) return false;
        try {
          await onChanged();
          if (!current()) return false;
        } catch (error) {
          if (!current()) return false;
          console.error('Failed to notify memory consumers after mutation:', error);
          refreshFailed = true;
        }
        if (refreshFailed) Message.warning(t('settings.memoryRefreshError'));
        return true;
      } catch (error) {
        if (!current()) return false;
        console.error('X-Science memory mutation failed:', error);
        Message.error(t('settings.memoryMutationError'));
        return false;
      } finally {
        if (mutationRef.current === flight) {
          mutationRef.current = null;
          if (account === getRendererAccountScopeToken()) setLocalPending(false);
        }
      }
    },
    [account, hasCurrentContext, load, onChanged, t]
  );

  // Keep the last acknowledged preference while busy. Only the scoped readback
  // changes it, so an old mutation cannot roll back a newer project's value.
  const handleToggle = useCallback(async () => {
    if (layer === 'project') {
      if (!selectedProjectId || projectEnabled === null) return;
      const next = !projectEnabled;
      await runMutation(() => setXScienceProjectMemoryEnabled(selectedProjectId, next));
      return;
    }
    const next = !enabled;
    await runMutation(() => setXScienceMemoryEnabled(next));
  }, [enabled, layer, projectEnabled, runMutation, selectedProjectId]);

  const handleAutoToggle = useCallback(async () => {
    if (layer !== 'global' || !enabled) return;
    const next = !autoEnabled;
    await runMutation((options) => setXScienceAutoMemoryEnabled(next, options));
  }, [autoEnabled, enabled, layer, runMutation]);

  const handleRecoveryPolicy = useCallback(
    async (policy: XScienceForkedViewRecovery) => {
      if (layer !== 'global' || !enabled || !autoEnabled) return;
      await runMutation((options) =>
        setXScienceAutoMemoryEnabled(autoEnabled, { ...options, forkedViewRecovery: policy })
      );
    },
    [autoEnabled, enabled, layer, runMutation]
  );

  const handleCreateMemory = useCallback(async () => {
    const text = newMemory.trim();
    if (!text || !addTarget) return;
    const succeeded = await runMutation(
      () => createXScienceMemory({ text, ...addTarget }),
      t('settings.memoryCreated')
    );
    if (!succeeded) return;
    setNewMemory('');
    setAdding(false);
  }, [addTarget, newMemory, runMutation, t]);

  const handleUpdateMemory = useCallback(
    async (memoryId: string) => {
      const text = editingMemoryText.trim();
      if (!text) return;
      const succeeded = await runMutation(() => updateXScienceMemory(memoryId, { text }), t('settings.memoryUpdated'));
      if (!succeeded) return;
      setEditingMemoryId(null);
      setEditingMemoryText('');
    },
    [editingMemoryText, runMutation, t]
  );

  const handleDeleteMemory = useCallback(
    (row: XScienceMemoryRow) => {
      Modal.confirm({
        title: t('settings.memoryDeleteTitle'),
        content: t('settings.memoryDeleteConfirm'),
        okButtonProps: { id: 'memory-delete-confirm', status: 'danger' },
        onOk: () => runMutation(() => deleteXScienceMemory(row.id), t('settings.memoryDeleted')),
      });
    },
    [runMutation, t]
  );

  const handleClearAll = useCallback(() => {
    Modal.confirm({
      title: t('settings.memoryClearTitle'),
      content: t('settings.memoryClearConfirm'),
      okButtonProps: { status: 'danger' },
      onOk: () => runMutation(() => clearXScienceMemories(), t('settings.memoryCleared')),
    });
  }, [runMutation, t]);

  const toggleCategoryAutoRecall = useCallback(
    async (category: XScienceMemoryCategory) => {
      await runMutation(() =>
        updateXScienceMemoryCategory(category.id, {
          name: category.name,
          guidance: category.guidance,
          autoRecall: !category.autoRecall,
        })
      );
    },
    [runMutation]
  );

  const confirmDeleteCategory = useCallback(async () => {
    if (!categoryToDelete) return;
    const succeeded = await runMutation(
      () => deleteXScienceMemoryCategory(categoryToDelete.id, deleteCategoryFacts),
      t('settings.memoryCategoryDeleted')
    );
    if (!succeeded) return;
    setCategoryToDelete(null);
    setDeleteCategoryFacts(false);
    setScope({ kind: 'profile', key: 'profile' });
  }, [categoryToDelete, deleteCategoryFacts, runMutation, t]);

  if (loadError && !loading && !hasLoaded) {
    return (
      <section data-testid='memory-manager' className='memory-manager'>
        <div className='memory-manager__state'>
          <p className='m-0 text-13px text-t-secondary' role='alert'>
            {loadError}
          </p>
          <Button
            icon={<Refresh size='14' />}
            onClick={() => void load(layer === 'project' ? (selectedProjectId ?? undefined) : undefined)}
          >
            {t('common.retry')}
          </Button>
        </div>
      </section>
    );
  }

  const activeEnabled = layer === 'project' ? projectEnabled === true : enabled;
  const activeToggleDisabled =
    pending ||
    loading ||
    !hasCurrentContext ||
    (layer === 'project' && (!selectedProjectId || projectEnabled === null));
  const showDisabledNotice =
    hasCurrentContext && !activeEnabled && !loading && (layer === 'global' || selectedProjectId !== null);
  const showProjectPicker = layer === 'project' && projects.length !== 1;
  const showScopePicker =
    hasCurrentContext && (layer === 'global' ? context.categories.length > 0 : projectScopeOptions.length > 1);
  const unknownPreference = (testId: string) => (
    <span
      data-testid={testId}
      role='status'
      aria-busy={loading}
      aria-label={loading ? t('common.loading') : loadError || t('settings.memoryProjectSelect')}
    >
      {loading ? <Spin size={14} /> : '—'}
    </span>
  );

  return (
    <section data-testid='memory-manager' className='memory-manager'>
      <div role='group' aria-label={t('settings.memoryLayers')} className='memory-manager__layer-stack'>
        <section
          role='presentation'
          data-memory-module='global'
          className={`memory-layer-card ${layer === 'global' ? 'memory-layer-card--active' : ''}`}
        >
          <MemoryLayerButton
            testId='memory-layer-global'
            active={layer === 'global'}
            icon='governance'
            label={t('settings.memoryGlobalLayer')}
            description={t('settings.memoryGlobalLayerDescription')}
            onClick={openGlobalLayer}
          />
          {layer === 'global' ? (
            <div className='memory-manager__status memory-manager__status--global'>
              <div className='memory-manager__status-control'>
                <span className='memory-manager__status-label'>{t('settings.memoryToggle')}</span>
                {hasCurrentContext ? (
                  <Switch
                    data-testid='memory-enabled-toggle'
                    aria-label={t('settings.memoryToggle')}
                    checked={enabled}
                    disabled={pending || loading}
                    loading={pending}
                    onChange={() => void handleToggle()}
                  />
                ) : (
                  unknownPreference('memory-enabled-pending')
                )}
              </div>
              <Tooltip content={t('settings.memoryAutoDescription')}>
                <div className='memory-manager__status-control'>
                  <span className='memory-manager__status-label'>{t('settings.memoryAuto')}</span>
                  {hasCurrentContext ? (
                    <Switch
                      data-testid='memory-auto-enabled-toggle'
                      aria-label={t('settings.memoryAuto')}
                      checked={autoEnabled}
                      disabled={pending || loading || !enabled}
                      loading={pending}
                      onChange={() => void handleAutoToggle()}
                    />
                  ) : (
                    unknownPreference('memory-auto-enabled-pending')
                  )}
                </div>
              </Tooltip>
              {hasCurrentContext ? (
                <MemoryRecoveryPolicy
                  value={context.forkedViewRecovery ?? 'strict'}
                  disabled={pending || loading || !enabled || !autoEnabled}
                  onChange={(policy) => void handleRecoveryPolicy(policy)}
                />
              ) : (
                unknownPreference('memory-recovery-policy-pending')
              )}
              <Button
                data-testid='memory-clear-all'
                type='text'
                status='danger'
                size='small'
                disabled={context.totalRows === 0 || pending || !hasCurrentContext}
                onClick={handleClearAll}
              >
                {t('settings.memoryClearAll')}
              </Button>
              {showDisabledNotice ? (
                <div className='memory-layer-card__notice'>
                  <span>{t('settings.memoryDisabledHint')}</span>
                  <Button
                    size='mini'
                    data-testid='memory-disabled-enable'
                    disabled={activeToggleDisabled}
                    onClick={() => void handleToggle()}
                  >
                    {t('settings.memoryTurnOn')}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </section>
        <section
          role='presentation'
          data-memory-module='project'
          className={`memory-layer-card ${layer === 'project' ? 'memory-layer-card--active' : ''}`}
        >
          <MemoryLayerButton
            testId='memory-layer-project'
            active={layer === 'project'}
            icon='experts'
            label={t('settings.memoryProjectLayer')}
            description={
              selectedProject
                ? t('settings.memoryProjectLayerSelected', { project: selectedProject.name })
                : t('settings.memoryProjectLayerDescription')
            }
            onClick={openProjectLayer}
          />
          {layer === 'project' ? (
            <div className='memory-manager__status memory-manager__status--project'>
              <div className='memory-manager__status-control'>
                <span className='memory-manager__status-label'>{t('settings.memoryProjectToggle')}</span>
                {hasCurrentContext ? (
                  <Switch
                    data-testid='memory-enabled-toggle'
                    aria-label={t('settings.memoryProjectToggle')}
                    checked={projectEnabled === true}
                    disabled={activeToggleDisabled}
                    loading={pending}
                    onChange={() => void handleToggle()}
                  />
                ) : (
                  unknownPreference('memory-enabled-pending')
                )}
              </div>
              {showDisabledNotice ? (
                <div className='memory-layer-card__notice'>
                  <span>{t('settings.memoryProjectDisabledHint')}</span>
                  <Button
                    size='mini'
                    data-testid='memory-disabled-enable'
                    disabled={activeToggleDisabled}
                    onClick={() => void handleToggle()}
                  >
                    {t('settings.memoryTurnOn')}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </section>
      </div>

      {loadError && hasCurrentContext ? (
        <div className='memory-manager__notice'>
          <p className='m-0 min-w-0 flex-1 text-12px text-t-secondary' role='status'>
            {loadError}
          </p>
          <Button
            size='mini'
            loading={loading}
            onClick={() => void load(layer === 'project' ? (selectedProjectId ?? undefined) : undefined)}
          >
            {t('common.retry')}
          </Button>
        </div>
      ) : null}

      <div
        data-testid='memory-workspace'
        data-memory-module='library'
        className={`memory-manager__workspace ${layer === 'project' ? 'memory-manager__workspace--project' : ''}`}
      >
        {loading && !hasLoaded ? (
          <div className='memory-manager__state'>
            <Spin />
          </div>
        ) : (
          <div data-testid='memory-editor' className='memory-manager__editor'>
            <div className='memory-manager__editor-header'>
              <div className='memory-manager__editor-heading'>
                <h4 className='memory-manager__editor-title'>{title}</h4>
                {selectedCategory?.guidance ? (
                  <p className='memory-manager__editor-description'>{selectedCategory.guidance}</p>
                ) : layer === 'global' && scope.kind === 'profile' ? (
                  <p className='memory-manager__editor-description'>{t('settings.memoryGlobalProfileDescription')}</p>
                ) : layer === 'project' && selectedProject ? (
                  <p className='memory-manager__editor-description'>{t('settings.memoryProjectSelectedDescription')}</p>
                ) : null}
              </div>
              <div className='memory-manager__editor-actions'>
                {showProjectPicker ? (
                  <div data-testid='memory-project-picker' className='memory-manager__project-selector'>
                    <Select
                      data-testid='memory-project-select'
                      size='small'
                      value={selectedProjectId ?? undefined}
                      placeholder={t('settings.memoryProjectSelectPlaceholder')}
                      aria-label={t('settings.memoryProjectList')}
                      onChange={(value) => selectProject(String(value))}
                    >
                      {projects.length > 0 ? (
                        projects.map((project) => (
                          <Select.Option key={project.projectId} value={project.projectId}>
                            {project.name}
                          </Select.Option>
                        ))
                      ) : (
                        <Select.Option value='__no-projects__' disabled>
                          {t('settings.memoryProjectNoProjects')}
                        </Select.Option>
                      )}
                    </Select>
                  </div>
                ) : null}
                {layer === 'project' && projectLoadError ? (
                  <span className='memory-manager__project-error' role='status'>
                    {projectLoadError}
                  </span>
                ) : null}
                {layer === 'global' && hasCurrentContext ? (
                  <div className='memory-manager__category-actions'>
                    <span className='memory-manager__category-count' data-testid='memory-category-count'>
                      {t('settings.memoryCategories')} {context.categories.length}/{MAX_MEMORY_CATEGORIES}
                    </span>
                    <Tooltip
                      content={
                        categoryLimitReached
                          ? t('settings.memoryCategoryLimit', { count: MAX_MEMORY_CATEGORIES })
                          : t('settings.memoryCategoryAdd')
                      }
                    >
                      <Button
                        data-testid='memory-category-add'
                        type='text'
                        size='small'
                        icon={<Plus theme='outline' size='14' />}
                        aria-label={
                          categoryLimitReached
                            ? t('settings.memoryCategoryLimit', { count: MAX_MEMORY_CATEGORIES })
                            : t('settings.memoryCategoryAdd')
                        }
                        disabled={
                          pending || (!categoryEditor.retained && categoryLimitReached) || !categoryEditor.available
                        }
                        onClick={() => categoryEditor.openCreate()}
                      >
                        {t(
                          categoryEditor.retained ? 'settings.memoryCategoryResumeSave' : 'settings.memoryCategoryAdd'
                        )}
                      </Button>
                    </Tooltip>
                  </div>
                ) : null}
                {showScopePicker ? (
                  <Select
                    data-testid='memory-scope-select'
                    size='small'
                    className='min-w-170px max-w-240px'
                    value={scopeValue}
                    aria-label={title}
                    onChange={(value) => handleScopeChange(String(value))}
                  >
                    {layer === 'global'
                      ? [
                          <Select.Option key='profile' value='profile'>
                            {t('settings.memoryGlobalProfile')}
                          </Select.Option>,
                          ...context.categories.map((category) => (
                            <Select.Option key={category.id} value={'category:' + category.id}>
                              {category.name}
                            </Select.Option>
                          )),
                        ]
                      : projectScopeOptions.map((option) => (
                          <Select.Option key={option.value} value={option.value}>
                            {option.label}
                          </Select.Option>
                        ))}
                  </Select>
                ) : null}
                {selectedCategory ? (
                  <>
                    <span className='memory-manager__auto-recall-control'>
                      {t('settings.memoryAutoRecallShort')}
                      <Switch
                        data-testid={`memory-category-auto-recall-${selectedCategory.id}`}
                        size='small'
                        checked={selectedCategory.autoRecall}
                        loading={pending}
                        disabled={pending}
                        onChange={() => void toggleCategoryAutoRecall(selectedCategory)}
                      />
                    </span>
                    <Tooltip content={t('common.edit')}>
                      <Button
                        data-testid={`memory-category-edit-${selectedCategory.id}`}
                        type='text'
                        size='small'
                        icon={<Edit theme='outline' size='15' />}
                        aria-label={`${t('common.edit')} ${selectedCategory.name}`}
                        disabled={pending || !categoryEditor.available}
                        onClick={() => categoryEditor.openEdit(selectedCategory)}
                      />
                    </Tooltip>
                    <Tooltip content={t('common.delete')}>
                      <Button
                        data-testid={`memory-category-delete-${selectedCategory.id}`}
                        type='text'
                        status='danger'
                        size='small'
                        icon={<Delete theme='outline' size='15' />}
                        aria-label={`${t('common.delete')} ${selectedCategory.name}`}
                        disabled={pending}
                        onClick={() => setCategoryToDelete(selectedCategory)}
                      />
                    </Tooltip>
                  </>
                ) : null}
                {addTarget ? (
                  <Button
                    data-testid='memory-add'
                    type='secondary'
                    size='small'
                    disabled={pending || !hasCurrentContext}
                    onClick={() => setAdding(true)}
                  >
                    {t('common.add')}
                  </Button>
                ) : null}
              </div>
            </div>

            <div className='memory-manager__content'>
              {awaitingContext ? (
                <div
                  className='memory-manager__state'
                  data-testid={loadError && !loading ? 'memory-context-error' : 'memory-context-loading'}
                  role={loadError && !loading ? 'alert' : 'status'}
                  aria-busy={loading}
                >
                  {loadError && !loading ? (
                    <>
                      <p>{loadError}</p>
                      <Button
                        size='small'
                        onClick={() => void load(layer === 'project' ? (selectedProjectId ?? undefined) : undefined)}
                      >
                        {t('common.retry')}
                      </Button>
                    </>
                  ) : (
                    <>
                      <Spin />
                      <span>{t('common.loading')}</span>
                    </>
                  )}
                </div>
              ) : sessionLoading ? (
                <div className='flex min-h-160px items-center justify-center' data-testid='memory-session-loading'>
                  <Spin />
                </div>
              ) : sessionError ? (
                <div
                  className='flex min-h-160px flex-col items-center justify-center gap-10px text-center'
                  data-testid='memory-session-error'
                  role='alert'
                >
                  <p className='m-0 text-12px text-t-secondary'>{t('settings.memorySessionLoadError')}</p>
                  <Button size='small' onClick={() => setSessionReloadToken((value) => value + 1)}>
                    {t('common.retry')}
                  </Button>
                </div>
              ) : (
                <>
                  {adding && addTarget ? (
                    <div className='mb-10px flex flex-col gap-7px'>
                      <Input.TextArea
                        data-testid='memory-new-input'
                        autoFocus
                        rows={3}
                        value={newMemory}
                        placeholder={t(
                          layer === 'project'
                            ? 'settings.memoryProjectNewPlaceholder'
                            : 'settings.memoryGlobalNewPlaceholder'
                        )}
                        onChange={setNewMemory}
                      />
                      <div className='flex justify-end gap-6px'>
                        <Button size='small' disabled={pending} onClick={() => setAdding(false)}>
                          {t('common.cancel')}
                        </Button>
                        <Button
                          data-testid='memory-new-save'
                          type='primary'
                          size='small'
                          disabled={!newMemory.trim()}
                          loading={pending}
                          onClick={() => void handleCreateMemory()}
                        >
                          {t('common.save')}
                        </Button>
                      </div>
                    </div>
                  ) : null}

                  {rows.length === 0 && !adding ? (
                    <div className='memory-manager__empty'>
                      <span>
                        {layer === 'project'
                          ? selectedProject
                            ? t('settings.memoryProjectEmpty')
                            : t('settings.memoryProjectListEmpty')
                          : t('settings.memoryEmpty')}
                      </span>
                    </div>
                  ) : (
                    <div className='flex flex-col divide-y divide-[var(--color-border-2)]'>
                      {rows.map((row) => (
                        <MemoryRow
                          key={row.id}
                          row={row}
                          category={context.categories.find((category) => category.id === row.categoryId)}
                          editing={editingMemoryId === row.id}
                          editText={editingMemoryText}
                          sessionScoped={scope.kind === 'session'}
                          pending={pending}
                          onEdit={() => {
                            setEditingMemoryId(row.id);
                            setEditingMemoryText(row.body);
                          }}
                          onEditText={setEditingMemoryText}
                          onCancelEdit={() => setEditingMemoryId(null)}
                          onSave={() => void handleUpdateMemory(row.id)}
                          onDelete={() => handleDeleteMemory(row)}
                        />
                      ))}
                    </div>
                  )}
                </>
              )}
            </div>
          </div>
        )}
      </div>

      {categoryDraft ? (
        <Modal
          title={categoryDraft.id ? t('settings.memoryCategoryEdit') : t('settings.memoryCategoryAdd')}
          visible
          onCancel={() => {
            if (pending) return;
            categoryEditor.close();
          }}
          onOk={() => void categoryEditor.save()}
          confirmLoading={pending}
          okText={categoryEditor.uncertain ? t('settings.memoryCategoryRetrySameSave') : undefined}
          cancelText={categoryEditor.uncertain ? t('settings.memoryCategoryCloseRetained') : undefined}
          cancelButtonProps={{ disabled: pending }}
          escToExit={!pending}
          okButtonProps={{
            id: 'memory-category-save',
            disabled: !categoryDraft.name.trim() || !categoryDraft.guidance.trim(),
          }}
          unmountOnExit
        >
          <div className='flex flex-col gap-12px'>
            {categoryEditor.uncertain ? (
              <div role='status' data-testid='memory-category-uncertain' className='text-12px text-t-secondary'>
                {t('settings.memoryCategoryUncertain')}
              </div>
            ) : null}
            <label className='flex flex-col gap-5px text-12px text-t-secondary'>
              {t('settings.memoryCategoryName')}
              <Input
                data-testid='memory-category-name'
                disabled={pending || categoryEditor.fieldsFrozen}
                value={categoryDraft.name}
                onChange={(name) => categoryEditor.update({ name })}
              />
            </label>
            <label className='flex flex-col gap-5px text-12px text-t-secondary'>
              {t('settings.memoryCategoryGuidance')}
              <Input.TextArea
                data-testid='memory-category-guidance'
                disabled={pending || categoryEditor.fieldsFrozen}
                value={categoryDraft.guidance}
                rows={4}
                onChange={(guidance) => categoryEditor.update({ guidance })}
              />
            </label>
            <div className='flex items-center justify-between gap-12px text-12px text-t-secondary'>
              <span>{t('settings.memoryAutoRecall')}</span>
              <Switch
                data-testid='memory-category-draft-auto-recall'
                checked={categoryDraft.autoRecall}
                disabled={pending || categoryEditor.fieldsFrozen}
                onChange={(autoRecall) => categoryEditor.update({ autoRecall })}
              />
            </div>
          </div>
        </Modal>
      ) : null}

      <Modal
        title={t('settings.memoryCategoryDeleteTitle')}
        visible={categoryToDelete !== null}
        onCancel={() => setCategoryToDelete(null)}
        onOk={() => void confirmDeleteCategory()}
        confirmLoading={pending}
        okButtonProps={{ status: 'danger' }}
        unmountOnExit
      >
        <div className='flex flex-col gap-14px'>
          <p className='m-0 text-13px text-t-secondary'>
            {t('settings.memoryCategoryDeleteDescription', { name: categoryToDelete?.name ?? '' })}
          </p>
          <div className='flex items-center justify-between gap-12px border border-arco-2 rd-6px px-12px py-10px'>
            <span className='text-12px text-t-secondary'>{t('settings.memoryCategoryDeleteFacts')}</span>
            <Switch checked={deleteCategoryFacts} onChange={setDeleteCategoryFacts} />
          </div>
        </div>
      </Modal>
    </section>
  );
};

const MemoryLayerButton: React.FC<{
  testId: string;
  active: boolean;
  icon: SettingsGeneratedIconId;
  label: string;
  description: string;
  onClick: () => void;
}> = ({ testId, active, icon, label, description, onClick }) => (
  <button
    type='button'
    data-testid={testId}
    aria-pressed={active}
    className={`memory-layer-tab ${active ? 'memory-layer-tab--active' : ''}`}
    onClick={onClick}
  >
    <SettingsGeneratedIcon id={icon} className='memory-layer-tab__icon' />
    <span className='memory-layer-tab__label'>{label}</span>
    <span className='memory-layer-tab__description'>{description}</span>
  </button>
);
const MemoryRow: React.FC<{
  row: XScienceMemoryRow;
  category?: XScienceMemoryCategory;
  editing: boolean;
  editText: string;
  sessionScoped: boolean;
  pending: boolean;
  onEdit: () => void;
  onEditText: (value: string) => void;
  onCancelEdit: () => void;
  onSave: () => void;
  onDelete: () => void;
}> = ({
  row,
  category,
  editing,
  editText,
  sessionScoped,
  pending,
  onEdit,
  onEditText,
  onCancelEdit,
  onSave,
  onDelete,
}) => {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const copyTimerRef = useRef<number | undefined>(undefined);
  useEffect(
    () => () => {
      if (copyTimerRef.current) window.clearTimeout(copyTimerRef.current);
    },
    []
  );
  const copy = async () => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard API unavailable');
      await navigator.clipboard.writeText(row.body);
      setCopied(true);
      if (copyTimerRef.current) window.clearTimeout(copyTimerRef.current);
      copyTimerRef.current = window.setTimeout(() => setCopied(false), 1600);
    } catch (error) {
      console.error('Failed to copy memory:', error);
      Message.error(t('common.copyFailed'));
    }
  };
  return (
    <div
      data-testid={`memory-row-${row.id}`}
      data-expanded={expanded || undefined}
      className='group py-10px first:pt-0 last:pb-0'
    >
      {editing ? (
        <div className='flex flex-col gap-7px'>
          <Input.TextArea value={editText} rows={3} onChange={onEditText} />
          <div className='flex justify-end gap-6px'>
            <Button size='small' onClick={onCancelEdit}>
              {t('common.cancel')}
            </Button>
            <Button type='primary' size='small' loading={pending} disabled={!editText.trim()} onClick={onSave}>
              {t('common.save')}
            </Button>
          </div>
        </div>
      ) : (
        <div className='flex items-start gap-6px rounded-6px px-4px py-2px hover:bg-fill-1'>
          <button
            type='button'
            className='flex min-w-0 flex-1 items-start gap-10px border-0 bg-transparent p-0 text-left'
            aria-expanded={expanded}
            onClick={() => setExpanded((value) => !value)}
          >
            {row.origin !== 'user' ? (
              <span className='mt-1px shrink-0 text-11px text-t-tertiary' title={t('settings.memoryExpertSaved')}>
                {t('settings.memoryAutomaticMarker')}
              </span>
            ) : null}
            <span className='min-w-0 flex-1'>
              <span
                className={`block break-words text-13px leading-5 text-t-primary ${expanded ? 'whitespace-pre-wrap' : 'truncate'}`}
              >
                {row.body}
              </span>
              <span
                className={`mt-5px flex flex-wrap items-center gap-6px text-11px text-t-tertiary ${expanded ? '' : 'hidden'}`}
              >
                <span>{row.origin === 'user' ? t('settings.memoryUserSaved') : t('settings.memoryExpertSaved')}</span>
                {category ? <Tag size='small'>{category.name}</Tag> : null}
                {row.updatedAt ? <span>{new Date(row.updatedAt).toLocaleString()}</span> : null}
              </span>
            </span>
          </button>
          <div className='flex items-center gap-2px opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity'>
            <Tooltip content={copied ? t('common.copySuccess') : t('common.copy')}>
              <Button
                type='text'
                size='small'
                icon={copied ? <Check size='15' /> : <Copy size='15' />}
                data-testid={`memory-row-copy-${row.id}`}
                aria-label={copied ? t('common.copySuccess') : t('common.copy')}
                onClick={() => void copy()}
              />
            </Tooltip>
            {!sessionScoped ? (
              <Tooltip content={t('common.edit')}>
                <Button
                  type='text'
                  size='small'
                  icon={<Edit theme='outline' size='15' />}
                  data-testid={`memory-row-edit-${row.id}`}
                  aria-label={`${t('common.edit')} ${row.body}`}
                  disabled={pending}
                  onClick={onEdit}
                />
              </Tooltip>
            ) : null}
            <Tooltip content={t('common.delete')}>
              <Button
                type='text'
                status='danger'
                size='small'
                icon={<Delete theme='outline' size='15' />}
                data-testid={`memory-row-delete-${row.id}`}
                aria-label={`${t('common.delete')} ${row.body}`}
                disabled={pending}
                onClick={onDelete}
              />
            </Tooltip>
          </div>
        </div>
      )}
    </div>
  );
};

export default XScienceMemoryManager;
