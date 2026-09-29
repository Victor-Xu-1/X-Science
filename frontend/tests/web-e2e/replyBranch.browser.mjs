import assert from "node:assert/strict";
import { createServer } from "vite";
import { resolve } from "node:path";
import { chromium, expect } from "@playwright/test";

const api = process.env.SYNON_BRANCH_API;
const fixture = JSON.parse(process.env.SYNON_BRANCH_FIXTURE);
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(api))
  throw new Error("Controlled local API required");
let server, browser;
try {
  server = await createServer({
    configFile: resolve("vite.config.ts"),
    server: {
      host: "127.0.0.1",
      port: 0,
      hmr: false,
      proxy: { "/api": { target: api, changeOrigin: false } },
      fs: { allow: [process.cwd()] },
    },
    plugins: [
      {
        name: "reply-branch-regression",
        configureServer(vite) {
          vite.middlewares.use("/__branch_fixture__", (_req, res) => {
            res.setHeader("content-type", "application/json");
            res.end(JSON.stringify(fixture));
          });
          vite.middlewares.use("/__reply_branch__", async (_req, res, next) => {
            try {
              const html = await vite.transformIndexHtml(
                "/__reply_branch__",
                `<html lang="zh-CN" data-theme="light" data-color-scheme="default"><body arco-theme="light"><div id="root"></div><script type="module" src="/@fs/${resolve(
                  "tests/web-e2e/replyBranch.fixture.tsx"
                )}"></script></body></html>`
              );
              res.setHeader("content-type", "text/html; charset=utf-8");
              res.end(html);
            } catch (error) {
              next(error);
            }
          });
        },
      },
    ],
  });
  await server.listen();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({
    viewport: { width: 1100, height: 780 },
  });
  const errors = [],
    writes = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("response", async (response) => {
    if (response.status() >= 400)
      console.error(
        "HTTP failure",
        response.status(),
        response.url(),
        (await response.text()).slice(0, 500)
      );
  });
  page.on("request", (r) => {
    if (r.method() === "POST") writes.push(r.url());
  });
  const url = `http://127.0.0.1:${
    server.httpServer.address().port
  }/__reply_branch__#/conversation/${fixture.source}`;
  await page.goto(url);
  const button = page.getByRole("button", { name: "从此回复分支到新会话" });
  await expect(button).toBeEnabled({ timeout: 20000 });
  await expect(page.getByTestId("history")).toContainText("Next round");
  const source = await page.getByTestId("history").innerText();
  await button.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("conversation-id")).not.toHaveText(
    fixture.source
  );
  await expect(page.getByTestId("history")).toContainText("Round ended");
  await expect(page.getByTestId("history")).not.toContainText("Next round");
  const target = await page.getByTestId("conversation-id").innerText();
  assert.match(target, /^[a-f0-9-]{36}$/);
  const branched = await page.getByTestId("history").innerText();
  await expect(page.getByTestId("inline-scientific-files")).toContainText(
    "branch-result.txt"
  );
  await page.getByRole("button", { name: "调用", exact: true }).press("Enter");
  await expect(page.getByTestId("round-usage-panel")).toContainText(
    "controlled-audit-model"
  );
  await expect(page.getByTestId("round-usage-panel")).toContainText("110");
  assert.equal(
    await page
      .getByRole("button", { name: "调用", exact: true })
      .evaluate((el) => getComputedStyle(el).borderBottomWidth),
    "0px"
  );
  const content = await page.request.get(new URL(fixture.artifact.content_url, url).href);
  assert.equal(content.status(), 200);
  assert.match(await content.text(), /controlled branch fixture/);
  await page.reload();
  await expect(page.getByTestId("history")).toHaveText(branched);
  await expect(page.getByTestId("inline-scientific-files")).toContainText(
    "branch-result.txt"
  );
  await page.goto(url);
  await expect(page.getByTestId("history")).toHaveText(source);
  assert.equal(
    writes.filter((url) => url.endsWith("/api/conversations/clone")).length,
    1
  );
  assert.equal(
    writes.filter((url) => /\/messages$|\/resume$|\/ensure$/.test(url)).length,
    0,
    JSON.stringify(writes)
  );
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      passed: true,
      target,
      browser: await browser.version(),
      checks: [
        "keyboard action",
        "real HTTP and SQLite",
        "completed prefix only",
        "source unchanged",
        "refresh persistence",
        "inherited immutable file card and content",
        "inherited usage and borderless trigger",
        "one create",
        "no runner dispatch",
      ],
    })
  );
} finally {
  if (browser) await browser.close();
  if (server) await server.close();
}
