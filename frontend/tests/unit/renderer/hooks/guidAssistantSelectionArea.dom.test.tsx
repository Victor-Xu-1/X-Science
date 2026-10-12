/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { Assistant } from '@/common/types/agent/assistantTypes';
import AssistantSelectionArea from '@/renderer/pages/guid/components/AssistantSelectionArea';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key,
  }),
}));

vi.mock('react-router', () => ({
  useNavigate: () => vi.fn(),
}));

vi.mock('@arco-design/web-react', async () => {
  const actual = await vi.importActual<typeof import('@arco-design/web-react')>('@arco-design/web-react');
  return {
    ...actual,
    Message: {
      useMessage: () => [{ warning: vi.fn() }, <div key='message-holder' />],
    },
  };
});

describe('AssistantSelectionArea', () => {
  it('keeps the X-Science expert picker visible after an assistant is selected', () => {
    render(
      <AssistantSelectionArea
        selectedAssistantId='xscience:aidd-expert'
        assistants={assistants()}
        localeKey='en-US'
        onSelectAssistant={vi.fn()}
      />
    );

    expect(screen.getByTestId('preset-pill-xscience:aidd-expert')).toBeInTheDocument();
    expect(screen.queryByTestId('btn-add-preset')).not.toBeInTheDocument();
    expect(screen.queryByText('Select an assistant to start a task')).not.toBeInTheDocument();
    expect(screen.queryByText('Try these example prompts:')).not.toBeInTheDocument();
    expect(screen.queryByText('Run target discovery')).not.toBeInTheDocument();
  });

  it('moves overflow X-Science experts into a more dropdown', async () => {
    render(
      <AssistantSelectionArea
        selectedAssistantId='xscience:aidd-expert'
        assistants={manyAssistants()}
        localeKey='en-US'
        onSelectAssistant={vi.fn()}
      />
    );

    expect(screen.getByTestId('preset-pill-xscience:aidd-expert')).toBeInTheDocument();
    expect(screen.getByTestId('preset-pill-xscience:oncology-expert')).toBeInTheDocument();
    expect(screen.getByTestId('preset-pill-xscience:medchem-expert')).toBeInTheDocument();
    expect(screen.getByTestId('preset-pill-xscience:dmpk-expert')).toBeInTheDocument();
    expect(screen.queryByTestId('preset-pill-xscience:immunology-expert')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('assistant-more-btn'));

    expect(await screen.findByTestId('assistant-overflow-xscience:immunology-expert')).toBeInTheDocument();
    expect(screen.getByTestId('assistant-overflow-xscience:genomics-bioinfo-expert')).toBeInTheDocument();
    expect(screen.queryByTestId('assistant-overflow-xscience:aidd-expert')).not.toBeInTheDocument();
    expect(screen.queryByTestId('assistant-overflow-xscience:oncology-expert')).not.toBeInTheDocument();
  });

  it('reports the real X-Science assistant id when a pill is selected', () => {
    const onSelectAssistant = vi.fn();

    render(
      <AssistantSelectionArea
        selectedAssistantId='xscience:aidd-expert'
        assistants={assistants()}
        localeKey='en-US'
        onSelectAssistant={onSelectAssistant}
      />
    );

    fireEvent.click(screen.getByTestId('preset-pill-xscience:oncology-expert'));

    expect(onSelectAssistant).toHaveBeenCalledWith('xscience:oncology-expert');
  });

  it('orders X-Science expert pills by sort_order before applying overflow', () => {
    render(
      <AssistantSelectionArea
        selectedAssistantId='xscience:aidd-expert'
        assistants={[
          mkXScienceAssistant('xscience:late-expert', 'Late Expert', 90),
          mkXScienceAssistant('xscience:early-expert', 'Early Expert', 5),
          ...assistants(),
          mkXScienceAssistant('xscience:mid-expert', 'Mid Expert', 15),
        ]}
        localeKey='en-US'
        onSelectAssistant={vi.fn()}
      />
    );

    expect(
      screen
        .getAllByRole('button')
        .slice(0, 4)
        .map((node) => node.textContent?.trim())
    ).toEqual(['Early Expert', 'AIDD Expert', 'Mid Expert', 'Oncology Expert']);
  });

  it('keeps a selected overflow X-Science expert visible in the top pill row', () => {
    render(
      <AssistantSelectionArea
        selectedAssistantId='xscience:genomics-bioinfo-expert'
        assistants={manyAssistants()}
        localeKey='en-US'
        onSelectAssistant={vi.fn()}
      />
    );

    expect(screen.getByTestId('preset-pill-xscience:genomics-bioinfo-expert')).toBeInTheDocument();
    expect(screen.queryByTestId('preset-pill-xscience:dmpk-expert')).not.toBeInTheDocument();
  });

  it('can re-render from an empty assistant catalog without breaking hook order', () => {
    const { rerender } = render(
      <AssistantSelectionArea
        selectedAssistantId={null}
        assistants={[]}
        localeKey='en-US'
        onSelectAssistant={vi.fn()}
      />
    );

    expect(() =>
      rerender(
        <AssistantSelectionArea
          selectedAssistantId='xscience:aidd-expert'
          assistants={assistants()}
          localeKey='en-US'
          onSelectAssistant={vi.fn()}
        />
      )
    ).not.toThrow();

    expect(screen.getByTestId('preset-pill-xscience:aidd-expert')).toBeInTheDocument();
  });
});

function assistants(): Assistant[] {
  return [
    mkXScienceAssistant('xscience:aidd-expert', 'AIDD Expert', 10, ['Run target discovery']),
    mkXScienceAssistant('xscience:oncology-expert', 'Oncology Expert', 20, ['Prioritize oncology targets']),
  ];
}

function manyAssistants(): Assistant[] {
  return [
    ...assistants(),
    mkXScienceAssistant('xscience:medchem-expert', 'MedChem Expert', 30),
    mkXScienceAssistant('xscience:dmpk-expert', 'DMPK Expert', 40),
    mkXScienceAssistant('xscience:immunology-expert', 'Immunology Expert', 50),
    mkXScienceAssistant('xscience:genomics-bioinfo-expert', 'Genomics Bioinfo Expert', 60),
  ];
}

function mkXScienceAssistant(id: string, name: string, sort_order: number, prompts: string[] = []): Assistant {
  const agentId = id
    .replace(/^xscience:/, '')
    .replace(/-/g, '_')
    .toUpperCase();
  return {
    id,
    source: 'builtin',
    name,
    name_i18n: {},
    description_i18n: {},
    enabled: true,
    sort_order,
    agent_id: agentId,
    agent: { type: 'xscience', source: 'builtin', acp_backend: 'xscience' },
    enabled_skills: [],
    custom_skill_names: [],
    disabled_builtin_skills: [],
    context_i18n: {},
    prompts,
    prompts_i18n: {},
    models: [],
    agent_status: 'online',
    deletable: false,
  };
}
