import React, { useId, useState } from 'react';
import {
  ArrowLeft,
  BookOpen,
  ChevronDown,
  ChevronRight,
  FolderOpen,
  Inbox,
  PanelLeft,
  Plus,
  Trash2,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { libraryCollectionRows } from './LibraryCollectionSidebar';
import type { useLibraryWorkbench } from './useLibraryWorkbench';
import { isLibraryReadFresh } from './useLibraryWorkbench';
import { LibraryDirectoryState } from './LibraryDirectoryState';
import './libraryNavigationRail.css';

type Entry = { id: string; name: string; count?: number };
function RailGroup({
  label,
  entries,
  selected,
  collapsed,
  action,
  choose,
  known,
  state,
}: {
  label: string;
  entries: Entry[];
  selected: string;
  collapsed: boolean;
  action?: React.ReactNode;
  choose: (id: string) => void;
  known: boolean;
  state: React.ReactNode;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(true),
    [expanded, setExpanded] = useState(false);
  const id = useId();
  let visible = expanded ? entries : entries.slice(0, 5);
  const active = entries.find((entry) => entry.id === selected);
  if (!expanded && active && !visible.includes(active)) visible = [...visible.slice(0, 4), active];
  return (
    <section className='library-rail__group'>
      <div className='library-rail__group-heading'>
        <button
          type='button'
          aria-expanded={open}
          aria-controls={id}
          aria-label={label}
          onClick={() => setOpen((value) => !value)}
        >
          {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          <span>{label}</span>
          {known && <small>{entries.length}</small>}
        </button>
        {action}
      </div>
      <div id={id} hidden={!collapsed && !open}>
        {state}
        {visible.map((entry) => (
          <button
            type='button'
            className='library-rail__entry'
            key={entry.id}
            title={collapsed ? entry.name : undefined}
            aria-label={entry.name}
            aria-current={selected === entry.id ? 'page' : undefined}
            onClick={() => choose(entry.id)}
          >
            <FolderOpen size={16} aria-hidden='true' />
            <span>{entry.name}</span>
            {entry.count !== undefined && <small>{entry.count}</small>}
          </button>
        ))}
        {!collapsed && entries.length > 5 && (
          <button
            type='button'
            className='library-rail__entry'
            aria-expanded={expanded}
            onClick={() => setExpanded((value) => !value)}
          >
            {t(expanded ? 'conversation.libraryAbstract.showLess' : 'conversation.libraryAbstract.showMore')}
          </button>
        )}
      </div>
    </section>
  );
}

/** Presentation of the Page's existing snapshots; never starts a request. */
export function LibraryNavigationRail({
  reads,
  collapsed,
  toggle,
  back,
  inbox,
  trash = false,
  collectionId,
  projectId,
  busy,
  selectInbox,
  selectTrash,
  selectCollection,
  selectProject,
  create,
}: {
  reads: ReturnType<typeof useLibraryWorkbench>;
  collapsed: boolean;
  toggle: () => void;
  back: () => void;
  inbox: boolean;
  trash?: boolean;
  collectionId: string;
  projectId: string;
  busy: boolean;
  selectInbox: () => void;
  selectTrash?: () => void;
  selectCollection: (id: string) => void;
  selectProject: (id: string) => void;
  create: () => void;
}) {
  const { t } = useTranslation();
  const collections =
    reads.collections.status === 'ready'
      ? libraryCollectionRows(reads.collections.value?.collections ?? []).map(({ collection, path }) => ({
          id: collection.id,
          name: path,
          count: collection.item_count,
        }))
      : [];
  const projects =
    reads.projects.status === 'ready'
      ? (reads.projects.value ?? []).map((project) => ({ id: project.projectId, name: project.name }))
      : [];
  return (
    <section className='library-rail' data-collapsed={collapsed} aria-label={t('conversation.libraryWorkbench.title')}>
      <header className='library-rail__heading'>
        <span className='library-rail__mark'>
          <BookOpen size={16} aria-hidden='true' />
        </span>
        <div>
          <strong>{t('conversation.libraryWorkbench.title')}</strong>
          <p>{t('conversation.libraryWorkbench.eyebrow')}</p>
        </div>
        <button
          type='button'
          className='library-rail__toggle'
          data-library-rail-toggle
          aria-label={t(collapsed ? 'common.expandSidebar' : 'common.collapseSidebar')}
          aria-expanded={!collapsed}
          onClick={toggle}
        >
          <PanelLeft size={16} aria-hidden='true' />
        </button>
      </header>
      <div className='library-rail__return'>
        <button
          type='button'
          className='library-rail__entry'
          aria-label={t('common.back')}
          title={collapsed ? t('common.back') : undefined}
          onClick={back}
        >
          <ArrowLeft size={16} aria-hidden='true' />
          <span>{t('common.back')}</span>
        </button>
      </div>
      <nav aria-label={t('conversation.libraryWorkbench.title')} className='library-rail__navigation'>
        <button
          type='button'
          className='library-rail__entry'
          aria-label={t('conversation.libraryInbox.title')}
          title={collapsed ? t('conversation.libraryInbox.title') : undefined}
          aria-current={inbox ? 'page' : undefined}
          onClick={selectInbox}
        >
          <Inbox size={16} aria-hidden='true' />
          <span>{t('conversation.libraryInbox.title')}</span>
        </button>
        <button
          type='button'
          className='library-rail__entry'
          aria-label={t('conversation.libraryWorkbench.allReferences')}
          title={collapsed ? t('conversation.libraryWorkbench.allReferences') : undefined}
          aria-current={!inbox && !trash && !collectionId && !projectId ? 'page' : undefined}
          onClick={() => selectCollection('')}
        >
          <BookOpen size={16} aria-hidden='true' />
          <span>{t('conversation.libraryWorkbench.allReferences')}</span>
        </button>
        {selectTrash && (
          <button
            type='button'
            className='library-rail__entry'
            aria-label={t('conversation.libraryLifecycle.trash')}
            title={collapsed ? t('conversation.libraryLifecycle.trash') : undefined}
            aria-current={trash ? 'page' : undefined}
            onClick={selectTrash}
          >
            <Trash2 size={16} aria-hidden='true' />
            <span>{t('conversation.libraryLifecycle.trash')}</span>
          </button>
        )}
        <div className='library-rail__groups'>
          <RailGroup
            label={t('conversation.libraryWorkbench.projects')}
            entries={projects}
            selected={inbox ? '' : projectId}
            collapsed={collapsed}
            choose={selectProject}
            known={reads.projects.status === 'ready'}
            state={
              <LibraryDirectoryState
                view={reads.projects}
                errorText={t('conversation.libraryWorkbench.projectsFailed')}
                retry={reads.refresh}
                className='library-rail__hint'
              />
            }
          />
          <RailGroup
            label={t('conversation.libraryWorkbench.collections')}
            entries={collections}
            selected={inbox ? '' : collectionId}
            collapsed={collapsed}
            choose={selectCollection}
            known={reads.collections.status === 'ready'}
            state={
              <LibraryDirectoryState
                view={reads.collections}
                errorText={t('conversation.libraryWorkbench.collectionsFailed')}
                retry={reads.refresh}
                className='library-rail__hint'
              />
            }
            action={
              <button
                type='button'
                className='library-rail__add'
                disabled={busy || !isLibraryReadFresh(reads.collections)}
                aria-label={t('conversation.libraryWorkbench.newCollection')}
                onClick={create}
              >
                <Plus size={16} aria-hidden='true' />
              </button>
            }
          />
        </div>
      </nav>
    </section>
  );
}
