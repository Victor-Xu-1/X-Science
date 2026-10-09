import {
  attachSynonBiomedExpertConnector,
  deleteSynonBiomedExpertProfile,
  detachSynonBiomedExpertConnector,
  loadSynonBiomedExpertInstructions,
  loadSynonBiomedExpertProfilesWithRuntimeConnectors,
  saveSynonBiomedExpertInstructions,
  setSynonBiomedExpertProfileEnabled,
  updateSynonBiomedExpertProfile,
  updateSynonBiomedExpertSkills,
  type SynonBiomedExpertProfile,
} from '@/renderer/services/agents/synonBiomedExpertProfiles';
import {
  loadSynonBiomedMcpServers,
  loadSynonBiomedSkills,
  type SynonBiomedMcpServer,
  type SynonBiomedSkill,
} from '@/renderer/services/synonBiomedCapabilities';
import {
  loadSynonBiomedExpertUsage,
  type SynonBiomedExpertUsageByName,
} from '@/renderer/services/agents/synonBiomedExpertUsage';
import { Message } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { ExpertUsageView } from './ExpertUsageSummary';
export type ExpertDetailDraft = {
  displayName: string;
  description: string;
  instructions: string;
  skillNames: string[];
  connectorIds: string[];
};
type ExpertDraft = ExpertDetailDraft;

const createRequestedFromHash = (): boolean => {
  if (typeof window === 'undefined') return false;
  const query = window.location.hash.split('?', 2)[1] ?? '';
  return new URLSearchParams(query).get('create') === '1';
};

const clearCreateRequestFromHash = (): void => {
  if (typeof window === 'undefined' || !window.location.hash.includes('?')) return;
  const [route, query = ''] = window.location.hash.split('?', 2);
  const params = new URLSearchParams(query);
  if (!params.has('create')) return;
  params.delete('create');
  const suffix = params.size > 0 ? `?${params.toString()}` : '';
  window.history.replaceState(
    window.history.state,
    '',
    `${window.location.pathname}${window.location.search}${route}${suffix}`
  );
};

function sameSet(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value) => right.includes(value));
}

function connectorIdsFor(profile: SynonBiomedExpertProfile, connectors: SynonBiomedMcpServer[]): string[] {
  // Metadata may be delayed or unavailable; it cannot revoke configured IDs.
  const ids = new Set(profile.connectorIds ?? []);
  for (const connector of connectors) {
    if (connector.attachedAgents.includes(profile.name)) {
      ids.add(connector.id);
    }
  }
  return [...ids];
}

/** One authority for expert reads, submitted writes and detail-opening ownership. */
export default function useExpertWorkbenchController() {
  const { t } = useTranslation();
  const [message, messageContext] = Message.useMessage({ maxCount: 4 });
  const [profiles, setProfiles] = useState<SynonBiomedExpertProfile[]>([]);
  const [skills, setSkills] = useState<SynonBiomedSkill[]>([]);
  const [connectors, setConnectors] = useState<SynonBiomedMcpServer[]>([]);
  const [expertUsage, setExpertUsage] = useState<ExpertUsageView>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [selected, setSelected] = useState<SynonBiomedExpertProfile | null>(null);
  const [draft, setDraft] = useState<ExpertDraft | null>(null);
  const [baseline, setBaseline] = useState<ExpertDraft | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [pendingProfileName, setPendingProfileName] = useState<string | null>(null);
  const [createVisible, setCreateVisible] = useState(createRequestedFromHash());
  const viewActive = useRef(false);
  const savingGeneration = useRef<number | null>(null);
  const pendingProfileToken = useRef<string | null>(null);
  const detailGeneration = useRef(0);
  const catalogGeneration = useRef(0);
  const translationRef = useRef(t);
  translationRef.current = t;

  const reloadCatalog = useCallback(async () => {
    const generation = ++catalogGeneration.current;
    setLoading(true);
    setLoadError('');
    setExpertUsage(undefined);

    let nextProfiles: SynonBiomedExpertProfile[];
    try {
      nextProfiles = await loadSynonBiomedExpertProfilesWithRuntimeConnectors();
      if (generation !== catalogGeneration.current) return;
      setProfiles(nextProfiles);
      setSelected((current) =>
        current ? (nextProfiles.find((profile) => profile.name === current.name) ?? null) : current
      );
    } catch (error) {
      if (generation !== catalogGeneration.current) return;
      console.error('Failed to load expert catalog:', error);
      setLoadError(translationRef.current('settings.expertsSettings.loadFailed'));
      setLoading(false);
      return;
    }

    // The profile list is the critical first paint. Skills, MCP metadata and
    // usage are secondary enrichment and must not keep the whole page blocked.
    if (generation === catalogGeneration.current) setLoading(false);
    void Promise.all([
      loadSynonBiomedSkills().catch((error: unknown): SynonBiomedSkill[] => {
        console.error('Failed to load expert skills:', error);
        return [];
      }),
      loadSynonBiomedMcpServers().catch((error: unknown): SynonBiomedMcpServer[] => {
        console.error('Failed to load expert connectors:', error);
        return [];
      }),
      loadSynonBiomedExpertUsage().catch((error: unknown): SynonBiomedExpertUsageByName | null => {
        console.error('Failed to load expert usage:', error);
        return null;
      }),
    ]).then(([nextSkills, nextConnectors, nextUsage]) => {
      if (generation !== catalogGeneration.current) return;
      setSkills(nextSkills);
      setConnectors(nextConnectors);
      setExpertUsage(nextUsage);
    });
  }, []);

  useEffect(() => {
    viewActive.current = true;
    void reloadCatalog();
    return () => {
      viewActive.current = false;
      detailGeneration.current += 1;
      catalogGeneration.current += 1;
    };
  }, [reloadCatalog]);

  const openProfile = useCallback(
    async (profile: SynonBiomedExpertProfile) => {
      const generation = ++detailGeneration.current;
      setSelected(profile);
      setDraft(null);
      setBaseline(null);
      setDetailLoading(true);
      try {
        const instructions = await loadSynonBiomedExpertInstructions(profile);
        if (generation !== detailGeneration.current) return;
        const nextDraft: ExpertDraft = {
          displayName: profile.displayName,
          description: profile.description,
          instructions,
          skillNames: [...profile.skillNames],
          connectorIds: connectorIdsFor(profile, connectors),
        };
        setDraft(nextDraft);
        setBaseline(nextDraft);
      } catch (error) {
        console.error('Failed to load expert details:', error);
        if (generation === detailGeneration.current) {
          message.error(translationRef.current('settings.expertsSettings.detailLoadFailed'));
          setSelected(null);
        }
      } finally {
        if (generation === detailGeneration.current) setDetailLoading(false);
      }
    },
    [connectors, message]
  );

  const closeDetail = useCallback(() => {
    detailGeneration.current += 1;
    setSelected(null);
    setDraft(null);
    setBaseline(null);
  }, []);

  const dirty = useMemo(() => {
    if (!draft || !baseline) return false;
    return (
      draft.displayName !== baseline.displayName ||
      draft.description !== baseline.description ||
      draft.instructions !== baseline.instructions ||
      !sameSet(draft.skillNames, baseline.skillNames) ||
      !sameSet(draft.connectorIds, baseline.connectorIds)
    );
  }, [baseline, draft]);

  const save = useCallback(async () => {
    if (
      !selected ||
      !draft ||
      !baseline ||
      saving ||
      savingGeneration.current !== null ||
      pendingProfileToken.current ||
      !dirty
    )
      return;
    if (!draft.displayName.trim() || !draft.description.trim()) {
      message.error(translationRef.current('settings.expertsSettings.nameDescriptionRequired'));
      return;
    }
    const opening = detailGeneration.current;
    savingGeneration.current = opening;
    setSaving(true);
    let mutationStarted = false;
    try {
      let nextProfile = selected;
      if (
        selected.source === 'user' &&
        (draft.displayName !== baseline.displayName || draft.description !== baseline.description)
      ) {
        mutationStarted = true;
        nextProfile = await updateSynonBiomedExpertProfile(selected.name, {
          name: selected.name,
          displayName: draft.displayName.trim(),
          description: draft.description.trim(),
          systemPrompt: draft.instructions,
          enabled: selected.enabled,
        });
      } else if (draft.instructions !== baseline.instructions) {
        mutationStarted = true;
        await saveSynonBiomedExpertInstructions(selected, draft.instructions);
      }

      if (!sameSet(draft.skillNames, baseline.skillNames)) {
        mutationStarted = true;
        nextProfile = await updateSynonBiomedExpertSkills(selected.name, baseline.skillNames, draft.skillNames);
      }

      const addedConnectors = draft.connectorIds.filter((id) => !baseline.connectorIds.includes(id));
      const removedConnectors = baseline.connectorIds.filter((id) => !draft.connectorIds.includes(id));
      if (addedConnectors.length > 0 || removedConnectors.length > 0) mutationStarted = true;
      await Promise.all([
        ...removedConnectors.map((serverId) => detachSynonBiomedExpertConnector(selected.name, serverId)),
        ...addedConnectors.map((serverId) => attachSynonBiomedExpertConnector(selected.name, serverId)),
      ]);

      if (!viewActive.current) return;
      const catalogRequest = ++catalogGeneration.current;
      const [nextProfiles, nextSkills, nextConnectors] = await Promise.all([
        loadSynonBiomedExpertProfilesWithRuntimeConnectors(),
        loadSynonBiomedSkills(),
        loadSynonBiomedMcpServers(),
      ]);
      if (!viewActive.current || catalogRequest !== catalogGeneration.current) return;
      setProfiles(nextProfiles);
      setSkills(nextSkills);
      setConnectors(nextConnectors);
      if (opening !== detailGeneration.current) {
        setSelected((current) =>
          current ? (nextProfiles.find((profile) => profile.name === current.name) ?? current) : null
        );
        return;
      }
      nextProfile = nextProfiles.find((profile) => profile.name === nextProfile.name) ?? nextProfile;
      const nextDraft: ExpertDraft = {
        ...draft,
        displayName: nextProfile.displayName,
        description: nextProfile.description,
        skillNames: [...nextProfile.skillNames],
        connectorIds: connectorIdsFor(nextProfile, nextConnectors),
      };
      setSelected(nextProfile);
      setDraft(nextDraft);
      setBaseline(nextDraft);
      message.success(translationRef.current('settings.expertsSettings.saved'));
    } catch {
      console.error('Failed to save expert settings');
      if (!viewActive.current) return;
      if (mutationStarted) {
        if (opening === detailGeneration.current) {
          message.warning(translationRef.current('settings.expertsSettings.savePartiallyApplied'));
          closeDetail();
        }
        await reloadCatalog();
      } else if (opening === detailGeneration.current) {
        message.error(translationRef.current('settings.expertsSettings.saveFailed'));
      }
    } finally {
      savingGeneration.current = null;
      if (viewActive.current) setSaving(false);
    }
  }, [baseline, closeDetail, dirty, draft, message, pendingProfileName, reloadCatalog, saving, selected]);

  const toggleEnabled = useCallback(
    async (profile: SynonBiomedExpertProfile, enabled: boolean) => {
      if (profile.source !== 'user' || pendingProfileToken.current || savingGeneration.current !== null) return;
      const opening = detailGeneration.current;
      pendingProfileToken.current = profile.name;
      setPendingProfileName(profile.name);
      try {
        const updated = await setSynonBiomedExpertProfileEnabled(profile.name, enabled);
        if (!viewActive.current) return;
        setProfiles((current) => current.map((item) => (item.name === updated.name ? updated : item)));
        setSelected((current) => (current?.name === updated.name ? updated : current));
      } catch {
        console.error('Failed to update expert state');
        if (viewActive.current && opening === detailGeneration.current) {
          message.error(translationRef.current('settings.expertsSettings.statusUpdateFailed'));
        }
      } finally {
        pendingProfileToken.current = null;
        if (viewActive.current) setPendingProfileName(null);
      }
    },
    [message]
  );

  const removeProfile = useCallback(() => {
    if (!selected || selected.source !== 'user') return;
    Modal.confirm({
      title: translationRef.current('settings.expertsSettings.deleteTitle'),
      content: translationRef.current('settings.expertsSettings.deleteBody', {
        name: selected.displayName,
      }),
      okButtonProps: { status: 'danger' },
      onOk: async () => {
        try {
          await deleteSynonBiomedExpertProfile(selected.name);
          closeDetail();
          await reloadCatalog();
        } catch (error) {
          console.error('Failed to delete expert:', error);
          message.error(translationRef.current('settings.expertsSettings.deleteFailed'));
        }
      },
    });
  }, [closeDetail, message, reloadCatalog, selected]);

  const closeCreate = useCallback(() => {
    setCreateVisible(false);
    clearCreateRequestFromHash();
  }, []);

  useEffect(() => {
    const handleHashChange = () => {
      if (createRequestedFromHash()) setCreateVisible(true);
    };
    window.addEventListener('hashchange', handleHashChange);
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, []);

  const opening = detailGeneration.current;
  const ownsDetail = () => viewActive.current && opening === detailGeneration.current;
  return {
    messageContext,
    profiles,
    skills,
    connectors,
    expertUsage,
    loading,
    loadError,
    selected,
    draft,
    baseline,
    dirty,
    detailLoading,
    saving,
    pendingProfileName,
    createVisible,
    setCreateVisible,
    closeCreate,
    openProfile,
    toggleEnabled,
    reloadCatalog,
    detailKey: `${selected?.name ?? ''}:${opening}`,
    savingCurrent: saving && savingGeneration.current === opening,
    updateDraft: (update: (current: ExpertDraft) => ExpertDraft) => {
      setDraft((current) => (ownsDetail() && current ? update(current) : current));
    },
    saveCurrent: () => {
      if (ownsDetail()) void save();
    },
    closeCurrent: () => {
      if (ownsDetail()) closeDetail();
    },
    toggleCurrent: (enabled: boolean) => {
      if (ownsDetail() && selected) void toggleEnabled(selected, enabled);
    },
    deleteCurrent: () => {
      if (ownsDetail()) removeProfile();
    },
  };
}
