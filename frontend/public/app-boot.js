// The native reload link is already present in HTML. This small independent
// script adds truthful slow/failed states even when the main module never loads.
(function () {
  var root = document.getElementById('root');
  var boot = document.querySelector('.app-boot-loading');
  var configuration = document.getElementById('app-boot-config');
  if (!root || !boot || !configuration) return;
  var config;
  try {
    config = JSON.parse(configuration.textContent);
  } catch {
    return; // The server-rendered message and native reload link remain usable.
  }

  var language = config.defaultLanguage;
  try {
    var hint = localStorage.getItem('i18nextLng');
    if (hint) {
      var normalized = hint.replace(/_/g, '-').toLowerCase();
      language =
        Object.keys(config.locales).find(function (locale) {
          return locale.toLowerCase() === normalized || locale.split('-')[0] === normalized.split('-')[0];
        }) || language;
    }
  } catch {
    // Hardened storage must not block the shared default language.
  }
  var copy = config.locales[language];
  if (!copy) return;
  document.documentElement.lang = language;
  var label = boot.querySelector('[data-app-boot-label]');
  var description = boot.querySelector('[data-app-boot-description]');
  var reload = boot.querySelector('[data-app-boot-reload]');
  var disposed = false;
  var failed = false;
  var timer;
  var observer;

  function pending() {
    return !disposed && boot.isConnected && root.contains(boot);
  }
  function present(state) {
    if (!pending()) return;
    label.textContent = copy[state].replace(/\{\{productName\}\}/g, config.productName);
    description.textContent = state === 'loading' ? '' : copy[state + 'Description'];
    description.hidden = state === 'loading';
    boot.setAttribute('role', state === 'failed' ? 'alert' : 'status');
    boot.setAttribute('data-state', state);
    reload.textContent = copy.reload;
  }
  function onFailure(event) {
    if (!pending()) return;
    if (
      event.type === 'error' &&
      !(event instanceof window.ErrorEvent) &&
      !(event.target instanceof window.HTMLScriptElement && event.target.hasAttribute('data-app-entry'))
    )
      return;
    failed = true;
    present('failed');
  }
  function onReload(event) {
    if (!pending()) return;
    event.preventDefault();
    window.location.reload();
  }
  function dispose() {
    if (disposed) return;
    disposed = true;
    window.clearTimeout(timer);
    if (observer) observer.disconnect();
    window.removeEventListener('error', onFailure, true);
    window.removeEventListener('unhandledrejection', onFailure);
    window.removeEventListener('vite:preloadError', onFailure);
    window.removeEventListener('pagehide', dispose);
    reload.removeEventListener('click', onReload);
  }

  present('loading');
  reload.addEventListener('click', onReload);
  timer = window.setTimeout(function () {
    if (!failed) present('slow'); // A slow load is not a confirmed failure.
  }, 30_000);
  observer = new window.MutationObserver(function () {
    if (!pending()) dispose();
  });
  observer.observe(root, { childList: true });
  window.addEventListener('error', onFailure, true);
  window.addEventListener('unhandledrejection', onFailure);
  window.addEventListener('vite:preloadError', onFailure);
  window.addEventListener('pagehide', dispose, { once: true });
})();
