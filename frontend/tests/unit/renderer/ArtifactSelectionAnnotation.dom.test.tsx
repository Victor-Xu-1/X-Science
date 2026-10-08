import { ConfigProvider } from '@arco-design/web-react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ArtifactSelectionAnnotationModal } from '@/renderer/pages/artifact/ArtifactSelectionAnnotationModal';
import { artifactCanvasSelectionIdentity } from '@/renderer/pages/artifact/artifactCanvasSelection';
import { locateRenderedTextSelection } from '@/renderer/pages/artifact/artifactTextSelection';
import { SynonBiomedImageArtifactViewer } from '@/renderer/pages/artifact/SynonBiomedImageArtifactViewer';
import { renderWithI18n } from '../i18nTestUtils';

const createAnnotation = vi.hoisted(() => vi.fn());
const selection = {
  type: 'point' as const,
  text: 'Image position',
  x: 20,
  y: 30,
  xPercent: 25,
  yPercent: 50,
  pageNumber: null,
};
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
};
beforeEach(() => createAnnotation.mockReset());

vi.mock('@/renderer/services/synonBiomedAnnotations', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/services/synonBiomedAnnotations')>();
  return { ...actual, createSynonBiomedArtifactAnnotation: createAnnotation };
});

describe('Artifact selection annotation', () => {
  it('keys text, page and HTML anchors independently of screen placement', () => {
    const text = {
      type: 'text_selection' as const,
      text: 'Passage',
      x: 10,
      y: 20,
      startLine: 1,
      startColumn: 1,
      endLine: 1,
      endColumn: 8,
      selectionPrefix: '',
      pageNumber: null,
    };
    expect(artifactCanvasSelectionIdentity({ ...text, x: 100, y: 200 })).toBe(artifactCanvasSelectionIdentity(text));
    expect(artifactCanvasSelectionIdentity({ ...text, startLine: 2 })).not.toBe(artifactCanvasSelectionIdentity(text));
    expect(artifactCanvasSelectionIdentity({ ...text, selectionPrefix: 'Different source' })).not.toBe(
      artifactCanvasSelectionIdentity(text)
    );
    expect(artifactCanvasSelectionIdentity({ ...selection, pageNumber: 2 })).not.toBe(
      artifactCanvasSelectionIdentity(selection)
    );
    expect(artifactCanvasSelectionIdentity({ ...selection, text: 'Localized position label' })).toBe(
      artifactCanvasSelectionIdentity(selection)
    );
    const element = {
      type: 'html_element' as const,
      text: 'Cell',
      x: 10,
      y: 20,
      xPercent: 25,
      yPercent: 50,
      elementSelector: '#cell-1',
      elementDescriptor: 'td — Cell',
    };
    expect(artifactCanvasSelectionIdentity({ ...element, elementSelector: '#cell-2' })).not.toBe(
      artifactCanvasSelectionIdentity(element)
    );
    expect(
      artifactCanvasSelectionIdentity({
        ...element,
        xPercent: 80,
        yPercent: 10,
        elementDescriptor: 'Localized descriptor',
      })
    ).toBe(artifactCanvasSelectionIdentity(element));
  });

  it('retries only publishing a saved annotation after a consumer failure, without posting it twice', async () => {
    const annotation = { id: 'saved-annotation', text: 'Selection note' };
    createAnnotation.mockResolvedValueOnce(annotation);
    const onCreated = vi.fn().mockRejectedValueOnce(new Error('fixture consumer failure')).mockResolvedValue(undefined);
    await renderWithI18n(
      <ArtifactSelectionAnnotationModal
        artifactId='artifact-1'
        versionId='version-1'
        selection={selection}
        onCancel={vi.fn()}
        onCreated={onCreated}
      />
    );
    fireEvent.change(screen.getByRole('textbox', { name: '选区批注内容' }), { target: { value: annotation.text } });
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('批注已保存');
    expect(screen.getByRole('textbox', { name: '选区批注内容' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(2));
    expect(createAnnotation).toHaveBeenCalledTimes(1);
    expect(onCreated).toHaveBeenLastCalledWith(annotation);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not publish a late annotation into the parent after this editor is closed', async () => {
    const pending = deferred<{ id: string; text: string }>();
    createAnnotation.mockReturnValueOnce(pending.promise);
    const onCreated = vi.fn();
    const view = await renderWithI18n(
      <ArtifactSelectionAnnotationModal
        artifactId='artifact-1'
        versionId='version-1'
        selection={selection}
        onCancel={vi.fn()}
        onCreated={onCreated}
      />
    );
    fireEvent.change(screen.getByRole('textbox', { name: '选区批注内容' }), { target: { value: 'Selection note' } });
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    view.unmount();
    await act(async () => {
      pending.resolve({ id: 'old-annotation', text: 'Selection note' });
      await pending.promise;
    });
    expect(onCreated).not.toHaveBeenCalled();
  });

  it('resets an actually changed anchor, but not an equivalent selection object or toolbar location', async () => {
    const callbacks = { onCancel: vi.fn(), onCreated: vi.fn() };
    const view = await renderWithI18n(
      <ArtifactSelectionAnnotationModal
        artifactId='artifact-1'
        versionId='version-1'
        selection={selection}
        {...callbacks}
      />
    );
    fireEvent.change(screen.getByRole('textbox', { name: '选区批注内容' }), { target: { value: 'Current draft' } });
    view.rerender(
      <ArtifactSelectionAnnotationModal
        artifactId='artifact-1'
        versionId='version-1'
        selection={{ ...selection, x: 200, y: 300 }}
        {...callbacks}
      />
    );
    expect(screen.getByRole('textbox', { name: '选区批注内容' })).toHaveValue('Current draft');
    view.rerender(
      <ArtifactSelectionAnnotationModal
        artifactId='artifact-1'
        versionId='version-1'
        selection={{ ...selection, xPercent: 80 }}
        {...callbacks}
      />
    );
    expect(screen.getByRole('textbox', { name: '选区批注内容' })).toHaveValue('');
  });

  it('does not leave the submitted note editable while its annotation is pending', async () => {
    const pending = deferred<{ id: string; text: string }>();
    createAnnotation.mockReturnValueOnce(pending.promise);
    const view = await renderWithI18n(
      <ArtifactSelectionAnnotationModal
        artifactId='artifact-1'
        versionId='version-1'
        selection={selection}
        onCancel={vi.fn()}
        onCreated={vi.fn()}
      />
    );
    const editor = screen.getByRole('textbox', { name: '选区批注内容' });
    fireEvent.change(editor, { target: { value: 'Submitted note' } });
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    expect(editor).toBeDisabled();
    await act(async () => {
      pending.resolve({ id: 'annotation', text: 'Submitted note' });
      await pending.promise;
    });
    view.unmount();
  });

  it('maps an exact rendered selection back to one-based source coordinates and prefix', () => {
    const content = '# Result\n\nSTAT6 inhibition changed viability.\nConclusion.';
    expect(locateRenderedTextSelection(content, 'STAT6 inhibition changed viability.', 240, 180)).toEqual({
      type: 'text_selection',
      text: 'STAT6 inhibition changed viability.',
      x: 240,
      y: 180,
      startLine: 3,
      startColumn: 1,
      endLine: 3,
      endColumn: 36,
      selectionPrefix: '# Result\n\n',
      pageNumber: null,
    });
  });

  it('retains the selected text when rendered HTML cannot be mapped byte-for-byte to its source', () => {
    expect(locateRenderedTextSelection('<strong>STAT6</strong>', 'STAT6 result', 90, 110)).toEqual({
      type: 'text_selection',
      text: 'STAT6 result',
      x: 90,
      y: 110,
      startLine: null,
      startColumn: null,
      endLine: null,
      endColumn: null,
      selectionPrefix: null,
      pageNumber: null,
    });
  });

  it('creates a real text-selection annotation payload without dropping source anchors', async () => {
    createAnnotation.mockResolvedValueOnce({ id: 'annotation-1', text: 'Review this claim' });
    const onCreated = vi.fn();
    await renderWithI18n(
      <ConfigProvider>
        <ArtifactSelectionAnnotationModal
          artifactId='artifact-1'
          versionId='version-1'
          selection={{
            type: 'text_selection',
            text: 'STAT6 inhibition changed viability.',
            x: 240,
            y: 180,
            startLine: 3,
            startColumn: 1,
            endLine: 3,
            endColumn: 36,
            selectionPrefix: '# Result\n\n',
            pageNumber: null,
          }}
          onCancel={vi.fn()}
          onCreated={onCreated}
        />
      </ConfigProvider>
    );

    fireEvent.change(screen.getByRole('textbox', { name: '选区批注内容' }), {
      target: { value: 'Review this claim' },
    });
    fireEvent.click(screen.getByRole('button', { name: '添加' }));

    await waitFor(() =>
      expect(createAnnotation).toHaveBeenCalledWith('artifact-1', 'version-1', {
        type: 'text_selection',
        text: 'Review this claim',
        selectionText: 'STAT6 inhibition changed viability.',
        selectionPrefix: '# Result\n\n',
        startLine: 3,
        startColumn: 1,
        endLine: 3,
        endColumn: 36,
        xPercent: null,
        yPercent: null,
        pageNumber: null,
        elementSelector: null,
        elementDescriptor: null,
      })
    );
    expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: 'annotation-1' }));
  });

  it('normalizes image clicks against the rendered image and restores existing point markers', async () => {
    const onSelectionChange = vi.fn();
    await renderWithI18n(
      <SynonBiomedImageArtifactViewer
        filename='assay.png'
        contentUrl='/api/artifacts/image-1'
        annotations={[
          {
            id: 'point-1',
            artifactId: 'artifact-1',
            targetKey: 'av:version-1',
            label: '①',
            contentChecksum: 'checksum',
            type: 'point',
            text: 'Inspect this band',
            xPercent: 25,
            yPercent: 75,
            startLine: null,
            startColumn: null,
            endLine: null,
            endColumn: null,
            selectionText: null,
            pageNumber: null,
            selectionPrefix: null,
            screenshotArtifactId: null,
            elementSelector: null,
            elementDescriptor: null,
            addressedAt: null,
            addressedInFrameId: null,
            createdAt: '2026-07-13T00:00:00.000Z',
          },
        ]}
        onSelectionChange={onSelectionChange}
      />
    );
    const image = screen.getByRole('img', { name: 'assay.png' });
    Object.defineProperties(image, { naturalWidth: { value: 400 }, naturalHeight: { value: 200 } });
    Object.defineProperty(image, 'getBoundingClientRect', {
      value: () => ({ left: 100, top: 50, width: 400, height: 200, right: 500, bottom: 250 }),
    });
    fireEvent.load(image);
    fireEvent.click(image, { clientX: 200, clientY: 200 });

    expect(onSelectionChange).toHaveBeenLastCalledWith({
      type: 'point',
      text: '图片位置 25.0%, 75.0%',
      x: 200,
      y: 200,
      xPercent: 25,
      yPercent: 75,
      pageNumber: null,
    });
    expect(screen.getByRole('button', { name: '查看批注 ①' })).toHaveStyle({ left: '25%', top: '75%' });
  });

  it('creates point and HTML-element payloads without losing their anchors', async () => {
    createAnnotation.mockResolvedValue({ id: 'annotation-canvas', text: 'Canvas note' });
    const { rerender } = await renderWithI18n(
      <ConfigProvider>
        <ArtifactSelectionAnnotationModal
          artifactId='artifact-1'
          versionId='version-1'
          selection={{
            type: 'point',
            text: 'PDF 第 3 页 · 20.0%, 40.0%',
            x: 320,
            y: 240,
            xPercent: 20,
            yPercent: 40,
            pageNumber: 3,
          }}
          onCancel={vi.fn()}
          onCreated={vi.fn()}
        />
      </ConfigProvider>
    );
    fireEvent.change(screen.getByRole('textbox', { name: '选区批注内容' }), { target: { value: 'PDF point' } });
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    await waitFor(() =>
      expect(createAnnotation).toHaveBeenCalledWith(
        'artifact-1',
        'version-1',
        expect.objectContaining({ type: 'point', xPercent: 20, yPercent: 40, pageNumber: 3 })
      )
    );

    rerender(
      <ConfigProvider>
        <ArtifactSelectionAnnotationModal
          key='html-element'
          artifactId='artifact-1'
          versionId='version-1'
          selection={{
            type: 'html_element',
            text: 'STAT6 result',
            x: 500,
            y: 300,
            xPercent: 60,
            yPercent: 35,
            elementSelector: '#result-table > tbody > tr:nth-of-type(2)',
            elementDescriptor: 'tr — STAT6 result',
          }}
          onCancel={vi.fn()}
          onCreated={vi.fn()}
        />
      </ConfigProvider>
    );
    fireEvent.change(screen.getByRole('textbox', { name: '选区批注内容' }), {
      target: { value: 'HTML element' },
    });
    fireEvent.click(screen.getByRole('button', { name: '添加' }));
    await waitFor(() =>
      expect(createAnnotation).toHaveBeenCalledWith(
        'artifact-1',
        'version-1',
        expect.objectContaining({
          type: 'html_element',
          selectionText: 'STAT6 result',
          xPercent: 60,
          yPercent: 35,
          elementSelector: '#result-table > tbody > tr:nth-of-type(2)',
          elementDescriptor: 'tr — STAT6 result',
        })
      )
    );
  });
});
