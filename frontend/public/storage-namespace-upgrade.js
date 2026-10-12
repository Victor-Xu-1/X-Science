// One startup authority for the first-party browser namespace transition.
// Old names below are exact migration input facts, never current write keys.
(function () {
  'use strict';
  var exact = [
    ['synon-ai_workspace_collapse_state', 'x-science_workspace_collapse_state'],
    ['synon-ai_sider_collapsed', 'x-science_sider_collapsed'],
    ['synon-ai_theme', 'x-science_theme'],
    ['synon-ai_language', 'x-science_language'],
    ['__synon-ai_theme', '__x-science_theme'],
    ['__synon-ai_theme_family', '__x-science_theme_family'],
    ['synon-biomed:user-profiles:v1', 'x-science:user-profiles:v1'],
    ['synonbiomed.onboarding.pending-project.v1', 'xscience.onboarding.pending-project.v1'],
    ['synonbiomed.auth.returnPath', 'xscience.auth.returnPath'],
    ['synonbiomed.auth.sessionExpired', 'xscience.auth.sessionExpired'],
    ['synon.webChunkRecovery.v1', 'x-science.webChunkRecovery.v1'],
    ['synon-ai.emoji.recent', 'x-science.emoji.recent'],
    ['synon-ai:recent-workspaces', 'x-science:recent-workspaces'],
    ['synon-ai.sttStreamUnsupported', 'x-science.sttStreamUnsupported'],
    ['synon-ai:desktop-sider-width', 'x-science:desktop-sider-width'],
    ['synon.language.preAuthOverride', 'x-science.language.preAuthOverride'],
    ['synon-ai_preview_tabs', 'x-science_preview_tabs'],
    ['synon-ai_preview_active_tab_id', 'x-science_preview_active_tab_id'],
    ['synon-ai_preview_state', 'x-science_preview_state'],
    ['synon-ai_preview_board_columns', 'x-science_preview_board_columns'],
    ['synon.performance.conversation-route', 'x-science.performance.conversation-route'],
    ['synon-ai_workspace_update_time', 'x-science_workspace_update_time'],
    ['synon-ai_workspace_expansion', 'x-science_workspace_expansion'],
    ['synonbiomed.active-project-id', 'xscience.active-project-id'],
    ['synon-ai:last-non-settings-path', 'x-science:last-non-settings-path'],
    ['synon-biomed:dev-service-worker-cleanup', 'x-science:dev-service-worker-cleanup'],
    ['synon.appSessionToken', 'x-science.appSessionToken'],
    ['synon.link.autoConnect', 'x-science.link.autoConnect'],
  ];
  var prefixes = [
    ['synon-biomed.task-status.presentation.v5.', 'x-science.task-status.presentation.v5.'],
    ['synon.realtime.cursor.v1:', 'x-science.realtime.cursor.v1:'],
    ['synonbiomed.composer.draft.v1:', 'xscience.composer.draft.v1:'],
    ['synonbiomed.onboarding.draft.v1:', 'xscience.onboarding.draft.v1:'],
  ];
  var report = { version: 1, migrated: 0, failures: [], cachePending: false };
  window.__xScienceStorageNamespaceUpgrade = report;

  function failure(area, code) {
    if (report.failures.length < 32) report.failures.push({ area: area, code: code });
  }
  function notify() {
    if (report.failures.length) {
      // No stored values, opaque keys, browser exceptions or credentials.
      console.warn('[X-Science storage upgrade]', { failures: report.failures.length });
    }
    if (typeof window.dispatchEvent === 'function' && typeof window.Event === 'function') {
      window.dispatchEvent(new window.Event('x-science:storage-upgrade-status'));
    }
  }
  function move(storage, area, source, target) {
    try {
      var original = storage.getItem(source);
      if (original === null) return;
      var current = storage.getItem(target);
      if (current !== null && current !== original) return failure(area, 'value_conflict');
      if (current === null) storage.setItem(target, original);
      if (storage.getItem(target) !== original) return failure(area, 'write_not_verified');
      if (storage.getItem(source) !== original) return failure(area, 'source_changed');
      if (storage.getItem(target) !== original) return failure(area, 'target_changed');
      storage.removeItem(source);
      if (storage.getItem(source) !== null) return failure(area, 'delete_not_verified');
      report.migrated += 1;
    } catch (_) {
      failure(area, 'storage_operation_failed');
    }
  }
  function upgrade(area) {
    var storage;
    try {
      storage = window[area];
      if (!storage) return failure(area, 'storage_unavailable');
      var count = storage.length;
      if (!Number.isInteger(count) || count < 0 || count > 4096) return failure(area, 'inventory_limit');
      // Freeze keys before moving entries: removeItem changes storage indexes.
      var keys = [];
      for (var index = 0; index < count; index += 1) {
        var key = storage.key(index);
        if (typeof key === 'string' && key.length <= 8192) keys.push(key);
        else return failure(area, 'inventory_changed');
      }
      if (storage.length !== count) return failure(area, 'inventory_changed');
      exact.forEach(function (pair) {
        move(storage, area, pair[0], pair[1]);
      });
      keys.forEach(function (key) {
        for (var index = 0; index < prefixes.length; index += 1) {
          var pair = prefixes[index];
          if (key.startsWith(pair[0]) && key.length > pair[0].length) {
            move(storage, area, key, pair[1] + key.slice(pair[0].length));
            return;
          }
        }
      });
    } catch (_) {
      failure(area, 'storage_unavailable');
    }
  }
  upgrade('localStorage');
  upgrade('sessionStorage');
  notify();

  // CacheStorage retirement only follows a response from the actual current
  // controller. The preceding worker serves the same sw.js URL, so its URL
  // alone cannot prove that the new cache namespace has claimed this page.
  var serviceWorker = window.navigator && window.navigator.serviceWorker;
  if (!serviceWorker || !window.caches) return;
  var disposed = false;
  var lastProbed = null;
  var retiring = false;
  var retired = false;
  function probe() {
    if (disposed || retired || !serviceWorker.controller || lastProbed === serviceWorker.controller) return;
    lastProbed = serviceWorker.controller;
    report.cachePending = true;
    try {
      lastProbed.postMessage({ type: 'x-science-cache-namespace-probe-v1' });
    } catch (_) {
      report.cachePending = false;
      failure('cacheStorage', 'cache_probe_failed');
      notify();
    }
  }
  function onMessage(event) {
    if (
      disposed ||
      retiring ||
      retired ||
      !serviceWorker.controller ||
      event.source !== serviceWorker.controller ||
      event.source !== lastProbed ||
      !event.data ||
      event.data.type !== 'x-science-cache-namespace-ready-v1' ||
      event.data.cache !== 'x-science-webui-v2'
    )
      return;
    retiring = true;
    window.caches
      .keys()
      .then(function (keys) {
        if (disposed || event.source !== serviceWorker.controller || !keys.includes('x-science-webui-v2')) {
          throw new Error('cache_claim_changed');
        }
        return Promise.all(
          keys
            .filter(function (key) {
              return key.startsWith('synon-ai-webui-') || key === 'synon-biomed-structure-thumbnails-v1';
            })
            .map(function (key) {
              return window.caches.delete(key);
            })
        );
      })
      .then(function (deleted) {
        if (
          deleted.some(function (value) {
            return value !== true;
          })
        )
          throw new Error('cache_delete_unverified');
        retired = true;
        report.cachePending = false;
        dispose();
        notify();
      })
      .catch(function () {
        report.cachePending = false;
        failure('cacheStorage', 'cache_retirement_failed');
        notify();
      })
      .finally(function () {
        retiring = false;
      });
  }
  function dispose() {
    disposed = true;
    serviceWorker.removeEventListener('message', onMessage);
    serviceWorker.removeEventListener('controllerchange', probe);
    window.removeEventListener('pagehide', dispose);
  }
  serviceWorker.addEventListener('message', onMessage);
  serviceWorker.addEventListener('controllerchange', probe);
  window.addEventListener('pagehide', dispose, { once: true });
  probe();
})();
