/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Keep callback identities stable across streaming updates that do not change
 * derived membership. A new Map/Set every tick would refresh Virtuoso's
 * itemContent and force every visible non-memoized row to render again.
 */
export function reuseIfEqual<T>(previous: T | undefined, next: T, equal: (left: T, right: T) => boolean): T {
  return previous !== undefined && equal(previous, next) ? previous : next;
}

export function areStringNumberMapsEqual(left: Map<string, number>, right: Map<string, number>): boolean {
  if (left === right) return true;
  if (left.size !== right.size) return false;
  for (const [key, value] of left) {
    if (right.get(key) !== value) return false;
  }
  return true;
}

export function areStringSetsEqual(left: Set<string>, right: Set<string>): boolean {
  if (left === right) return true;
  if (left.size !== right.size) return false;
  for (const value of left) {
    if (!right.has(value)) return false;
  }
  return true;
}
