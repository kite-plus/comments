import { expect, test, type BrowserContext, type FrameLocator, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

// The end-to-end run against the real local identity stack (ZITADEL in
// kite-plus/identity, `make dev-up dev-init` there) and this repository's
// dev servers: make dev, make dev-worker, make dev-blogs. It signs in through
// the real login pages with a throwaway user it creates and deletes with the
// identity stack's dev admin token.
const IDENTITY = process.env.IDENTITY_DIR ?? new URL("../../../identity", import.meta.url).pathname;
const env = Object.fromEntries(
  readFileSync(IDENTITY + "/dev/out/comments.env", "utf8").split("\n").filter((l) => l.includes("=") && !l.startsWith("#")).map((l) => l.split(/=(.*)/s).slice(0, 2)),
);
const ISSUER = env.COMMENTS_OIDC_ISSUER;
const ADMIN = readFileSync(IDENTITY + "/dev/out/bootstrap/admin.pat", "utf8").trim();
const users = readFileSync(IDENTITY + "/dev/out/users.txt", "utf8").split("\n").filter((l) => l && !l.startsWith("#")).map((l) => l.split("\t"));
const alice = { username: users[0][0], password: users[0][2] };
const PUBLIC = "http://localhost:8081";
const BLOG_A = "http://blog-a.localhost:8301";
const BLOG_B = "http://blog-b.localhost:8302";
const MAILPIT = "http://localhost:8025";

async function zitadel(method: string, path: string, body?: unknown) {
  const r = await fetch(ISSUER + path, { method, headers: { Authorization: "Bearer " + ADMIN, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  const text = await r.text();
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`);
  return text ? JSON.parse(text) : {};
}

function sql(query: string): string {
  return execFileSync("docker", ["exec", "kite-comments-dev-postgres-1", "psql", "-U", "comments", "-d", "comments", "-At", "-c", query], { encoding: "utf8" }).trim();
}

const widget = (page: Page): FrameLocator => page.frameLocator("kite-comments iframe");
const height = (page: Page) => page.locator("kite-comments iframe").evaluate((f: HTMLIFrameElement) => f.style.height);

async function open(page: Page, url: string) {
  await page.goto(url);
  await widget(page).locator(".kc-head").waitFor();
}

// Signs in through the popup; with credentials, through ZITADEL's pages.
async function popupSignIn(context: BrowserContext, page: Page, creds?: { username: string; password: string }) {
  const popupEvent = context.waitForEvent("page");
  await widget(page).getByRole("button", { name: /Kite Plus/ }).click();
  const popup = await popupEvent;
  if (creds) {
    // The login pages render on the client; type once they are live.
    await popup.waitForLoadState("networkidle");
    const login = popup.locator("input").first();
    await login.click();
    await login.pressSequentially(creds.username);
    await expect(popup.getByRole("button", { name: /Continue|继续/ })).toBeEnabled();
    await popup.getByRole("button", { name: /Continue|继续/ }).click();
    const pw = popup.locator('input[type="password"]');
    await pw.waitFor();
    await popup.waitForLoadState("networkidle");
    await pw.click();
    await pw.pressSequentially(creds.password);
    await expect(popup.getByRole("button", { name: /Continue|继续/ })).toBeEnabled();
    await popup.getByRole("button", { name: /Continue|继续/ }).click();
  }
  await popup.waitForEvent("close");
  await widget(page).locator(".kc-who .kc-name").waitFor();
}

async function post(page: Page, text: string) {
  await widget(page).locator(".kc-composer textarea").first().fill(text);
  await widget(page).getByRole("button", { name: /^(发表|Post)$/ }).first().click();
  await expect(widget(page).locator(".kc-list .kc-body", { hasText: text })).toBeVisible();
}

test("real identity stack: popup, one click, reply mail, sign-outs, back-channel logout and user events", async ({ browser }) => {
  const orgs = await zitadel("POST", "/v2/organizations/_search", {});
  const orgId = orgs.result.find((o: { name: string }) => o.name === "Kite Plus").id;
  const name = "e2e-" + Math.random().toString(36).slice(2, 8);
  const password = "Pw-" + Math.random().toString(36).slice(2, 12) + "!9";
  const email = name + "@example.com";
  const created = await zitadel("POST", "/v2/users/human", {
    username: name, organization: { orgId }, profile: { givenName: "E2E", familyName: "Reader" },
    email: { email, isVerified: true }, password: { password, changeRequired: false },
  });
  const sub: string = created.userId;
  try {
    // T1: the popup goes through the real login pages and closes itself.
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await open(page, BLOG_A + "/posts/hello/");
    const signedOut = await height(page);
    await popupSignIn(ctx, page, { username: name, password });
    await expect(widget(page).locator(".kc-who .kc-name")).toHaveText("E2E Reader");
    expect(await height(page)).toBe(signedOut);
    const mine = "Real stack comment " + Date.now();
    await post(page, mine);
    const partitions = (await ctx.cookies(PUBLIC)).filter((c) => c.name.startsWith("comments_site_")).map((c) => (c as { partitionKey?: string }).partitionKey);
    expect(partitions).toContain("http://blog-a.localhost");

    // T6: blog B, another site: one click, no login page.
    await open(page, BLOG_B + "/posts/welcome/");
    await popupSignIn(ctx, page);
    await post(page, "Real stack on blog B " + Date.now());

    // A reply from alice reaches the reader by mail, through the worker
    // and Mailpit.
    const actx = await browser.newContext();
    const apage = await actx.newPage();
    await open(apage, BLOG_A + "/posts/hello/");
    await popupSignIn(actx, apage, alice);
    const item = widget(apage).locator(".kc-thread", { hasText: mine }).first();
    await item.getByRole("button", { name: /^(回复|Reply)$/ }).first().click();
    await widget(apage).locator(".kc-reply-form textarea").fill("A reply for the mail test");
    await widget(apage).locator(".kc-reply-form").getByRole("button", { name: /^(发表|Post)$/ }).click();
    await expect(widget(apage).locator(".kc-body", { hasText: "A reply for the mail test" })).toBeVisible();
    await expect
      .poll(async () => (await (await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent("to:" + email)}`)).json()).messages_count, { timeout: 60000 })
      .toBeGreaterThan(0);
    const mail = await (await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent("to:" + email)}`)).json();
    const msg = await (await fetch(`${MAILPIT}/api/v1/message/${mail.messages[0].ID}`)).json();
    expect(msg.Text).toContain("#kc-");
    const headers = await (await fetch(`${MAILPIT}/api/v1/message/${mail.messages[0].ID}/headers`)).json();
    expect(headers["List-Unsubscribe-Post"]?.[0]).toBe("List-Unsubscribe=One-Click");
    await actx.close();

    // Sign out of blog B only; blog A stays.
    await widget(page).getByRole("button", { name: "···" }).click();
    await widget(page).getByRole("button", { name: /退出此站点|Sign out of this site/ }).click();
    await page.reload();
    await expect(widget(page).getByRole("button", { name: /Kite Plus/ })).toBeVisible();
    await open(page, BLOG_A + "/posts/hello/");
    await expect(widget(page).locator(".kc-who .kc-name")).toHaveText("E2E Reader");

    // Back-channel logout: ending the identity session ends what it
    // issued here.
    const sid = sql(`SELECT ss.idp_sid FROM site_sessions ss JOIN user_identities ui ON ui.user_id = ss.user_id WHERE ui.subject = '${sub}' LIMIT 1`);
    expect(sid).not.toBe("");
    await zitadel("DELETE", "/v2/sessions/" + sid, {});
    await expect.poll(() => sql(`SELECT count(*) FROM site_sessions WHERE idp_sid = '${sid}'`), { timeout: 20000 }).toBe("0");
    await page.reload();
    await expect(widget(page).getByRole("button", { name: /Kite Plus/ })).toBeVisible();

    // User events: deactivation suspends, reactivation lifts it, removal
    // takes the reader's data (the comments are removed by default).
    await zitadel("POST", `/v2/users/${sub}/deactivate`, {});
    await expect.poll(() => sql(`SELECT u.suspended_at IS NOT NULL FROM users u JOIN user_identities ui ON ui.user_id = u.id WHERE ui.subject = '${sub}'`), { timeout: 20000 }).toBe("t");
    await zitadel("POST", `/v2/users/${sub}/reactivate`, {});
    await expect.poll(() => sql(`SELECT u.suspended_at IS NULL FROM users u JOIN user_identities ui ON ui.user_id = u.id WHERE ui.subject = '${sub}'`), { timeout: 20000 }).toBe("t");
    await zitadel("DELETE", `/v2/users/${sub}`);
    await expect.poll(() => sql(`SELECT count(*) FROM user_identities WHERE subject = '${sub}'`), { timeout: 20000 }).toBe("0");
    expect(sql(`SELECT status FROM comments WHERE body_md = '${mine}'`)).toBe("deleted");
    await ctx.close();
  } finally {
    await zitadel("DELETE", `/v2/users/${sub}`).catch(() => {});
  }
});
