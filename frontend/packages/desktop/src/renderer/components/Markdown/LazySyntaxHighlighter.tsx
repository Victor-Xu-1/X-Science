/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useEffect, useState } from 'react';

type HighlighterComponent = React.ComponentType<Record<string, unknown>>;

type LazySyntaxHighlighterProps = Record<string, unknown> & {
  children?: React.ReactNode;
  customStyle?: React.CSSProperties;
  codeTagProps?: { style?: React.CSSProperties };
  PreTag?: keyof React.JSX.IntrinsicElements | React.ComponentType<Record<string, unknown>>;
};

let highlighterPromise: Promise<HighlighterComponent> | null = null;
let loadedHighlighter: HighlighterComponent | null = null;

const loadHighlighter = (): Promise<HighlighterComponent> => {
  highlighterPromise ??= import('./CodeHighlighter').then((module) => {
    loadedHighlighter = module.default;
    return module.default;
  });
  return highlighterPromise;
};

/**
 * Warm the highlighter chunk without rendering it (e.g. once a conversation
 * with code is known to be present).
 */
export const preloadSyntaxHighlighter = (): void => {
  void loadHighlighter().catch(() => {
    highlighterPromise = null;
  });
};

/**
 * Deterministic stand-in rendered for the brief window before the highlighter
 * chunk arrives. Same code text, same block metrics; the colored tokens swap in
 * when the real component is ready.
 */
const PlainCodeFallback: React.FC<LazySyntaxHighlighterProps> = ({ children, customStyle, codeTagProps, PreTag }) => {
  const Tag = (typeof PreTag === 'string' ? PreTag : 'div') as 'div';
  return (
    <Tag
      style={{
        margin: 0,
        whiteSpace: 'pre',
        overflowX: 'auto',
        color: 'var(--text-primary)',
        background: 'transparent',
        ...codeTagProps?.style,
        ...customStyle,
      }}
    >
      {children}
    </Tag>
  );
};

const LazySyntaxHighlighter: React.FC<LazySyntaxHighlighterProps> = (props) => {
  const [Highlighter, setHighlighter] = useState<HighlighterComponent | null>(() => loadedHighlighter);

  useEffect(() => {
    let active = true;
    const promise = loadHighlighter();
    // Always subscribe, including when another instance filled the shared cache
    // after this component rendered. Observe rejection on the same promise chain.
    void promise.then(
      (component) => {
        // Function components must be wrapped; setState(fn) is an updater.
        if (active) setHighlighter(() => component);
      },
      () => {
        if (highlighterPromise === promise) highlighterPromise = null;
        console.warn('[LazySyntaxHighlighter] Highlighting could not load; source text remains available.');
      }
    );
    return () => {
      active = false;
    };
  }, []);

  return Highlighter ? <Highlighter {...props} /> : <PlainCodeFallback {...props} />;
};

LazySyntaxHighlighter.displayName = 'LazySyntaxHighlighter';

export default React.memo(LazySyntaxHighlighter);
