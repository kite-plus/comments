import { expect, test, type BrowserContext, type FrameLocator, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";

interface Harness {
  public: string;
  issuer: string;
  blogA: string;
  blogB: string;
  evil: string;
  siteA: string;
  siteB: string;
}
const H: Harness = JSON.parse(readFileSync(new URL("../.harness.json", import.meta.url), "utf8"));

const control = (path: string, method = "GET") => fetch(H.public + path, { method }).then((r) => r.json().catch(() => ({})));
const widget = (page: Page): FrameLocator => page.frameLocator("kite-comments iframe");
const height = (page: Page) => page.locator("kite-comments iframe").evaluate((f: HTMLIFrameElement) => f.style.height);

async function open(page: Page, url: string) {
  await page.goto(url);
  await widget(page).locator(".kc-head").waitFor();
}

// Signs in through the popup; with user, the fake identity service asks
// which account, as a first sign-in would.
async function popupSignIn(context: BrowserContext, page: Page, user?: string, name = user ?? "Ann") {
  const popup = context.waitForEvent("page");
  await widget(page).getByRole("button", { name: /Kite Plus/ }).click();
  const p = await popup;
  if (user) await p.getByRole("button", { name: user }).click();
  await p.waitForEvent("close");
  await expect(widget(page).locator(".kc-who .kc-name")).toHaveText(name);
}

async function post(page: Page, text: string) {
  const w = widget(page);
  await w.locator(".kc-composer textarea").first().fill(text);
  await w.getByRole("button", { name: /^(发表|Post)$/ }).first().click();
  await expect(w.locator(".kc-list .kc-body", { hasText: text })).toBeVisible();
}

test.beforeEach(async () => {
  await control("/__e2e/idp/signout", "POST");
});

test("T1 and T6: sign in on blog A, then on blog B with one click", async ({ context, page }) => {
  await open(page, H.blogA + "/posts/hello/");
  const before = await height(page);
  await popupSignIn(context, page, "Ann");
  // The same height signed in and out (widget.md section 9).
  expect(await height(page)).toBe(before);
  await post(page, "Hello from blog A");

  // Blog B is another site with its own partition; the identity session
  // makes it one click with no page in the popup.
  await open(page, H.blogB + "/posts/welcome/");
  await expect(widget(page).getByRole("button", { name: /Kite Plus/ })).toBeVisible();
  await popupSignIn(context, page);
  await post(page, "Hello from blog B");

  const cookies = await context.cookies(H.public);
  const site = cookies.filter((c) => c.name.startsWith("comments_site_"));
  expect(site.map((c) => (c as { partitionKey?: string }).partitionKey).sort()).toEqual([
    "http://blog-a.localhost",
    "http://blog-b.localhost",
  ]);
});

test("T8: reads never reach the service with a cookie, and nothing sets one", async ({ context, page }) => {
  await open(page, H.blogA + "/posts/hello/");
  await popupSignIn(context, page, "Ann");
  await control("/__e2e/seen/reset", "POST");
  await page.goto(H.blogA + "/posts/second/");
  await widget(page).locator(".kc-who").waitFor();
  await widget(page).getByRole("button", { name: /^(最新|Newest)$/ }).click();
  await page.waitForTimeout(500);
  const seen: { method: string; path: string; browser_sent_cookie: boolean; service_got_cookie: boolean; set_cookie: boolean }[] =
    await control("/__e2e/seen");
  const reads = seen.filter((s) => /^\/(embed|static\/widget|avatars|api\/v1\/threads\/comments)/.test(s.path));
  expect(reads.length).toBeGreaterThanOrEqual(2);
  for (const r of reads) {
    expect(r.service_got_cookie, r.path).toBe(false);
    expect(r.set_cookie, r.path).toBe(false);
  }
  // The widget's own reads go without credentials at all.
  expect(reads.filter((r) => r.path === "/api/v1/threads/comments").every((r) => !r.browser_sent_cookie)).toBe(true);
  // The viewer call is the one credentialed request a load makes.
  expect(seen.some((s) => s.path === "/api/v1/widget/viewer" && s.service_got_cookie)).toBe(true);
});

test("T8: the host page cannot use the reader's session or start a login", async ({ context, page }) => {
  await open(page, H.blogA + "/posts/hello/");
  await popupSignIn(context, page, "Ann");
  await control("/__e2e/seen/reset", "POST");
  const results = await page.evaluate(async ({ pub, site }) => {
    const out: Record<string, string> = {};
    // A form-encoded no-cors POST is the request a page can send with the
    // partitioned cookie attached.
    await fetch(pub + "/api/v1/comments", { method: "POST", mode: "no-cors", credentials: "include", body: new URLSearchParams({ site, body: "forged" }) })
      .then(() => (out.post = "sent"))
      .catch((e) => (out.post = String(e)));
    await fetch(pub + "/api/v1/widget/viewer?site=" + site, { credentials: "include" })
      .then(() => (out.viewer = "readable"))
      .catch(() => (out.viewer = "blocked"));
    try {
      const f = document.querySelector("kite-comments iframe") as HTMLIFrameElement;
      out.storage = String(f.contentWindow!.localStorage.length);
    } catch {
      out.storage = "blocked";
    }
    return out;
  }, { pub: H.public, site: H.siteA });
  expect(results.viewer).toBe("blocked");
  expect(results.storage).toBe("blocked");
  const seen: { method: string; path: string; status: number; browser_sent_cookie: boolean }[] = await control("/__e2e/seen");
  const forged = seen.find((s) => s.method === "POST" && s.path === "/api/v1/comments");
  expect(forged?.browser_sent_cookie).toBe(true);
  expect(forged?.status).toBe(403);
  expect(seen.find((s) => s.path === "/api/v1/widget/viewer")?.status).toBe(403);

  // The host page opens the login popup itself and sends begin: the popup
  // takes begin only from the service's own origin, so no transaction.
  await control("/__e2e/seen/reset", "POST");
  const popup = context.waitForEvent("page");
  await page.evaluate(({ pub, site }) => {
    const w = window.open(pub + "/auth/popup", "x", "popup");
    const send = () => w?.postMessage({ kc: 1, type: "begin", site, challenge: "A".repeat(43) }, "*");
    setTimeout(send, 800);
    setTimeout(send, 1500);
  }, { pub: H.public, site: H.siteA });
  const p = await popup;
  await page.waitForTimeout(2500);
  const after: { method: string; path: string }[] = await control("/__e2e/seen");
  expect(after.some((s) => s.path === "/auth/transactions")).toBe(false);
  await p.close();
});

test("T3: a login page that cuts the popup off still completes by polling", async ({ context, page }) => {
  await control("/__e2e/idp/coop?on=1", "POST");
  try {
    await open(page, H.blogA + "/posts/hello/");
    const popupEvent = context.waitForEvent("page");
    await widget(page).getByRole("button", { name: /Kite Plus/ }).click();
    const popup = await popupEvent;
    await popup.getByRole("button", { name: "Ann" }).click();
    // The opener is gone: no done message, but the widget's polling
    // redeems the login anyway.
    await expect(widget(page).locator(".kc-who .kc-name")).toHaveText("Ann", { timeout: 15000 });
    await popup.close().catch(() => {});
  } finally {
    await control("/__e2e/idp/coop?on=0", "POST");
  }
});

test("T7: the widget does not render on an origin that is not the site's", async ({ page }) => {
  const blocked = page.waitForEvent("console", { predicate: (m) => /frame-ancestors|Refused to frame/i.test(m.text()), timeout: 15000 });
  await page.goto(H.evil + "/embed");
  await blocked;
  await expect(page.locator("kite-comments .kc-failed")).toBeVisible({ timeout: 15000 });
});

test("T8: the iframe height does not change with sign-in or a comment awaiting review", async ({ context, page, browser }) => {
  await open(page, H.blogB + "/posts/welcome/");
  await popupSignIn(context, page, "Bob", "Bob");
  const other = await browser.newContext();
  const anon = await other.newPage();
  await open(anon, H.blogB + "/posts/welcome/");
  const anonHeight = await height(anon);
  expect(await height(page)).toBe(anonHeight);

  // Bob's comment goes to review: the moment it is sent says so, and then
  // nothing in the widget is Bob's, so the height matches again.
  await control("/__e2e/review?site=" + H.siteB + "&on=1", "POST");
  await page.reload();
  await widget(page).locator(".kc-composer textarea").fill("Held for review");
  await widget(page).getByRole("button", { name: /^(发表|Post)$/ }).click();
  await expect(widget(page).locator(".kc-notice")).not.toBeEmpty();
  await expect(widget(page).locator(".kc-notice")).toBeEmpty({ timeout: 10000 });
  await page.reload();
  await widget(page).locator(".kc-who").waitFor();
  await anon.reload();
  await widget(anon).locator(".kc-head").waitFor();
  expect(await height(page)).toBe(await height(anon));
  await expect(widget(page).locator(".kc-body", { hasText: "Held for review" })).toHaveCount(0);
  await control("/__e2e/review?site=" + H.siteB + "&on=0", "POST");
  await other.close();
});

test("T10: a host page with COOP same-origin falls back to the full-page redirect", async ({ page }) => {
  await open(page, H.blogB + "/coop/");
  await widget(page).getByRole("button", { name: /Kite Plus/ }).click();
  const fallback = widget(page).getByRole("button", { name: /在当前页面登录|Sign in on this page/ });
  await expect(fallback).toBeVisible({ timeout: 8000 });
  await fallback.click();
  // The fake identity service asks which account, then the confirm page.
  await page.getByRole("button", { name: "Ann" }).click();
  await page.getByRole("button", { name: /确认|Confirm/ }).click();
  await page.waitForURL(H.blogB + "/coop/");
  await expect(widget(page).locator(".kc-who .kc-name")).toHaveText("Ann");
  expect(page.url()).not.toContain("#kc-login");
});

test("T9: sign out of a site, and back-channel logout", async ({ context, page }) => {
  await open(page, H.blogA + "/posts/hello/");
  await popupSignIn(context, page, "Ann");
  await open(page, H.blogB + "/posts/welcome/");
  await popupSignIn(context, page);

  // Sign out of blog B only.
  await widget(page).getByRole("button", { name: "···" }).click();
  await widget(page).getByRole("button", { name: /退出此站点|Sign out of this site/ }).click();
  await expect(widget(page).getByRole("button", { name: /Kite Plus/ })).toBeVisible();
  await page.reload();
  await expect(widget(page).getByRole("button", { name: /Kite Plus/ })).toBeVisible();
  await open(page, H.blogA + "/posts/hello/");
  await expect(widget(page).locator(".kc-who .kc-name")).toHaveText("Ann");

  // The identity service ends the session: back-channel logout removes
  // what it issued.
  const r = await control("/__e2e/idp/logout?sub=ann", "POST");
  expect(r.status).toBe(200);
  await page.reload();
  await expect(widget(page).getByRole("button", { name: /Kite Plus/ })).toBeVisible();
});

test("deep links scroll to and highlight a comment", async ({ context, page }) => {
  await open(page, H.blogA + "/posts/second/");
  await popupSignIn(context, page, "Ann");
  await post(page, "First on the second post");
  const id = await widget(page).locator(".kc-list .kc-comment").first().getAttribute("id");
  const fresh = await context.newPage();
  await fresh.goto(H.blogA + "/posts/second/#" + id);
  await expect(widget(fresh).locator("#" + id)).toHaveClass(/kc-focus/);
});

test("C3: a guest comments on blog B with proof of work", async ({ page }) => {
  await open(page, H.blogB + "/posts/welcome/");
  const w = widget(page);
  await w.getByRole("button", { name: /访客|guest/i }).click();
  await w.getByPlaceholder(/Name|昵称/).fill("Guest Gail");
  await w.getByPlaceholder(/Email|邮箱/).fill("gail@example.com");
  await w.locator(".kc-guest textarea").fill("A guest was here");
  await w.locator(".kc-guest").getByRole("button", { name: /^(发表|Post)$/ }).click();
  await expect(w.locator(".kc-list .kc-body", { hasText: "A guest was here" })).toBeVisible({ timeout: 30000 });
  await expect(w.locator(".kc-name", { hasText: "Guest Gail" }).first()).toBeVisible();
});

test("reply mail reaches the author of the comment replied to", async ({ browser }) => {
  const annCtx = await browser.newContext();
  const ann = await annCtx.newPage();
  await open(ann, H.blogA + "/posts/hello/");
  await popupSignIn(annCtx, ann, "Ann");
  await post(ann, "Waiting for a reply");
  await control("/__e2e/idp/signout", "POST");

  const bobCtx = await browser.newContext();
  const bob = await bobCtx.newPage();
  await open(bob, H.blogA + "/posts/hello/");
  await popupSignIn(bobCtx, bob, "Bob", "Bob");
  const item = widget(bob).locator(".kc-thread", { hasText: "Waiting for a reply" }).first();
  await item.getByRole("button", { name: /^(回复|Reply)$/ }).first().click();
  await widget(bob).locator(".kc-reply-form textarea").fill("Here is your reply");
  await widget(bob).locator(".kc-reply-form").getByRole("button", { name: /^(发表|Post)$/ }).click();
  await expect(widget(bob).locator(".kc-body", { hasText: "Here is your reply" })).toBeVisible();

  await expect
    .poll(async () => ((await control("/__e2e/mail")) as { To: string; Subject: string }[]).filter((m) => m.To === "ann@example.com" && /Bob/.test(m.Subject)).length, { timeout: 15000 })
    .toBeGreaterThan(0);
  await annCtx.close();
  await bobCtx.close();
});
