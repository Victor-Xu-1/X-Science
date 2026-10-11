/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import SyntaxHighlighter from 'react-syntax-highlighter';

/**
 * Isolation module for the full highlight.js-backed `react-syntax-highlighter`
 * build. It is only loaded through the dynamic import in `LazySyntaxHighlighter`
 * so conversations without code blocks never fetch it.
 */
export default SyntaxHighlighter;
