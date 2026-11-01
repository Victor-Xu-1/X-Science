import React, { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import {
  autoUpdate,
  flip,
  FloatingFocusManager,
  FloatingPortal,
  offset,
  shift,
  useClick,
  useDismiss,
  useFloating,
  useInteractions,
  useRole,
} from '@floating-ui/react';
import { Check, ChevronDown, Plus, Search, Tags } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { normalizeResourceTagName, type ResourceTag } from '@/common/adapter/resourceTags';
import { ResourceTagVisual, resourceTagLabel } from './ResourceTagBadges';
import './resourceTags.css';

export type ResourceTagMenuProps = {
  targetKey: string;
  /** Read-only content inside the existing trigger button, never another control. */
  trigger?: React.ReactNode;
  tags: readonly ResourceTag[];
  assignedIds: readonly string[];
  pendingTagIds: readonly string[];
  creating: boolean;
  disabled: boolean;
  error?: string;
  onAssign: (tagId: string, assigned: boolean) => Promise<void>;
  onCreate: (name: string) => Promise<void>;
};
const matchName = (name: string) => name.normalize('NFKC').trim().replace(/\s+/gu, ' ').toLowerCase();
type Option = { key: string; tag?: ResourceTag };

function Menu({
  trigger: triggerContent,
  tags,
  assignedIds,
  pendingTagIds,
  creating,
  disabled,
  error,
  onAssign,
  onCreate,
}: ResourceTagMenuProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [activeKey, setActiveKey] = useState<string>();
  const [failure, setFailure] = useState(false);
  const [portalRoot, setPortalRoot] = useState<HTMLElement | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const interaction = useRef(0);
  const mounted = useRef(false);
  const pending = useRef(new Set<string>());
  const tabDismissal = useRef(false);
  const id = useId();
  const changeOpen = useCallback((value: boolean) => {
    interaction.current++;
    if (value) tabDismissal.current = false;
    setOpen(value);
    setQuery('');
    setActiveKey(undefined);
    setFailure(false);
  }, []);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      interaction.current++;
    };
  }, []);
  useEffect(() => {
    if (disabled) changeOpen(false);
  }, [disabled, changeOpen]);
  const { refs, floatingStyles, context, isPositioned } = useFloating({
    open,
    onOpenChange: changeOpen,
    placement: 'bottom-end',
    strategy: 'fixed',
    whileElementsMounted: autoUpdate,
    middleware: [offset(4), flip({ padding: 8 }), shift({ padding: 8 })],
  });
  const setTrigger = useCallback(
    (element: HTMLButtonElement | null) => {
      trigger.current = element;
      refs.setReference(element);
      setPortalRoot(element?.closest<HTMLElement>('[role="dialog"]') ?? null);
    },
    [refs.setReference]
  );
  const click = useClick(context, { enabled: !disabled });
  const dismiss = useDismiss(context, { escapeKey: false });
  const role = useRole(context, { role: 'dialog' });
  const { getReferenceProps, getFloatingProps } = useInteractions([click, dismiss, role]);
  const favorite = t('conversation.resourceTags.favorite');
  const normalizedQuery = matchName(query);
  const visible = tags.filter((tag) => matchName(resourceTagLabel(tag, favorite)).includes(normalizedQuery));
  let validName = false;
  try {
    validName = normalizeResourceTagName(query).length > 0;
  } catch {
    /* An incomplete search is not a create command. */
  }
  const canCreate = validName && !tags.some((tag) => matchName(resourceTagLabel(tag, favorite)) === normalizedQuery);
  const options: Option[] = [
    ...visible.map((tag) => ({ key: `tag:${tag.id}`, tag })),
    ...(canCreate ? [{ key: 'create' }] : []),
  ];
  const activeIndex = Math.max(
    0,
    options.findIndex((option) => option.key === activeKey)
  );
  const activeId = options.length ? `${id}-option-${activeIndex}` : undefined;
  const selected = new Set(assignedIds);
  const message = error || (failure ? t('conversation.resourceTags.updateFailed') : undefined);
  useEffect(() => {
    if (open && activeId) document.getElementById(activeId)?.scrollIntoView?.({ block: 'nearest' });
  }, [open, activeId, normalizedQuery]);
  const activate = async (option: Option) => {
    if (disabled || pending.current.has(option.key) || (option.tag ? pendingTagIds.includes(option.tag.id) : creating))
      return;
    pending.current.add(option.key);
    const version = interaction.current;
    setFailure(false);
    try {
      if (option.tag) await onAssign(option.tag.id, !selected.has(option.tag.id));
      else await onCreate(query);
      if (!option.tag && mounted.current && version === interaction.current) {
        setQuery('');
        setActiveKey(undefined);
      }
    } catch {
      if (mounted.current && version === interaction.current) setFailure(true);
    } finally {
      pending.current.delete(option.key);
    }
  };
  const onKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      changeOpen(false);
      trigger.current?.focus({ preventScroll: true });
    } else if (event.key === 'Tab') {
      tabDismissal.current = true;
      trigger.current?.focus({ preventScroll: true });
      changeOpen(false);
    } else if (event.target === input.current && (event.key === 'ArrowDown' || event.key === 'ArrowUp')) {
      event.preventDefault();
      event.stopPropagation();
      if (options.length)
        setActiveKey(
          options[(activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length].key
        );
    } else if (event.target === input.current && event.key === 'Enter') {
      event.preventDefault();
      event.stopPropagation();
      if (options[activeIndex]) void activate(options[activeIndex]);
    }
  };
  return (
    <>
      <button
        ref={setTrigger}
        {...getReferenceProps()}
        type='button'
        className='resource-tag-menu__trigger'
        disabled={disabled}
        aria-label={t('conversation.resourceTags.manage')}
        title={t('conversation.resourceTags.manage')}
        aria-haspopup='dialog'
        aria-expanded={open}
        aria-controls={open ? id : undefined}
      >
        <span className='resource-tag-menu__trigger-content'>
          {triggerContent ?? <Tags size={14} aria-hidden='true' />}
        </span>
        <ChevronDown size={12} aria-hidden='true' />
      </button>
      {open && !disabled && (
        <FloatingPortal root={portalRoot ?? undefined}>
          <FloatingFocusManager
            context={context}
            modal={false}
            initialFocus={input}
            disabled={!isPositioned}
            returnFocus={!tabDismissal.current}
            restoreFocus
          >
            <section
              ref={refs.setFloating}
              {...getFloatingProps()}
              id={id}
              role='dialog'
              aria-modal='false'
              aria-label={t('conversation.resourceTags.manage')}
              data-library-tag-menu
              className='resource-tag-menu'
              onKeyDown={onKeyDown}
              style={{ ...floatingStyles, visibility: isPositioned ? 'visible' : 'hidden' }}
            >
              <div className='resource-tag-menu__heading'>{t('conversation.resourceTags.manage')}</div>
              <div className='resource-tag-menu__search'>
                <Search size={14} aria-hidden='true' />
                <input
                  ref={input}
                  role='combobox'
                  aria-autocomplete='list'
                  aria-expanded={open}
                  aria-controls={`${id}-list`}
                  aria-activedescendant={activeId}
                  aria-invalid={Boolean(message)}
                  aria-describedby={message ? `${id}-error` : undefined}
                  aria-label={t('conversation.resourceTags.search')}
                  placeholder={t('conversation.resourceTags.search')}
                  autoComplete='off'
                  maxLength={64}
                  value={query}
                  onChange={(event) => {
                    interaction.current++;
                    setQuery(event.target.value);
                    setActiveKey(undefined);
                    setFailure(false);
                  }}
                />
              </div>
              <div
                id={`${id}-list`}
                role='listbox'
                aria-multiselectable='true'
                aria-label={t('conversation.resourceTags.manage')}
                className='resource-tag-menu__options'
              >
                {options.map((option, index) => (
                  <div
                    key={option.key}
                    id={`${id}-option-${index}`}
                    role='option'
                    aria-selected={Boolean(option.tag && selected.has(option.tag.id))}
                    aria-disabled={disabled || (option.tag ? pendingTagIds.includes(option.tag.id) : creating)}
                    data-active={index === activeIndex || undefined}
                    className='resource-tag-menu__option'
                    onPointerMove={() => setActiveKey(option.key)}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => {
                      void activate(option);
                    }}
                  >
                    <span className='resource-tag-menu__check'>
                      {option.tag ? (
                        selected.has(option.tag.id) && <Check size={14} aria-hidden='true' />
                      ) : (
                        <Plus size={14} aria-hidden='true' />
                      )}
                    </span>
                    {option.tag ? (
                      <ResourceTagVisual tag={option.tag} favorite={favorite} />
                    ) : (
                      <span className='resource-tag-menu__create'>
                        {t('conversation.resourceTags.create', { name: query.trim() })}
                      </span>
                    )}
                  </div>
                ))}
                {!options.length && <p className='resource-tag-menu__empty'>{t('conversation.resourceTags.empty')}</p>}
              </div>
              {message && (
                <p id={`${id}-error`} role='alert' className='resource-tags__error'>
                  {message}
                </p>
              )}
            </section>
          </FloatingFocusManager>
        </FloatingPortal>
      )}
    </>
  );
}

/** This local view never cancels accepted commands or owns the catalog snapshot. */
export function ResourceTagMenu(props: ResourceTagMenuProps) {
  return <Menu key={props.targetKey} {...props} />;
}
