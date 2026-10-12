import assert from 'node:assert/strict';
import { lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { basename, dirname, isAbsolute, join, normalize, posix, relative, resolve, win32 } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const sourceRoot = resolve(frontendRoot, '..');
const fixtureEntry = join(frontendRoot, 'tests/web-e2e/artifactLinks.fixture.html');
const reportLimit = 64 * 1024;

// This CLI is a synthetic renderer/resolver/native-preview integration backed by
// real local HTTP and SQLite. It is not full official-Windows scientific E2E.
// Local CLI users must already have official stable Google Chrome installed at
// Playwright's supported OS location. There is no install or browser fallback.
export function requireOfficialArtifactChromeIdentity(
  { product, commandLine, tempDirectory = tmpdir() },
  platform = process.platform
) {
  assert.ok(['linux', 'darwin', 'win32'].includes(platform), 'Unsupported installed-Chrome platform');
  assert.match(product, /^Chrome\/\d+\.\d+\.\d+\.\d+$/, 'Chrome CDP product required');
  assert.ok(
    Array.isArray(commandLine) && commandLine.length > 1 && commandLine.length <= 128,
    'Bounded CDP browser command line required'
  );
  assert.ok(commandLine.every((value) => typeof value === 'string' && value.length <= 2048));
  assert.ok(Buffer.byteLength(JSON.stringify(commandLine)) <= 24 * 1024, 'CDP command line too large');
  const path = platform === 'win32' ? win32 : posix;
  const executable = commandLine[0];
  assert.ok(path.isAbsolute(executable), 'Absolute official Chrome executable required');
  assert.equal(path.normalize(executable), executable, 'Normalized Chrome executable required');
  const officialExecutable =
    platform === 'linux'
      ? executable === '/opt/google/chrome/chrome'
      : platform === 'darwin'
        ? executable === '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
        : /\\Google\\Chrome\\Application\\chrome\.exe$/i.test(executable);
  assert.ok(officialExecutable, 'Unexpected official stable Google Chrome executable');
  assert.ok(
    !commandLine.some(
      (argument) =>
        /^--(?:no-sandbox|no-zygote|single-process|disable-(?:[\w-]*-)?sandbox)(?:=|$)/i.test(argument) ||
        /^--disable-features=.*sandbox/i.test(argument)
    ),
    'Sandbox-disabling browser flags are forbidden'
  );
  const profileArguments = commandLine.filter(
    (argument) => argument === '--user-data-dir' || argument.startsWith('--user-data-dir=')
  );
  assert.equal(profileArguments.length, 1, 'Exactly one ephemeral browser profile required');
  assert.ok(profileArguments[0].startsWith('--user-data-dir='), 'Explicit temporary profile required');
  const userDataDir = profileArguments[0].slice('--user-data-dir='.length);
  assert.ok(path.isAbsolute(userDataDir), 'Absolute temporary browser profile required');
  assert.equal(path.normalize(userDataDir), userDataDir, 'Normalized temporary browser profile required');
  assert.match(path.basename(userDataDir), /^playwright_chromiumdev_profile-[A-Za-z0-9]{6}$/);
  assert.ok(path.isAbsolute(tempDirectory), 'Absolute OS temporary directory required');
  const comparable = (value) => (platform === 'win32' ? value.toLowerCase() : value);
  assert.equal(
    comparable(path.dirname(userDataDir)),
    comparable(path.resolve(tempDirectory)),
    'Browser profile must be a direct Playwright-generated child of the OS temporary directory'
  );
  return { product, executable, commandLine: [...commandLine], userDataDir };
}

function within(directory, candidate) {
  const path = relative(directory, candidate);
  return (
    path === '' ||
    (path !== '..' && !path.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`) && !isAbsolute(path))
  );
}

async function evidenceDirectory() {
  const requested = process.env.SYNON_ARTIFACT_LINK_EVIDENCE;
  const realSource = await realpath(sourceRoot);
  const root = requested === undefined ? resolve(tmpdir()) : requested;
  assert.ok(
    isAbsolute(root) && normalize(root) === root && resolve(root) === root,
    'SYNON_ARTIFACT_LINK_EVIDENCE must be a normalized absolute directory'
  );
  assert.ok(!within(sourceRoot, root), 'Browser evidence must stay outside source');
  // Check existing ancestors before mkdir, so a symlink cannot redirect writes
  // into the checkout. Check the created root's real location again afterwards.
  let ancestor = root;
  for (;;) {
    try {
      assert.ok(!within(realSource, await realpath(ancestor)), 'Browser evidence resolves inside source');
      break;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      const parent = dirname(ancestor);
      if (parent === ancestor) throw error;
      ancestor = parent;
    }
  }
  await mkdir(root, { recursive: true });
  const realRoot = await realpath(root);
  assert.ok(!within(realSource, realRoot), 'Browser evidence resolves inside source');
  return mkdtemp(join(realRoot, 'synon-artifact-links-'));
}

function boundedText(value, limit = 4096) {
  let text = String(value);
  while (Buffer.byteLength(JSON.stringify(text)) > limit) {
    text = text.slice(0, Math.floor(text.length * 0.75));
  }
  return text;
}

function boundedError(error) {
  return boundedText(error?.stack || error);
}

async function main() {
  // The override is an evidence root, not a source/cache directory. Each run
  // owns a fresh child directory; CI can upload only its runner-temp root.
  const artifacts = await evidenceDirectory();
  const report = {
    schemaVersion: 1,
    result: 'failed',
    scope: 'synthetic artifact-link integration; not full official-Windows scientific E2E',
    platform: process.platform,
    nodeVersion: process.version,
    artifacts,
    launch: { channel: 'chrome', headless: true, chromiumSandbox: true, profile: 'ephemeral' },
    checks: [],
    screenshots: [],
  };
  let server, browser, page, cacheDir, failure;
  const errors = [],
    requests = [],
    downloads = [];
  try {
    const api = process.env.SYNON_ARTIFACT_LINK_API;
    const fixture = JSON.parse(process.env.SYNON_ARTIFACT_LINK_FIXTURE);
    if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(api)) throw new Error('Controlled local API required');
    const { createServer, normalizePath } = await import('vite');
    const { chromium, expect } = await import('@playwright/test');
    const require = createRequire(import.meta.url);
    report.playwrightVersion = require('@playwright/test/package.json').version;
    const ownedHtml = await readFile(fixtureEntry, 'utf8');
    const fixtureModuleUrl = `/@fs/${normalizePath(join(frontendRoot, 'tests/web-e2e/artifactLinks.fixture.tsx'))}`;
    assert.ok(ownedHtml.includes('src="./artifactLinks.fixture.tsx"'), 'Owned fixture module entry required');
    // Dependency optimizer data is neither source nor uploaded test evidence.
    cacheDir = await mkdtemp(join(tmpdir(), 'synon-artifact-links-vite-'));
    server = await createServer({
      configFile: join(frontendRoot, 'vite.config.ts'),
      cacheDir,
      // Scanning the owned HTML discovers fixture dependencies before rendering,
      // rather than discovering them in an inline, middleware-only document.
      optimizeDeps: { entries: [fixtureEntry] },
      server: {
        host: '127.0.0.1',
        port: 0,
        hmr: false,
        proxy: { '/api': { target: api } },
        fs: { allow: [frontendRoot] },
      },
      plugins: [
        {
          name: 'artifact-link-regression',
          configureServer(vite) {
            vite.middlewares.use('/__artifact_fixture__', (_request, response) => {
              response.setHeader('content-type', 'application/json');
              response.end(JSON.stringify(fixture));
            });
            vite.middlewares.use('/__artifact_links__', async (_request, response, next) => {
              try {
                // Keep the public fixture URL while making its owned relative
                // module entry portable across Linux, macOS, and Windows.
                const html = await vite.transformIndexHtml(
                  '/__artifact_links__',
                  ownedHtml.replace('src="./artifactLinks.fixture.tsx"', `src="${fixtureModuleUrl}"`)
                );
                response.setHeader('content-type', 'text/html; charset=utf-8');
                response.end(html);
              } catch (error) {
                next(error);
              }
            });
          },
        },
      ],
    });
    await server.listen();
    const realTemp = await realpath(tmpdir());
    const profilesBeforeLaunch = new Set(await readdir(realTemp));
    // launch(), with no persistent context/profile override, creates a fresh
    // Playwright temporary profile. Never retry with another browser or sandbox.
    browser = await chromium.launch({ channel: 'chrome', headless: true, chromiumSandbox: true });
    report.browserVersion = boundedText(await browser.version(), 128);
    const cdp = await browser.newBrowserCDPSession();
    try {
      const version = await cdp.send('Browser.getVersion');
      report.browserIdentity = { product: boundedText(version.product, 128) };
      const { arguments: commandLine } = await cdp.send('Browser.getBrowserCommandLine');
      // Retain bounded identity evidence even when validation rejects the launch.
      report.browserIdentity = {
        product: boundedText(version.product, 128),
        commandLine: commandLine.slice(0, 64).map((argument) => boundedText(argument, 256)),
      };
      const identity = requireOfficialArtifactChromeIdentity({ product: version.product, commandLine });
      assert.equal(version.product, `Chrome/${report.browserVersion}`, 'Browser versions disagree');
      assert.ok(!profilesBeforeLaunch.has(basename(identity.userDataDir)), 'Reused browser profile forbidden');
      const profileStat = await lstat(identity.userDataDir);
      assert.ok(
        profileStat.isDirectory() && !profileStat.isSymbolicLink(),
        'Real temporary profile directory required'
      );
      assert.equal(
        relative(realTemp, dirname(await realpath(identity.userDataDir))),
        '',
        'Profile resolves outside temporary directory'
      );
      report.browserIdentity = { ...identity, freshTemporaryProfile: true };
    } finally {
      await cdp.detach();
    }
    page = await browser.newPage({ viewport: { width: 1200, height: 850 } });
    page.on('pageerror', (error) => errors.push(error.message));
    page.on('pageerror', (error) => console.error('Browser error:', error.stack));
    page.on('response', (response) => {
      if (response.status() >= 400) console.error('HTTP failure:', response.status(), response.url());
    });
    page.on('request', (request) => requests.push(request.url()));
    page.on('download', (download) => downloads.push(download.suggestedFilename()));
    const url = `http://127.0.0.1:${server.httpServer.address().port}/__artifact_links__#/conversation/${fixture.conversation}`;
    await page.goto(url);
    const latest = page.getByRole('link', { name: 'Latest report', exact: true });
    await expect(latest).toHaveAttribute('data-artifact-link-resolution', 'resolved', { timeout: 20_000 });
    const style = await latest.evaluate((element) => ({
      color: getComputedStyle(element).color,
      weight: getComputedStyle(element).fontWeight,
    }));
    const rgb = style.color.match(/\d+/g).map(Number);
    assert.ok(rgb[2] > rgb[0] && Number(style.weight) >= 600, JSON.stringify(style));
    const pageCount = page.context().pages().length;
    await latest.focus();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('preview-identity')).toContainText(fixture.latest.version_id);
    await expect(page.getByText('controlled branch fixture new-version', { exact: true })).toBeVisible();
    await page.screenshot({ path: join(artifacts, 'native-preview.png'), fullPage: true });
    report.screenshots.push('native-preview.png');
    await page.getByRole('button', { name: 'Close test preview' }).click();
    await page.getByRole('link', { name: 'Exact old report', exact: true }).click();
    await expect(page.getByTestId('preview-identity')).toContainText(fixture.old.version_id);
    await expect(page.getByText('controlled branch fixture old-version', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Close test preview' }).click();
    await page.getByRole('link', { name: 'Unknown artifact', exact: true }).click();
    await expect(page.getByTestId('preview-identity')).toHaveText('closed');
    await expect(page.getByText('文件预览失败', { exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Unknown artifact', exact: true })).toHaveAttribute(
      'data-artifact-link-resolution',
      'unavailable'
    );
    assert.equal(page.url(), url);
    assert.equal(page.context().pages().length, pageCount);
    assert.deepEqual(downloads, []);
    assert.deepEqual(errors, []);
    assert.ok(
      requests.every((value) => new URL(value).origin === new URL(url).origin),
      'model origin was fetched'
    );
    await page.reload();
    await expect(latest).toHaveAttribute('data-artifact-link-resolution', 'resolved');
    assert.equal(page.url(), url);
    assert.equal(page.context().pages().length, pageCount);
    assert.deepEqual(downloads, []);
    assert.deepEqual(errors, []);
    assert.ok(
      requests.every((value) => new URL(value).origin === new URL(url).origin),
      'model origin was fetched after reload'
    );
    report.result = 'passed';
    report.checks = [
      'production renderer/resolver/native preview',
      'real HTTP and SQLite',
      'blue bold links',
      'keyboard',
      'immutable old version',
      'unknown identity contained',
      'no new tab/download/origin fetch',
      'reload',
    ];
  } catch (error) {
    failure = error;
    report.error = boundedError(error);
    if (page && !page.isClosed()) {
      try {
        await page.screenshot({ path: join(artifacts, 'failure.png'), timeout: 5000 });
        report.screenshots.push('failure.png');
      } catch (screenshotError) {
        report.screenshotError = boundedError(screenshotError);
      }
    }
  } finally {
    for (const close of [
      () => browser?.close(),
      () => server?.close(),
      () => cacheDir && rm(cacheDir, { recursive: true, force: true }),
    ]) {
      try {
        await close();
      } catch (error) {
        failure ??= error;
        report.result = 'failed';
        (report.cleanupErrors ??= []).push(boundedError(error));
      }
    }
    report.requestCount = requests.length;
    report.pageErrors = errors.slice(0, 5).map((error) => boundedText(error, 512));
    report.downloads = downloads.slice(0, 5).map((filename) => boundedText(filename, 512));
    const serialized = JSON.stringify(report, null, 2);
    assert.ok(Buffer.byteLength(serialized) + 1 <= reportLimit, 'Browser evidence report exceeded bound');
    await writeFile(join(artifacts, 'report.json'), `${serialized}\n`, { flag: 'wx' });
    console.log(
      JSON.stringify({ result: report.result, browser: report.browserVersion, artifacts, checks: report.checks })
    );
  }
  if (failure) throw failure;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => {
    console.error(boundedError(error));
    process.exitCode = 1;
  });
}
