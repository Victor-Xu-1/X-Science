import React from 'react';
import { NavLink } from 'react-router';
import { BookOpen } from 'lucide-react';
import classNames from 'classnames';
import { useTranslation } from 'react-i18next';

/** Native route entry; navigation does not submit or interrupt a research task. */
export function SiderLibraryEntry({ collapsed = false, onNavigate }: { collapsed?: boolean; onNavigate?: () => void }) {
  const { t } = useTranslation();
  const title = t('conversation.libraryWorkbench.title');
  return (
    <NavLink
      to='/library'
      aria-label={title}
      title={collapsed ? title : undefined}
      onClick={(event) => {
        if (event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) onNavigate?.();
      }}
      className={({ isActive }) =>
        classNames(
          'mx-8px flex min-h-32px items-center gap-8px rounded-7px px-9px py-7px text-12px text-t-secondary hover:bg-hover',
          isActive && 'bg-active text-t-primary',
          collapsed && 'justify-center px-0'
        )
      }
    >
      <BookOpen size={15} aria-hidden='true' />
      {!collapsed && <span>{title}</span>}
    </NavLink>
  );
}
