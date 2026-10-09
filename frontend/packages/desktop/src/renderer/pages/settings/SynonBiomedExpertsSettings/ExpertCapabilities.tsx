import type { SynonBiomedMcpServer, SynonBiomedSkill } from '@/renderer/services/synonBiomedCapabilities';
import { resolveSkillDisplayName } from '@/renderer/services/skills/synonBiomedSkillDescriptions';
import React from 'react';
import { useTranslation } from 'react-i18next';
import ExpertCapabilityPicker from './ExpertCapabilityPicker';

type SelectionProps = { selectedIds: string[]; disabled: boolean; onChange: (ids: string[]) => void };

export function ExpertSkillPicker({
  selectedIds,
  skills,
  disabled,
  onChange,
}: SelectionProps & { skills: SynonBiomedSkill[] }) {
  const { i18n } = useTranslation();
  const labelFor = (id: string) => {
    const skill = skills.find((item) => item.name === id);
    return resolveSkillDisplayName(id, skill?.displayName, i18n.language, skill?.name_i18n);
  };
  return (
    <ExpertCapabilityPicker
      kind='skills'
      selected={selectedIds.map((id) => ({ id, label: labelFor(id) }))}
      available={skills
        .filter((skill) => !selectedIds.includes(skill.name))
        .map((skill) => ({ id: skill.name, label: labelFor(skill.name) }))}
      disabled={disabled}
      onAdd={(id) => onChange([...selectedIds, id])}
      onRemove={(id) => onChange(selectedIds.filter((selected) => selected !== id))}
    />
  );
}

export function ExpertConnectorPicker({
  selectedIds,
  connectors,
  disabled,
  onChange,
}: SelectionProps & { connectors: SynonBiomedMcpServer[] }) {
  const labelFor = (id: string) => {
    const connector = connectors.find((item) => item.id === id);
    return connector?.displayName || connector?.name || id;
  };
  return (
    <ExpertCapabilityPicker
      kind='connectors'
      selected={selectedIds.map((id) => ({ id, label: labelFor(id) }))}
      available={connectors
        .filter((connector) => connector.enabled && !selectedIds.includes(connector.id))
        .map((connector) => ({ id: connector.id, label: labelFor(connector.id) }))}
      disabled={disabled}
      onAdd={(id) => onChange([...selectedIds, id])}
      onRemove={(id) => onChange(selectedIds.filter((selected) => selected !== id))}
    />
  );
}
