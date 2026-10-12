import type { TChatConversation } from '@/common/config/storage';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import { emitter } from '@/renderer/utils/emitter';
import { Message } from '@arco-design/web-react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

type MoveRuntime = Pick<
  typeof import('../conversationActionRuntime'),
  'loadXScienceProjects' | 'moveXScienceSession'
>;
type MoveState = {
  target: TChatConversation | null;
  projects: XScienceProject[];
  phase: 'idle' | 'loading' | 'ready' | 'saving';
  error: string | null;
};
const emptyState = (): MoveState => ({ target: null, projects: [], phase: 'idle', error: null });

/** Catalogue reads are disposable; an accepted move keeps its server-owned lifecycle. */
export function useSessionMove(loadRuntime: () => Promise<MoveRuntime>, onMoved: () => void) {
  const { t } = useTranslation();
  const [state, setState] = useState<MoveState>(emptyState);
  const readRef = useRef<AbortController | null>(null);
  const savingRef = useRef(false);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      readRef.current?.abort();
      readRef.current = null;
    };
  }, []);

  const handleMoveStart = useCallback(
    async (conversation: TChatConversation) => {
      if (savingRef.current) return;
      readRef.current?.abort();
      const controller = new AbortController();
      readRef.current = controller;
      const isCurrent = () => mountedRef.current && readRef.current === controller && !controller.signal.aborted;
      setState({ target: conversation, projects: [], phase: 'loading', error: null });
      try {
        const runtime = await loadRuntime();
        if (!isCurrent()) return;
        const projects = await runtime.loadXScienceProjects({ signal: controller.signal });
        if (isCurrent()) setState({ target: conversation, projects, phase: 'ready', error: null });
      } catch (error) {
        if (!isCurrent()) return;
        console.warn('[ConversationActions] Failed to load projects for move:', redactErrorText(String(error)));
        setState({
          target: conversation,
          projects: [],
          phase: 'ready',
          error: t('conversation.history.sessionMove.loadFailed'),
        });
      } finally {
        if (readRef.current === controller) readRef.current = null;
      }
    },
    [loadRuntime, t]
  );

  const handleMoveCancel = useCallback(() => {
    if (savingRef.current) return;
    readRef.current?.abort();
    readRef.current = null;
    setState(emptyState());
  }, []);

  const handleMoveConfirm = useCallback(
    async (projectId: string) => {
      const target = state.target;
      if (!target || state.phase !== 'ready' || savingRef.current) return;
      savingRef.current = true;
      setState((current) => ({ ...current, phase: 'saving', error: null }));
      try {
        const runtime = await loadRuntime();
        await runtime.moveXScienceSession(target.id, projectId);
        onMoved();
        emitter.emit('xscience.projects.refresh');
        if (mountedRef.current) {
          setState(emptyState());
          Message.success(t('conversation.history.sessionMove.success'));
        }
      } catch (error) {
        console.warn('[ConversationActions] Failed to move session:', redactErrorText(String(error)));
        if (mountedRef.current)
          setState((current) => ({ ...current, phase: 'ready', error: t('conversation.history.sessionMove.failed') }));
      } finally {
        savingRef.current = false;
      }
    },
    [loadRuntime, onMoved, state.phase, state.target, t]
  );

  return {
    moveTarget: state.target,
    moveProjects: state.projects,
    moveLoading: state.phase === 'loading' || state.phase === 'saving',
    moveError: state.error,
    handleMoveStart,
    handleMoveConfirm,
    handleMoveCancel,
  };
}
