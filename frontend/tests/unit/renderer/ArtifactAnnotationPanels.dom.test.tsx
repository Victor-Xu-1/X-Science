import { ConfigProvider } from '@arco-design/web-react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ArtifactAnnotationsPanel,
  ArtifactVerificationPanel,
} from '@/renderer/pages/artifact/ArtifactAnnotationPanels';
import { renderWithI18n } from '../i18nTestUtils';

const render = async (ui: React.ReactElement, language: 'zh-CN' | 'en-US' = 'zh-CN') => {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(ui, language);
  });
  return view;
};

const mocks = vi.hoisted(() => ({
  loadAnnotations: vi.fn(),
  createAnnotation: vi.fn(),
  updateAnnotation: vi.fn(),
  deleteAnnotation: vi.fn(),
  suggestEdit: vi.fn(),
  applyEdit: vi.fn(),
  loadVerification: vi.fn(),
  requestAudit: vi.fn(),
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: {
      useMessage: () => [{ success: vi.fn(), error: vi.fn() }, null],
    },
  };
});

vi.mock('@/renderer/services/synonBiomedAnnotations', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/services/synonBiomedAnnotations')>();
  return {
    ...actual,
    loadSynonBiomedArtifactAnnotations: mocks.loadAnnotations,
    createSynonBiomedArtifactAnnotation: mocks.createAnnotation,
    updateSynonBiomedArtifactAnnotation: mocks.updateAnnotation,
    deleteSynonBiomedArtifactAnnotation: mocks.deleteAnnotation,
    suggestSynonBiomedArtifactEdit: mocks.suggestEdit,
    applySynonBiomedArtifactEdit: mocks.applyEdit,
    loadSynonBiomedArtifactVerification: mocks.loadVerification,
    requestSynonBiomedFrameAudit: mocks.requestAudit,
  };
});

const annotation = {
  id: 'annotation-1',
  artifactId: 'artifact-1',
  targetKey: 'av:version-1',
  label: '①',
  contentChecksum: 'checksum-1',
  type: 'text_selection' as const,
  text: 'Review the STAT6 assay claim',
  xPercent: null,
  yPercent: null,
  startLine: 4,
  startColumn: 2,
  endLine: 5,
  endColumn: 20,
  selectionText: 'STAT6 assay',
  pageNumber: 2,
  selectionPrefix: null,
  screenshotArtifactId: null,
  elementSelector: null,
  elementDescriptor: null,
  addressedAt: null,
  addressedInFrameId: null,
  createdAt: '2026-07-13T00:00:00.000Z',
};

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
};

describe('Artifact annotation and verification panels', () => {
  it.each([
    { language: 'zh-CN' as const, add: '添加', type: '批注类型', option: '文本选择' },
    { language: 'en-US' as const, add: 'Add', type: 'Annotation type', option: 'Text selection' },
  ])(
    'keeps the $language composite type picker outside native label activation',
    async ({ language, add, type, option }) => {
      await render(<ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />, language);
      fireEvent.click(screen.getByRole('button', { name: add, exact: true }));
      const picker = screen.getByRole('combobox', { name: type });
      expect(picker.closest('label')).toBeNull();
      fireEvent.click(picker);
      expect(picker).toHaveAttribute('aria-expanded', 'true');
      fireEvent.click(screen.getByRole('option', { name: option, exact: true }));
      expect(picker).toHaveTextContent(option);
      expect(mocks.createAnnotation).not.toHaveBeenCalled();
    }
  );
  beforeEach(() => {
    for (const mock of Object.values(mocks)) mock.mockReset();
    mocks.loadAnnotations.mockResolvedValue({
      targetKey: 'av:version-1',
      currentChecksum: 'checksum-1',
      annotations: [annotation],
    });
    mocks.createAnnotation.mockResolvedValue({
      ...annotation,
      id: 'annotation-2',
      label: '②',
      type: 'point',
      text: 'New review note',
      selectionText: null,
      pageNumber: null,
      startLine: null,
    });
    mocks.loadVerification.mockResolvedValue([
      {
        id: 'check-pass',
        rootFrameId: 'frame-1',
        artifactVersionId: 'version-1',
        claimId: 'claim-1',
        claim: 'STAT6 binding improved',
        verdict: 'pass',
        severity: 'low',
        evidence: 'Two independent assay runs agree.',
        rebuttal: null,
        reviewerIndex: 0,
        reviewerModel: 'reviewer-model',
        reviewerFrameId: 'reviewer-frame',
        sourceRef: { kind: 'artifact_version' },
        status: 'resolved',
        reflagCount: 0,
        createdAt: '2026-07-13T00:00:00.000Z',
      },
      {
        id: 'check-warn',
        rootFrameId: 'frame-1',
        artifactVersionId: 'version-1',
        claimId: 'claim-2',
        claim: 'Assay variance remains acceptable',
        verdict: 'warn',
        severity: 'medium',
        evidence: 'Variance is near the threshold.',
        rebuttal: null,
        reviewerIndex: 1,
        reviewerModel: 'reviewer-model',
        reviewerFrameId: 'reviewer-frame',
        sourceRef: { kind: 'artifact_version' },
        status: 'open',
        reflagCount: 1,
        createdAt: '2026-07-13T00:00:00.000Z',
      },
    ]);
    mocks.requestAudit.mockResolvedValue({ frame_id: 'audit-frame' });
    mocks.suggestEdit.mockResolvedValue('STAT6 inhibition reduced viability.');
    mocks.applyEdit.mockResolvedValue({
      versionId: 'version-2',
      versionNumber: 2,
      artifactId: 'artifact-1',
      parentVersionId: 'version-1',
      carriedAnnotations: [{ ...annotation, id: 'annotation-carried' }],
    });
  });

  it('renders version anchors and creates a real annotation-shaped record', async () => {
    await render(
      <ConfigProvider>
        <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />
      </ConfigProvider>
    );

    expect(await screen.findByText('Review the STAT6 assay claim')).toBeInTheDocument();
    expect(screen.getByText('“STAT6 assay”')).toBeInTheDocument();
    expect(screen.getByText('第 2 页')).toBeInTheDocument();
    expect(screen.getByText('第 4 行')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    fireEvent.change(screen.getByRole('textbox', { name: '批注内容' }), { target: { value: 'New review note' } });
    const addButtons = screen.getAllByRole('button', { name: '添加' });
    fireEvent.click(addButtons.at(-1)!);

    await waitFor(() =>
      expect(mocks.createAnnotation).toHaveBeenCalledWith(
        'artifact-1',
        'version-1',
        expect.objectContaining({ type: 'point', text: 'New review note' })
      )
    );
    expect(await screen.findByText('New review note')).toBeInTheDocument();
  });

  it('does not publish an older version read over the current version list', async () => {
    const oldRead = deferred<{ currentChecksum: string; annotations: Array<typeof annotation> }>();
    mocks.loadAnnotations
      .mockReturnValueOnce(oldRead.promise)
      .mockResolvedValueOnce({ currentChecksum: 'new-checksum', annotations: [] });
    const onChange = vi.fn();
    const view = await render(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' onAnnotationsChange={onChange} />
    );
    view.rerender(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-2' onAnnotationsChange={onChange} />
    );
    await screen.findByText('暂无批注');
    onChange.mockClear();
    await act(async () => {
      oldRead.resolve({ currentChecksum: 'checksum-1', annotations: [annotation] });
      await oldRead.promise;
    });

    expect(screen.queryByText(annotation.text)).not.toBeInTheDocument();
    expect(screen.queryByText('checksum-1')).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('keeps a same-version draft on callback rerenders without rereading the list', async () => {
    const view = await render(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' onAnnotationsChange={vi.fn()} />
    );
    await screen.findByText(annotation.text);
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    fireEvent.change(screen.getByRole('textbox', { name: '批注内容' }), { target: { value: 'Unsaved annotation' } });
    view.rerender(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' onAnnotationsChange={vi.fn()} />
    );

    expect(screen.getByRole('textbox', { name: '批注内容' })).toHaveValue('Unsaved annotation');
    expect(mocks.loadAnnotations).toHaveBeenCalledTimes(1);
  });

  it('closes an obsolete editor and ignores its late save after the version changes', async () => {
    const save = deferred<typeof annotation>();
    mocks.createAnnotation.mockReturnValueOnce(save.promise);
    const onChange = vi.fn();
    const view = await render(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' onAnnotationsChange={onChange} />
    );
    await screen.findByText(annotation.text);
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    fireEvent.change(screen.getByRole('textbox', { name: '批注内容' }), {
      target: { value: 'Old version annotation' },
    });
    fireEvent.click(screen.getAllByRole('button', { name: '添加' }).at(-1)!);
    expect(mocks.createAnnotation).toHaveBeenCalledWith(
      'artifact-1',
      'version-1',
      expect.objectContaining({ text: 'Old version annotation' })
    );
    mocks.loadAnnotations.mockResolvedValueOnce({ currentChecksum: 'new-checksum', annotations: [] });
    view.rerender(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-2' onAnnotationsChange={onChange} />
    );
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '添加批注' })).not.toBeInTheDocument());
    await screen.findByText('暂无批注');
    onChange.mockClear();
    await act(async () => {
      save.resolve({ ...annotation, text: 'Old version annotation' });
      await save.promise;
    });

    expect(screen.queryByText('Old version annotation')).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('only publishes the latest same-version refresh when replies arrive out of order', async () => {
    const oldRefresh = deferred<{ currentChecksum: string; annotations: Array<typeof annotation> }>();
    const newRefresh = deferred<{ currentChecksum: string; annotations: Array<typeof annotation> }>();
    const onChange = vi.fn();
    await render(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' onAnnotationsChange={onChange} />
    );
    mocks.loadAnnotations.mockReturnValueOnce(oldRefresh.promise).mockReturnValueOnce(newRefresh.promise);
    const refresh = screen.getByRole('button', { name: '刷新批注' });
    fireEvent.click(refresh);
    fireEvent.click(refresh);
    await act(async () => {
      newRefresh.resolve({ currentChecksum: 'current-checksum', annotations: [] });
      await newRefresh.promise;
    });
    await screen.findByText('暂无批注');
    onChange.mockClear();
    await act(async () => {
      oldRefresh.resolve({ currentChecksum: 'obsolete-checksum', annotations: [annotation] });
      await oldRefresh.promise;
    });
    expect(screen.queryByText(annotation.text)).not.toBeInTheDocument();
    expect(screen.getByText('current-checksum')).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('coalesces an external refresh until the in-flight write settles', async () => {
    const pending = deferred<typeof annotation>();
    mocks.createAnnotation.mockReturnValueOnce(pending.promise);
    const created = { ...annotation, id: 'created-annotation', text: 'New annotation' };
    const view = await render(
      <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' refreshToken={0} />
    );
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    const editor = screen.getByRole('textbox', { name: '批注内容' });
    fireEvent.change(editor, { target: { value: created.text } });
    fireEvent.click(screen.getAllByRole('button', { name: '添加' }).at(-1)!);
    expect(editor).toBeDisabled();
    expect(screen.getByLabelText('批注类型')).toHaveAttribute('aria-disabled', 'true');
    mocks.loadAnnotations.mockResolvedValueOnce({
      currentChecksum: 'latest-checksum',
      annotations: [annotation, created],
    });
    view.rerender(<ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' refreshToken={1} />);
    view.rerender(<ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' refreshToken={2} />);
    expect(mocks.loadAnnotations).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.resolve(created);
      await pending.promise;
    });
    await screen.findByText(created.text);
    expect(mocks.loadAnnotations).toHaveBeenCalledTimes(2);
    expect(screen.getByText('latest-checksum')).toBeInTheDocument();
  });

  it('preserves a failed annotation draft with a visible error and allows a real retry', async () => {
    mocks.createAnnotation.mockRejectedValueOnce(new Error('fixture write failure'));
    await render(<ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />);
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    const editor = screen.getByRole('textbox', { name: '批注内容' });
    fireEvent.change(editor, { target: { value: 'New review note' } });
    fireEvent.click(screen.getAllByRole('button', { name: '添加' }).at(-1)!);
    expect(await screen.findByRole('alert')).toHaveTextContent('保存批注失败');
    expect(editor).toHaveValue('New review note');
    expect(editor).toBeEnabled();
    fireEvent.click(screen.getAllByRole('button', { name: '添加' }).at(-1)!);
    expect(await screen.findByText('New review note')).toBeInTheDocument();
    expect(mocks.createAnnotation).toHaveBeenCalledTimes(2);
  });

  it('keeps a successfully created annotation visible even when the initial list read failed', async () => {
    mocks.loadAnnotations.mockRejectedValueOnce(new Error('fixture initial read failure'));
    await render(<ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />);
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.queryByText('暂无批注')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    fireEvent.change(screen.getByRole('textbox', { name: '批注内容' }), { target: { value: 'New review note' } });
    fireEvent.click(screen.getAllByRole('button', { name: '添加' }).at(-1)!);
    expect(await screen.findByText('New review note')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(mocks.createAnnotation).toHaveBeenCalledTimes(1);
    expect(mocks.loadAnnotations).toHaveBeenCalledTimes(1);
  });

  it('keeps the native type selector operable and switches the editor fields without losing the draft', async () => {
    await render(<ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />);
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    const editor = screen.getByRole('textbox', { name: '批注内容' });
    fireEvent.change(editor, { target: { value: 'Unsaved annotation' } });
    fireEvent.click(screen.getByText('位置批注', { exact: true }));
    expect(await screen.findByRole('listbox')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('option', { name: '文本选择' }));
    expect(screen.getByRole('textbox', { name: '批注内容' })).toHaveValue('Unsaved annotation');
    expect(screen.getByRole('spinbutton', { name: '起始行' })).toBeInTheDocument();
    expect(screen.queryByRole('spinbutton', { name: '横向位置 (%)' })).not.toBeInTheDocument();
    expect(mocks.createAnnotation).not.toHaveBeenCalled();
  });

  it('shows verification verdict counts and starts an audit for the creating frame', async () => {
    await render(
      <ConfigProvider>
        <ArtifactVerificationPanel versionId='version-1' rootFrameId='frame-1' />
      </ConfigProvider>
    );

    expect(await screen.findByText('STAT6 binding improved')).toBeInTheDocument();
    expect(screen.getByText('Assay variance remains acceptable')).toBeInTheDocument();
    expect(screen.getByText('Two independent assay runs agree.')).toBeInTheDocument();
    expect(screen.getAllByText('通过')[0].parentElement).toHaveTextContent('1');
    expect(screen.getAllByText('警告')[0].parentElement).toHaveTextContent('1');

    fireEvent.click(screen.getByRole('button', { name: '重新审计' }));
    await waitFor(() => expect(mocks.requestAudit).toHaveBeenCalledWith('frame-1'));
  });

  it.each([false, true])('does not refresh after unmount when audit response is pending: %s', async (pending) => {
    let completeAudit!: (value: { frame_id: string }) => void;
    if (pending)
      mocks.requestAudit.mockReturnValueOnce(
        new Promise((resolve) => {
          completeAudit = resolve;
        })
      );
    const view = await render(
      <ConfigProvider>
        <ArtifactVerificationPanel versionId='version-1' rootFrameId='frame-1' />
      </ConfigProvider>
    );
    await screen.findByText('STAT6 binding improved');
    vi.useFakeTimers();
    try {
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: '重新审计' }));
      });
      view.unmount();
      await act(async () => {
        if (pending) completeAudit({ frame_id: 'audit-frame' });
        await Promise.resolve();
        await vi.advanceTimersByTimeAsync(1200);
      });
      expect(mocks.loadVerification).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('reviews a generated diff, allows manual revision and applies an immutable child version', async () => {
    const onVersionApplied = vi.fn();
    await render(
      <ConfigProvider>
        <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' onVersionApplied={onVersionApplied} />
      </ConfigProvider>
    );

    await screen.findByText('Review the STAT6 assay claim');
    fireEvent.click(screen.getByRole('button', { name: '改进批注 ①' }));
    expect(screen.getByRole('dialog', { name: '改进选区' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: '修改要求或问题' })).toHaveValue('Review the STAT6 assay claim');

    fireEvent.click(screen.getByRole('button', { name: '生成修改' }));
    await waitFor(() =>
      expect(mocks.suggestEdit).toHaveBeenCalledWith('artifact-1', 'version-1', {
        selectedText: 'STAT6 assay',
        annotationText: 'Review the STAT6 assay claim',
        mode: 'edit',
      })
    );
    expect(await screen.findByText('修改建议')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '手工修改建议' }));
    fireEvent.change(screen.getByRole('textbox', { name: '修改后的建议文本' }), {
      target: { value: 'STAT6 inhibition consistently reduced viability.' },
    });
    fireEvent.click(screen.getByRole('button', { name: '应用为新版本' }));

    await waitFor(() =>
      expect(mocks.applyEdit).toHaveBeenCalledWith('artifact-1', 'version-1', {
        selectedText: 'STAT6 assay',
        replacementText: 'STAT6 inhibition consistently reduced viability.',
      })
    );
    await waitFor(() =>
      expect(onVersionApplied).toHaveBeenCalledWith(expect.objectContaining({ versionId: 'version-2' }))
    );
    expect(await screen.findByText('修改已应用，新版本已创建')).toBeInTheDocument();
  });

  it('supports the v1.1 ask mode without exposing an apply action for an answer', async () => {
    mocks.suggestEdit.mockResolvedValueOnce('The selected assay statement does not specify direction or magnitude.');
    await render(
      <ConfigProvider>
        <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />
      </ConfigProvider>
    );

    await screen.findByText('Review the STAT6 assay claim');
    fireEvent.click(screen.getByRole('button', { name: '改进批注 ①' }));
    fireEvent.change(screen.getByRole('textbox', { name: '修改要求或问题' }), {
      target: { value: 'What is missing from this statement?' },
    });
    const askButtons = screen.getAllByRole('button', { name: '询问' });
    fireEvent.click(askButtons.at(-1)!);

    await waitFor(() =>
      expect(mocks.suggestEdit).toHaveBeenCalledWith('artifact-1', 'version-1', {
        selectedText: 'STAT6 assay',
        annotationText: 'What is missing from this statement?',
        mode: 'ask',
      })
    );
    expect(
      await screen.findByText('The selected assay statement does not specify direction or magnitude.')
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '应用为新版本' })).not.toBeInTheDocument();
  });

  it('renders annotation anchors and actions in English', async () => {
    await render(
      <ConfigProvider>
        <ArtifactAnnotationsPanel artifactId='artifact-1' versionId='version-1' />
      </ConfigProvider>,
      'en-US'
    );

    expect(await screen.findByText('Version annotations')).toBeInTheDocument();
    expect(screen.getByText('Page 2')).toBeInTheDocument();
    expect(screen.getByText('Line 4')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Refine annotation ①' })).toBeInTheDocument();
    expect(screen.queryByText('版本批注')).not.toBeInTheDocument();
  });
});
