import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ProjectCommandPalette from '@/renderer/components/layout/Titlebar/ProjectCommandPalette';
import { renderWithI18n } from '../../i18nTestUtils';

const gateway = vi.hoisted(() => ({
  projects: vi.fn(),
  artifacts: vi.fn(),
  benches: vi.fn(),
}));
const insertReference = vi.hoisted(() => vi.fn());

vi.mock('@/renderer/services/synonBiomedGateway', () => ({
  loadSynonBiomedProjects: gateway.projects,
  loadSynonBiomedProjectArtifacts: gateway.artifacts,
  loadSynonBiomedProjectBenches: gateway.benches,
}));
vi.mock('@/renderer/components/chat/SendBox/composerReferenceBridge', () => ({
  insertArtifactReferenceIntoActiveComposer: insertReference,
}));

const Location = () => <div data-testid='location'>{useLocation().pathname}</div>;

describe('ProjectCommandPalette', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() });
    gateway.projects.mockResolvedValue([{ projectId: 'project-1', name: 'STAT6', description: null }]);
    gateway.artifacts.mockResolvedValue([
      { artifactId: 'artifact-1', versionId: 'version-1', filename: 'STAT6_report.md' },
    ]);
    gateway.benches.mockResolvedValue([{ frameId: 'frame-1', name: 'STAT6 workflow' }]);
    insertReference.mockReset().mockReturnValue(true);
  });

  it('opens repeatedly with Ctrl+K and navigates to a real session result', async () => {
    await renderWithI18n(
      <MemoryRouter initialEntries={['/conversation/current']}>
        <ProjectCommandPalette />
        <Location />
      </MemoryRouter>
    );

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(await screen.findByRole('combobox', { name: '项目命令搜索' })).toBeInTheDocument();
    expect(await screen.findByText('STAT6 workflow')).toBeInTheDocument();
    fireEvent.click(screen.getByText('STAT6 workflow'));
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/conversation/frame-1'));

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(await screen.findByRole('combobox', { name: '项目命令搜索' })).toBeInTheDocument();
  });

  it('opens projects through the canonical project route', async () => {
    await renderWithI18n(
      <MemoryRouter initialEntries={['/conversation/current']}>
        <ProjectCommandPalette />
        <Location />
      </MemoryRouter>
    );

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    const palette = await screen.findByTestId('project-command-palette');
    const input = palette.querySelector('input');
    if (!input) throw new Error('project command search input is missing');
    fireEvent.change(input, { target: { value: 'STAT6' } });
    fireEvent.click(await screen.findByText('STAT6'));

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/projects/project-1'));
  });

  it('inserts an artifact reference with Shift+Enter', async () => {
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
      </MemoryRouter>
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    const input = await screen.findByRole('combobox', { name: '项目命令搜索' });
    await screen.findByText('STAT6_report.md');
    fireEvent.change(input, { target: { value: 'STAT6_report' } });
    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true });
    expect(insertReference).toHaveBeenCalledWith({
      filename: 'STAT6_report.md',
      artifactId: 'artifact-1',
      versionId: 'version-1',
    });
  });

  it('shows a recoverable error when project loading fails', async () => {
    gateway.projects.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([]);
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
      </MemoryRouter>
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    const retry = await screen.findByText('加载失败，点击重试');
    fireEvent.click(retry);
    await waitFor(() => expect(gateway.projects).toHaveBeenCalledTimes(2));
  });

  it('renders command labels and search affordances in English', async () => {
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
      </MemoryRouter>,
      'en-US'
    );

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(await screen.findByRole('combobox', { name: 'Project command search' })).toBeInTheDocument();
    expect(await screen.findByText('New task')).toBeInTheDocument();
    expect(screen.getByText('Shift+Enter to reference')).toBeInTheDocument();
  });

  it('names the dialog and exposes one active result through its labelled combobox', async () => {
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
      </MemoryRouter>,
      'en-US'
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    await screen.findByText('STAT6 workflow');
    expect(screen.getByRole('dialog', { name: 'Project command search' })).toBeInTheDocument();
    const input = screen.getByRole('combobox', { name: 'Project command search' });
    const results = document.getElementById(input.getAttribute('aria-controls')!);
    expect(results).toBe(screen.getByRole('listbox', { name: 'Project command search' }));
    expect(document.getElementById(input.getAttribute('aria-activedescendant')!)).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('does not choose a hidden cached action while a newly opened palette is loading', async () => {
    gateway.projects
      .mockResolvedValueOnce([{ projectId: 'project-1', name: 'STAT6', description: null }])
      .mockImplementationOnce(() => new Promise(() => {}));
    await renderWithI18n(
      <MemoryRouter initialEntries={['/conversation/current']}>
        <ProjectCommandPalette />
        <Location />
      </MemoryRouter>
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    await screen.findByText('STAT6 workflow');
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    const input = await screen.findByRole('combobox', { name: '项目命令搜索' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent('/conversation/current');
    expect(insertReference).not.toHaveBeenCalled();
  });

  it('retains the project and available sessions when its artifact catalogue fails', async () => {
    gateway.artifacts.mockRejectedValueOnce(new Error('artifact catalogue unavailable'));
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
      </MemoryRouter>,
      'en-US'
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(await screen.findByText('STAT6 workflow')).toBeInTheDocument();
    expect(screen.getByText('STAT6')).toBeInTheDocument();
    expect(screen.getByText('Some project contents are temporarily unavailable.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('STAT6_report.md')).toBeInTheDocument();
    expect(screen.queryByText('Some project contents are temporarily unavailable.')).not.toBeInTheDocument();
    expect(gateway.artifacts).toHaveBeenCalledTimes(2);
  });

  it('moves the active option through Home/End and scrolls it into view without selecting it', async () => {
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
        <Location />
      </MemoryRouter>,
      'en-US'
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    await screen.findByText('STAT6_report.md');
    const input = screen.getByRole('combobox');
    fireEvent.keyDown(input, { key: 'End' });
    const last = document.getElementById(input.getAttribute('aria-activedescendant')!);
    expect(last).toHaveTextContent('STAT6_report.md');
    expect(last).toHaveAttribute('aria-selected', 'true');
    expect(last).toHaveAttribute('tabindex', '-1');
    expect(last?.scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
    fireEvent.keyDown(input, { key: 'Home' });
    expect(document.getElementById(input.getAttribute('aria-activedescendant')!)).toHaveTextContent('New task');
    expect(insertReference).not.toHaveBeenCalled();
  });

  it('does not hijack composing, default-prevented or Alt-modified keyboard input', async () => {
    await renderWithI18n(
      <MemoryRouter>
        <ProjectCommandPalette />
      </MemoryRouter>
    );
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true, isComposing: true });
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true, altKey: true });
    const prevented = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, cancelable: true });
    prevented.preventDefault();
    window.dispatchEvent(prevented);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(gateway.projects).not.toHaveBeenCalled();
  });
});
