import React from 'react';
import type { PreviewMetadata } from '../../context/PreviewContext';
import { isNotebookSessionNavigation } from '@/renderer/services/notebook/notebookPreviewNavigation';
import { LazyXScienceNotebookViewer } from '../viewers/scientificPreviewLoaders';
import { useTranslation } from 'react-i18next';

const LiveNotebookPreview = React.lazy(() => import('./LiveNotebookPreview'));
function Unavailable({ capacity }: { capacity: boolean }) {
  const { t } = useTranslation();
  return (
    <p role='alert' className='p-12px text-12px text-t-secondary'>
      {t(capacity ? 'conversation.manualNotebook.sessionLimit' : 'conversation.manualNotebook.loadFailed')}
    </p>
  );
}
class NotebookBoundary extends React.Component<React.PropsWithChildren, { failed: boolean; capacity: boolean }> {
  state = { failed: false, capacity: false };
  static getDerivedStateFromError(error: unknown) {
    return { failed: true, capacity: error instanceof Error && error.message === 'notebook_open_session_limit' };
  }
  componentDidCatch(error: unknown) {
    console.warn('[Notebook] Preview unavailable', { errorName: error instanceof Error ? error.name : typeof error });
  }
  render() {
    return this.state.failed ? <Unavailable capacity={this.state.capacity} /> : this.props.children;
  }
}
export default function NotebookPreviewContent({
  metadata,
  content,
  filename,
}: {
  metadata?: PreviewMetadata;
  content: string;
  filename: string;
}) {
  const navigation = metadata?.notebookSession;
  return isNotebookSessionNavigation(navigation) ? (
    <NotebookBoundary key={JSON.stringify([navigation.frameId, navigation.rootFrameId])}>
      <LiveNotebookPreview navigation={navigation} />
    </NotebookBoundary>
  ) : (
    <LazyXScienceNotebookViewer
      filename={filename}
      contentUrl={metadata?.contentUrl}
      content={metadata?.contentUrl ? undefined : content}
    />
  );
}
