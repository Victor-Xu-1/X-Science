import React from 'react';
import { NavLink } from 'react-router';
import { BookOpen } from 'lucide-react';
import classNames from 'classnames';
import { useTranslation } from 'react-i18next';
import styles from './SiderLibraryEntry.module.css';

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
      className={classNames(styles.entry, collapsed && styles.collapsed)}
    >
      <span className={styles.icon}>
        <BookOpen size={18} aria-hidden='true' />
      </span>
      {!collapsed && <span className={styles.label}>{title}</span>}
    </NavLink>
  );
}
