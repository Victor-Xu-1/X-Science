import { useCallback, useLayoutEffect, useMemo, useRef } from 'react';

type DetailTicket = { name: string; revision: number; scope: symbol };

/** A receipt belongs to one committed opening, not merely a matching skill name. */
export function useSkillDetailSession(name: string | undefined, visible: boolean, mode: string) {
  const scope = useMemo(() => Symbol('skill-detail'), [name, visible, mode]);
  const owner = useRef({ name: '', revision: 0, live: false, scope });
  useLayoutEffect(() => {
    owner.current = { name: name ?? '', revision: owner.current.revision + 1, live: visible && !!name, scope };
    return () => {
      owner.current.live = false;
      owner.current.revision += 1;
    };
  }, [name, visible, mode, scope]);

  const capture = useCallback((): DetailTicket | null => {
    const current = owner.current;
    return current.live ? { name: current.name, revision: current.revision, scope: current.scope } : null;
  }, []);
  const isCurrent = useCallback((ticket: DetailTicket) => {
    const current = owner.current;
    return current.live && current.revision === ticket.revision && current.name === ticket.name;
  }, []);
  return { capture, isCurrent, scope };
}

export type SkillDetailSession = ReturnType<typeof useSkillDetailSession>;
export type SkillDetailTicket = DetailTicket;
