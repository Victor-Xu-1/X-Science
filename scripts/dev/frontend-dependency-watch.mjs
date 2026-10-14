#!/usr/bin/env node

import { renameSync, rmSync, watch, writeFileSync } from 'node:fs';

const [hostPidText, notifyPidText, readyFile, ...files] = process.argv.slice(2);
const hostPid = Number.parseInt(hostPidText ?? '', 10);
const notifyPid = Number.parseInt(notifyPidText ?? '', 10);

if (
  !Number.isSafeInteger(hostPid) ||
  hostPid <= 1 ||
  !Number.isSafeInteger(notifyPid) ||
  notifyPid <= 1 ||
  !readyFile ||
  files.length === 0
) {
  console.error('usage: frontend-dependency-watch.mjs HOST_PID NOTIFY_PID READY_FILE PACKAGE_FILE...');
  process.exit(2);
}

const watchers = [];
let closing = false;

function close(exitCode, message) {
  if (closing) return;
  closing = true;
  clearInterval(parentCheck);
  for (const watcher of watchers) watcher.close();
  if (message) console.error(message);
  process.exit(exitCode);
}

const parentCheck = setInterval(() => {
  try {
    process.kill(hostPid, 0);
  } catch {
    close(0);
  }
}, 250);
parentCheck.unref();

for (const file of files) {
  try {
    const watcher = watch(file, { persistent: true }, (eventType) => {
      const message = `[source-frontend] dependency metadata changed: ${file} (${eventType})`;
      try {
        process.kill(notifyPid, 'SIGUSR1');
      } catch (error) {
        close(1, `${message}; cannot notify host process group: ${error.message}`);
      }
    });
    watcher.on('error', (error) => {
      close(1, `[source-frontend] dependency watcher failed for ${file}: ${error.message}`);
    });
    watchers.push(watcher);
  } catch (error) {
    close(1, `[source-frontend] cannot watch dependency metadata ${file}: ${error.message}`);
  }
}

process.once('SIGINT', () => close(0));
process.once('SIGTERM', () => close(0));

// The host must not perform its install-to-watch fingerprint check until
// every subscription exists. Publish one atomic receipt for this group only.
const temporary = `${readyFile}.tmp.${process.pid}`;
try {
  writeFileSync(temporary, `watching:${notifyPid}\n`, { mode: 0o600 });
  renameSync(temporary, readyFile);
} catch (error) {
  try {
    rmSync(temporary, { force: true });
  } catch (cleanupError) {
    console.error(`[source-frontend] cannot remove this watcher's temporary receipt: ${cleanupError.message}`);
  }
  close(1, `[source-frontend] cannot publish dependency watcher readiness: ${error.message}`);
}
