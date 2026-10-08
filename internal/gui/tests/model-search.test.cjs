// Per-model search answers are staged with Names & levels, not posted on
// each tick. Restore default inherits the provider-wide answer, and a
// Chat-only provider says visibly why a saved on answer cannot be used.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(process.env.MAGPIE_MODEL_SEARCH_ASSETS || path.join(__dirname, "../assets"));
const words = {
  en: { names: "Names & levels", search: "Searches the web by itself", unsaved: "unsaved", save: "Save", cancel: "Cancel", reset: "Restore default", more: "More endpoints", anthropic: "Anthropic URL", needs: "Needs an Anthropic or Responses URL; magpie searches for this model instead" },
  zh: { names: "名称与推理档位", search: "自己能联网搜索", unsaved: "未保存", save: "保存", cancel: "取消", reset: "恢复默认", more: "更多端点", anthropic: "Anthropic 地址", needs: "需要 Anthropic 或 Responses 地址；当前由 magpie 代为搜索" },
  "zh-TW": { names: "名稱與推理檔位", search: "自己能聯網搜尋", unsaved: "未儲存", save: "儲存", cancel: "取消", reset: "恢復預設", more: "更多端點", anthropic: "Anthropic 位址", needs: "需要 Anthropic 或 Responses 位址；目前由 magpie 代為搜尋" },
  ja: { names: "名前と推論レベル", search: "自分でウェブ検索する", unsaved: "未保存", save: "保存", cancel: "キャンセル", reset: "デフォルトに戻す", more: "その他のエンドポイント", anthropic: "Anthropic URL", needs: "Anthropic または Responses の URL が必要です。現在は magpie がこのモデルの代わりに検索します" },
  de: { names: "Namen & Stufen", search: "Sucht selbst im Web", unsaved: "nicht gespeichert", save: "Speichern", cancel: "Abbrechen", reset: "Standard wiederherstellen", more: "Weitere Endpunkte", anthropic: "Anthropic-URL", needs: "Benötigt eine Anthropic- oder Responses-URL; stattdessen sucht magpie für dieses Modell" },
};

function serve(lang, posts) {
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  const base = { icon: "generic", agents: [], key: { set: true, masked: "test-key" }, ready: true, chat: "https://relay.example/v1", responses: "", anthropic: "" };
  const providers = {
    providers: [
      { ...base, id: "relay", name: "Relay", anthropic: "https://relay.example", models: [
        { id: "inherited", name: "Inherited", on: true, searches: true, ownSearches: true },
        { id: "off", name: "Off", on: true, searches: false, searchSet: true, ownSearches: true },
        { id: "tail", name: "Tail", on: true, searches: false, ownSearches: false },
      ] },
      { ...base, id: "chatonly", name: "Chat only", models: [
        { id: "m", name: "Model", on: true, searches: true, searchSet: true, ownSearches: false },
      ] },
      { ...base, id: "router", name: "Router", chat: "https://openrouter.ai/api/v1", models: [
        { id: "openai/m", name: "Native search", on: true, searches: true, ownSearches: true, searchOtherAPI: true },
      ] },
    ],
    presets: [], excluded: [], gateway: { running: true, window: true },
  };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:${JSON.stringify(lang)},theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname.startsWith("/api/provider/")) {
      const body = route.request().postDataJSON();
      posts.push({ path: url.pathname, body });
      if (url.pathname === "/api/provider/save") {
        const p = providers.providers.find((p) => p.id === body.id);
        for (const key of ["chat", "responses", "anthropic"]) if (key in body) p[key] = body[key];
        for (const [id, pref] of Object.entries(body.modelPrefs || {})) {
          const m = p.models.find((m) => m.id === id);
          if (pref.ownSearch) { m.searches = m.ownSearches; m.searchSet = false; }
          else if ("search" in pref) { m.searches = pref.search; m.searchSet = true; }
        }
      }
      return json(providers);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    await route.fulfill({ body: await fs.readFile(file), contentType: { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)] });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of Object.keys(words)) {
    test(`${engine} ${lang}: model search answers, Save, Cancel, defaults and Chat-only warning`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 900, height: 760 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      const errors = [], posts = [], L = words[lang];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang, posts));
      await page.goto("http://magpie.test/?view=providers");
      const row = (id) => page.locator(".mname", { has: page.locator("code", { hasText: id }) });
      const box = (id) => row(id).getByRole("checkbox", { name: L.search, exact: true });
      const open = async (id) => {
        await page.locator(`.row.provider[data-id="${id}"]`).click();
        if (!(await page.locator(".mnames:not([hidden])").count())) await page.getByRole("button", { name: L.names, exact: true }).click();
      };
      const cancel = async () => {
        await page.locator(".editor .bar").getByRole("button", { name: L.cancel, exact: true }).click();
        if (await page.locator("dialog.action-confirm").count()) await page.locator("dialog.action-confirm[open] button").last().click();
        await page.locator("#modal").waitFor({ state: "hidden" });
      };
      const save = async () => {
        await page.locator(".editor .bar").getByRole("button", { name: L.save, exact: true }).click();
        await page.locator("#modal").waitFor({ state: "hidden" });
      };
      await open("relay");
      assert.equal(await box("inherited").isChecked(), true);
      assert.equal(await row("inherited").getByRole("button", { name: L.reset, exact: true }).isVisible(), false, "an inherited answer has no per-model override to restore");
      assert.equal(await box("off").isChecked(), false);
      const list = await page.locator(".mnames").elementHandle();
      await box("inherited").scrollIntoViewIfNeeded();
      const scroll = () => page.evaluate(() => [scrollY, document.querySelector(".mnames").scrollTop]);
      const before = await scroll();
      await box("inherited").uncheck();
      assert.equal(await row("inherited").getByText(L.unsaved, { exact: true }).isVisible(), true);
      await box("inherited").check();
      assert.equal(await row("inherited").getByText(L.unsaved, { exact: true }).isVisible(), false, "returning to the saved answer removes the draft");
      await box("inherited").focus();
      await page.keyboard.press("Space");
      assert.equal(await box("inherited").isChecked(), false, "keyboard changes the same draft");
      assert.deepEqual(await scroll(), before, "ticks do not scroll");
      const reset = row("off").getByRole("button", { name: L.reset, exact: true });
      await reset.scrollIntoViewIfNeeded();
      const resetScroll = await scroll();
      await reset.click();
      assert.equal(await box("off").isChecked(), true, "Restore default shows the inherited on answer");
      assert.deepEqual(posts, [], "neither a tick nor Restore default calls the direct search API");
      assert(await list.evaluate((e) => e.isConnected), "ticks keep the list in place");
      assert.deepEqual(await scroll(), resetScroll, "Restore default does not scroll");
      await cancel();
      await open("relay");
      assert.equal(await box("inherited").isChecked(), true, "Cancel drops the staged off answer");
      assert.equal(await box("off").isChecked(), false, "Cancel drops the staged reset");
      await box("inherited").uncheck();
      await row("off").getByRole("button", { name: L.reset, exact: true }).click();
      await save();
      assert.deepEqual(posts.map((p) => p.path), ["/api/provider/save"]);
      assert.deepEqual(posts[0].body.modelPrefs, { inherited: { search: false }, off: { ownSearch: true } });
      await page.reload();
      await open("relay");
      assert.equal(await box("inherited").isChecked(), false, "the saved answer survives reload");
      assert.equal(await box("off").isChecked(), true, "the saved reset survives reload");
      await row("inherited").getByRole("button", { name: L.reset, exact: true }).click();
      assert.equal(await box("inherited").isChecked(), true);
      await save();
      assert.deepEqual(posts[1].body.modelPrefs, { inherited: { ownSearch: true } });

      await open("chatonly");
      assert.equal(await box("m").isChecked(), true, "an unusable saved answer remains editable");
      const hint = row("m").getByText(L.needs, { exact: true });
      assert.equal(await hint.isVisible(), true, "Chat-only on is visibly not active");
      const description = await box("m").getAttribute("aria-describedby");
      assert.equal(await page.locator(`[id="${description}"]`).textContent(), L.needs);
      await box("m").uncheck();
      assert.equal(await hint.isVisible(), false);
      await box("m").check();
      assert.equal(await hint.isVisible(), true);
      await page.locator(".editor details.more summary", { hasText: L.more }).click();
      const url = page.locator(".editor details.more label", { hasText: L.anthropic }).locator("xpath=following-sibling::div[1]//input");
      await url.fill("https://relay.example");
      assert.equal(await hint.isVisible(), false, "adding a search API removes the warning without redrawing");
      await url.fill("");
      assert.equal(await hint.isVisible(), true, "removing the API restores the warning");
      await page.setViewportSize({ width: 440, height: 760 });
      await box("m").scrollIntoViewIfNeeded();
      const fit = await hint.evaluate((e) => {
        const r = e.getBoundingClientRect();
        return r.width > 0 && r.left >= 0 && r.right <= innerWidth && document.documentElement.scrollWidth <= innerWidth;
      });
      assert(fit, "the warning stays readable in a narrow window");
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-model-search-narrow.png`) });
      }
      assert.equal(posts.length, 2, "endpoint edits and ticks are still staged");
      await cancel();
      await open("router");
      assert.equal(await box("openai/m").isChecked(), true);
      assert.equal(await row("openai/m").getByText(L.needs, { exact: true }).isVisible(), false, "a vendor known to search on Chat needs no other API");
      assert.equal(await page.locator("select").count(), 0);
      assert.deepEqual(errors, []);
    });
  }
}
