import { act, fireEvent, screen } from '@testing-library/react';
import type { TChatConversation } from '@/common/config/storage';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import React from 'react';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const originalConsoleError = console.error;

beforeAll(() => {
  vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
    if (String(args[0]).includes('Accessing element.ref was removed in React 19')) return;
    originalConsoleError(...args);
  });
});

afterAll(() => {
  vi.restoreAllMocks();
});

import SessionEditorModal from '@/renderer/pages/conversation/GroupedHistory/SessionEditorModal';
import SessionMoveModal from '@/renderer/pages/conversation/GroupedHistory/SessionMoveModal';
import SessionNotebookModal from '@/renderer/pages/conversation/GroupedHistory/SessionNotebookModal';

const conversation: TChatConversation = {
  id: 'frame-1',
  type: 'acp',
  name: 'STAT6 analysis',
  desc: 'Structure workflow',
  created_at: 1,
  modified_at: 1,
  status: 'finished',
  extra: { backend: 'xscience', project_id: 'project-a' },
};

const projects: XScienceProject[] = [
  {
    projectId: 'project-a',
    name: 'Current project',
    description: null,
    context: null,
    conversationCount: 1,
    artifactCount: 0,
    createdAt: null,
    updatedAt: null,
    lastActiveAt: null,
  },
  {
    projectId: 'project-b',
    name: 'Target project',
    description: null,
    context: null,
    conversationCount: 0,
    artifactCount: 0,
    createdAt: null,
    updatedAt: null,
    lastActiveAt: null,
  },
];

describe('v1.1 session action modals', () => {
  it('edits both the title and description', async () => {
    const onSubmit = vi.fn();
    await renderWithI18n(
      <SessionEditorModal
        conversation={conversation}
        loading={false}
        error={null}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />
    );

    expect(await screen.findByRole('dialog', { name: '编辑任务' })).toHaveStyle({
      width: 'min(520px, calc(100vw - 32px))',
    });
    expect(screen.getByRole('textbox', { name: '标题' })).toHaveValue('STAT6 analysis');
    expect(screen.getByRole('textbox', { name: '说明' })).toHaveValue('Structure workflow');
    fireEvent.change(screen.getByRole('textbox', { name: '标题' }), { target: { value: 'STAT6 updated' } });
    fireEvent.change(screen.getByRole('textbox', { name: '说明' }), { target: { value: 'Updated workflow' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    expect(onSubmit).toHaveBeenCalledWith({ name: 'STAT6 updated', taskSummary: 'Updated workflow' });
  });

  it('offers every project except the current project and submits the default target', async () => {
    const onSubmit = vi.fn();
    await renderWithI18n(
      <SessionMoveModal
        conversation={conversation}
        projects={projects}
        currentProjectId='project-a'
        loading={false}
        error={null}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />
    );

    expect(await screen.findByRole('dialog', { name: '移动任务到项目' })).toHaveStyle({
      width: 'min(520px, calc(100vw - 32px))',
    });
    expect(screen.getByRole('dialog', { name: '移动任务到项目' })).toHaveTextContent('STAT6 analysis');
    expect(screen.queryByText('Current project')).not.toBeInTheDocument();
    expect(screen.getByText('Target project')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '移动' }));
    expect(onSubmit).toHaveBeenCalledWith('project-b');
  });

  it('renders execution cells from the real notebook model', async () => {
    await renderWithI18n(
      <SessionNotebookModal
        conversation={conversation}
        records={[
          {
            kind: 'cell',
            id: 'cell-1',
            cellIndex: 0,
            language: 'python',
            source: 'print("STAT6")',
            stdout: 'STAT6\n',
            stderr: '',
            exitStatus: 'ok',
            errorLine: null,
            kernelKind: 'python',
            environment: 'base',
            filesWritten: [],
            at: null,
          },
        ]}
        loading={false}
        error={null}
        onClose={vi.fn()}
        hasOlder={false}
        onRetry={vi.fn()}
        onLoadOlder={vi.fn()}
      />,
      'en-US'
    );

    expect(await screen.findByRole('dialog', { name: 'Task notebook' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Execution cell 1' })).toHaveTextContent('print("STAT6")');
    expect(screen.getByText('STAT6')).toBeInTheDocument();
  });

  it('shows read-only retry and older-page states without discarding loaded records', async () => {
    const onRetry = vi.fn();
    const onLoadOlder = vi.fn();
    const props: React.ComponentProps<typeof SessionNotebookModal> = {
      conversation,
      records: [{ kind: 'save', id: 'record', label: 'Saved evidence', actor: null, detail: null, at: null }],
      loading: false,
      error: null,
      hasOlder: true,
      onRetry,
      onLoadOlder,
      onClose: vi.fn(),
    };
    const { rerender, i18n } = await renderWithI18n(<SessionNotebookModal {...props} />, 'en-US');
    await screen.findByRole('dialog', { name: 'Task notebook' });
    fireEvent.click(screen.getByRole('button', { name: 'Load older records' }));
    expect(onLoadOlder).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: /execute|run cell/i })).not.toBeInTheDocument();

    rerender(<SessionNotebookModal {...props} loading />);
    expect(screen.getByText(/Saved evidence/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Loading/ })).toBeDisabled();
    rerender(<SessionNotebookModal {...props} error='Read failed' />);
    expect(screen.getByText(/Saved evidence/)).toBeInTheDocument();
    expect(screen.getByText('Read failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: 'Load older records' })).not.toBeInTheDocument();

    rerender(<SessionNotebookModal {...props} records={[]} loading hasOlder={false} />);
    expect(screen.getByText(i18n.t('common.loading'))).toBeInTheDocument();
    expect(screen.queryByText('This task has no execution records')).not.toBeInTheDocument();
    rerender(<SessionNotebookModal {...props} records={[]} hasOlder={false} />);
    expect(screen.getByText('This task has no execution records')).toBeInTheDocument();
    rerender(<SessionNotebookModal {...props} records={[]} error='Initial read failed' hasOlder={false} />);
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();

    await act(async () => {
      await i18n.changeLanguage('zh-CN');
    });
    rerender(<SessionNotebookModal {...props} />);
    expect(screen.getByRole('button', { name: '加载更早记录' })).toBeInTheDocument();
  });

  it.each(['cancel', 'close', 'escape'] as const)(
    'keeps %s available while the project catalogue is loading',
    async (action) => {
      const onCancel = vi.fn();
      await renderWithI18n(
        <SessionMoveModal
          conversation={conversation}
          projects={[]}
          currentProjectId='project-a'
          loading
          error={null}
          onCancel={onCancel}
          onSubmit={vi.fn()}
        />,
        'en-US'
      );
      await screen.findByRole('dialog');
      if (action === 'escape')
        fireEvent.keyDown(screen.getByRole('button', { name: 'Close', exact: true }), { key: 'Escape', keyCode: 27 });
      else fireEvent.click(screen.getByRole('button', { name: action === 'close' ? 'Close' : 'Cancel', exact: true }));
      expect(onCancel).toHaveBeenCalledOnce();
    }
  );
});
