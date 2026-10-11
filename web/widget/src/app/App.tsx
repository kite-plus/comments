import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { ApiError, call, guestToken, q, read, setToken, storage, type Comment, type Page, type Viewer } from "./api";
import { Login, type LoginState } from "./auth";
import { relative, strings, type Strings } from "./i18n";
import { KC } from "../lib";
import { solve, type Challenge } from "./pow";

export interface Data {
  v: number;
  public_url: string;
  lang: string;
  theme: string;
  accent: string;
  font: string;
  color_scheme: string;
  mode: "normal" | "preview" | "foreign" | "closed" | "inactive";
  origins: string[];
  site?: string;
  guest_policy?: string;
  sort?: string;
  key?: string;
  url?: string;
  title?: string;
  published?: string;
  focus?: string;
  data?: Comment[];
  next_cursor?: string | null;
  count?: number;
  locked?: boolean;
  closed?: boolean;
  partial?: boolean;
}

// The policy that lets server-sanitized comment HTML into the page under
// Trusted Types (login.md section 5). Only internal/markup produces it.
const tt = (window as unknown as { trustedTypes?: { createPolicy(n: string, p: object): { createHTML(s: string): string } } })
  .trustedTypes?.createPolicy("kc", { createHTML: (s: string) => s });

function Html({ html, class: cls }: { html: string; class?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.innerHTML = (tt ? tt.createHTML(html) : html) as string;
  }, [html]);
  return <div class={cls} ref={ref} />;
}

function post(msg: object) {
  parent.postMessage({ kc: KC, ...msg }, "*");
}

const isManager = (v: Viewer | null) => v?.role === "owner" || v?.role === "moderator";

export function App({ d }: { d: Data }) {
  const s = strings(d.lang);
  const [viewer, setViewer] = useState<Viewer | null>(null);
  const [page, setPage] = useState<Page>({
    data: d.data ?? [], next_cursor: d.next_cursor ?? null, count: d.count ?? 0,
    locked: !!d.locked, closed: !!d.closed, partial: d.partial,
  });
  const [newest, setNewest] = useState(d.sort === "newest");
  const [loginState, setLoginState] = useState<LoginState>("idle");
  const [notice, setNotice] = useState("");
  const [highlight, setHighlight] = useState(d.focus ?? "");
  const [replying, setReplying] = useState<Comment | null>(null);
  const [storageOff, setStorageOff] = useState(false);
  const writable = d.mode === "normal" && !page.locked && !page.closed;
  const manager = isManager(viewer);

  const login = useMemo(
    () =>
      d.mode === "normal" && d.site
        ? new Login({ site: d.site, origin: d.public_url, returnURL: d.url ?? "", onViewer: setViewer, onState: setLoginState })
        : null,
    [],
  );

  const load = useCallback(
    async (opts: { cursor?: string | null; around?: string; sortNewest?: boolean; asManager?: boolean } = {}) => {
      if (!d.site || !d.key) return;
      const params = q({ site: d.site, key: d.key, sort: (opts.sortNewest ?? newest) ? "newest" : "oldest", cursor: opts.cursor, around: opts.around });
      const mgr = opts.asManager ?? manager;
      const next = mgr
        ? (await call<Page>("GET", "/api/v1/widget/threads/comments?" + params)).body
        : await read<Page>("/api/v1/threads/comments?" + params);
      setPage((p) => (opts.cursor ? { ...next, data: [...p.data, ...next.data] } : next));
    },
    [newest, manager],
  );

  // The viewer, once, from the site session (login.md section 5).
  useEffect(() => {
    if (!login || !d.site) return;
    const stored = storage.get("kc:t:" + d.site);
    if (stored) setToken(stored);
    call<{ viewer: Viewer | null }>("GET", "/api/v1/widget/viewer?site=" + encodeURIComponent(d.site))
      .then((r) => {
        if (!r.body.viewer && stored) {
          storage.del("kc:t:" + d.site);
          setToken(null);
        }
        setViewer(r.body.viewer);
      })
      .catch(() => {});
    void login.resume();
    const off = () => setStorageOff(true);
    document.addEventListener("kc:storage-off", off);
    return () => document.removeEventListener("kc:storage-off", off);
  }, []);

  useEffect(() => {
    if (manager) void load({ asManager: true });
  }, [manager]);

  // Messages from the host page: only from the parent at a verified origin.
  useEffect(() => {
    const allowed = (o: string) => d.origins.includes(o) || (d.mode === "preview" && /^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/.test(o));
    const onMessage = (e: MessageEvent) => {
      const m = e.data;
      if (e.source !== parent || !allowed(e.origin) || !m || m.kc !== KC) return;
      if (m.type === "theme") applyTheme(m.mode, m.accent ?? d.accent, m.colorScheme ?? m.mode, d.font);
      if (m.type === "focus" && typeof m.commentId === "string") {
        setHighlight(m.commentId);
        void load({ around: m.commentId });
      }
    };
    addEventListener("message", onMessage);
    return () => removeEventListener("message", onMessage);
  }, [load]);

  // Height, readiness and count for the host page (widget.md sections 6, 7).
  useEffect(() => {
    applyTheme(d.theme, d.accent, d.color_scheme, d.font);
    const ro = new ResizeObserver(() => post({ type: "resize", height: Math.ceil(document.documentElement.getBoundingClientRect().height) }));
    ro.observe(document.documentElement);
    post({ type: "ready" });
    return () => ro.disconnect();
  }, []);
  useEffect(() => post({ type: "count", count: page.count }), [page.count]);

  // A #kc- deep link: scroll the host page to the comment.
  useEffect(() => {
    if (!highlight) return;
    const el = document.getElementById("kc-" + highlight);
    if (el) post({ type: "scroll", top: Math.round(el.getBoundingClientRect().top + scrollY) });
  }, [highlight, page]);

  const added = (c: Comment) => {
    if (c.status && c.status !== "published") {
      setNotice(s.submitted);
      setTimeout(() => setNotice(""), 6000);
      return;
    }
    setPage((p) => {
      const data = c.root_id
        ? p.data.map((t) => (t.id === c.root_id ? { ...t, replies: [...(t.replies ?? []), c], reply_count: (t.reply_count ?? 0) + 1 } : t))
        : newest
          ? [c, ...p.data]
          : [...p.data, c];
      return { ...p, data, count: p.count + 1 };
    });
    setHighlight(c.id);
  };

  const replace = (c: Comment) =>
    setPage((p) => ({
      ...p,
      data: p.data.map((t) =>
        t.id === c.id ? { ...c, replies: t.replies, reply_count: t.reply_count } : { ...t, replies: t.replies?.map((r) => (r.id === c.id ? c : r)) },
      ),
    }));

  const signOut = async () => {
    await call("DELETE", "/api/v1/widget/session?site=" + encodeURIComponent(d.site ?? "")).catch(() => {});
    storage.del("kc:t:" + d.site);
    setToken(null);
    setViewer(null);
    if (manager) void load({ asManager: false });
  };

  if (d.mode === "foreign" || d.mode === "inactive") {
    return (
      <Frame s={s} d={d}>
        <p class="kc-state">{d.mode === "foreign" ? s.foreign : s.unavailable}</p>
      </Frame>
    );
  }

  const ctx: Ctx = { s, d, viewer, manager, writable, highlight, setReplying, replace, load, setPage };
  return (
    <Frame s={s} d={d}>
      <header class="kc-head">
        <strong>{s.comments(page.count)}</strong>
        <span class="kc-sort" role="group">
          {[false, true].map((n) => (
            <button
              type="button"
              aria-pressed={newest === n}
              class={newest === n ? "on" : ""}
              onClick={() => {
                setNewest(n);
                void load({ sortNewest: n });
              }}
            >
              {n ? s.newest : s.oldest}
            </button>
          ))}
        </span>
      </header>
      {d.mode === "preview" ? (
        <p class="kc-state kc-box">{s.preview_mode}</p>
      ) : !writable ? (
        <p class="kc-state kc-box">{s.closed}</p>
      ) : viewer ? (
        <Composer ctx={ctx} onDone={added} viewer={viewer} onSignOut={signOut} />
      ) : (
        <SignedOut ctx={ctx} login={login} state={loginState} storageOff={storageOff} onPosted={added} />
      )}
      <div aria-live="polite" class="kc-notice">
        {notice}
      </div>
      {manager && <ManagerBar ctx={ctx} locked={page.locked} />}
      {page.partial && (
        <button type="button" class="kc-link" onClick={() => void load({})}>
          {s.showAll}
        </button>
      )}
      {page.data.length === 0 ? (
        <p class="kc-state">{s.empty}</p>
      ) : (
        <ol class="kc-list">
          {page.data.map((c) => (
            <Item ctx={ctx} c={c} key={c.id} replying={replying} onPosted={added} />
          ))}
        </ol>
      )}
      {page.next_cursor && (
        <button type="button" class="kc-more" onClick={() => void load({ cursor: page.next_cursor })}>
          {s.more}
        </button>
      )}
    </Frame>
  );
}

interface Ctx {
  s: Strings;
  d: Data;
  viewer: Viewer | null;
  manager: boolean;
  writable: boolean;
  highlight: string;
  setReplying: (c: Comment | null) => void;
  replace: (c: Comment) => void;
  load: (o?: { cursor?: string | null; around?: string }) => Promise<void>;
  setPage: (f: (p: Page) => Page) => void;
}

function Frame({ s, d, children }: { s: Strings; d: Data; children: ComponentChildren }) {
  return (
    <main class="kc">
      {children}
      <footer class="kc-foot">
        <a href={d.public_url} target="_blank" rel="noopener">
          {s.footer}
        </a>
      </footer>
    </main>
  );
}

function applyTheme(mode: string, accent: string, cs: string, font: string) {
  const r = document.documentElement;
  r.dataset.theme = mode === "dark" ? "dark" : "light";
  r.dataset.font = font === "serif" ? "serif" : "sans";
  r.style.setProperty("--kc-accent", /^#[0-9a-fA-F]{6}$/.test(accent) ? accent : "#3d65bd");
  r.style.colorScheme = cs;
}

// The signed-out composer has the signed-in composer's height, so the host
// page cannot tell readers apart by the iframe's height (widget.md 9).
function SignedOut({ ctx, login, state, storageOff, onPosted }: {
  ctx: Ctx; login: Login | null; state: LoginState; storageOff: boolean; onPosted: (c: Comment) => void;
}) {
  const { s, d } = ctx;
  const [guest, setGuest] = useState(false);
  if (guest) return <GuestComposer ctx={ctx} onDone={onPosted} onCancel={() => setGuest(false)} />;
  return (
    <div class="kc-box kc-composer kc-signedout">
      {storageOff ? (
        <p class="kc-state">{s.storageOff}</p>
      ) : state === "fallback" ? (
        <>
          <p class="kc-hint">{s.popupBlocked}</p>
          <button type="button" class="kc-primary" onClick={() => login?.redirect()}>
            {s.signInHere}
          </button>
        </>
      ) : (
        <>
          <button type="button" class="kc-primary" onClick={() => login?.start()} disabled={state === "waiting"}>
            {state === "waiting" ? s.waiting : s.signIn}
          </button>
          {d.guest_policy && d.guest_policy !== "off" && (
            <button type="button" class="kc-link" onClick={() => setGuest(true)}>
              {s.guest}
            </button>
          )}
        </>
      )}
    </div>
  );
}

function Avatar({ src }: { src: string }) {
  return <img class="kc-avatar" src={src} width={36} height={36} alt="" loading="lazy" decoding="async" />;
}

function Editor({ ctx, value, onInput, autoFocus }: { ctx: Ctx; value: string; onInput: (v: string) => void; autoFocus?: boolean }) {
  const { s } = ctx;
  const [preview, setPreview] = useState<string | null>(null);
  const togglePreview = async () => {
    if (preview !== null) return setPreview(null);
    const r = await call<{ html: string }>("POST", "/api/v1/preview", { body: value }).catch(() => null);
    setPreview(r?.body.html ?? "");
  };
  return (
    <div class="kc-editor">
      {preview === null ? (
        <textarea
          aria-label={s.placeholder}
          placeholder={s.placeholder}
          value={value}
          maxLength={5000}
          autoFocus={autoFocus}
          onInput={(e) => onInput((e.target as HTMLTextAreaElement).value)}
        />
      ) : (
        <Html class="kc-body kc-preview" html={preview} />
      )}
      <button type="button" class="kc-link kc-preview-toggle" onClick={togglePreview}>
        {preview === null ? s.preview : s.write}
      </button>
    </div>
  );
}

function errorText(s: Strings, e: unknown) {
  return e instanceof ApiError ? e.message : s.failed;
}

function Composer({ ctx, viewer, onDone, onSignOut, parent, onCancel }: {
  ctx: Ctx; viewer: Viewer; onDone: (c: Comment) => void; onSignOut?: () => void; parent?: Comment; onCancel?: () => void;
}) {
  const { s, d } = ctx;
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [menu, setMenu] = useState(false);
  const submit = async (e: Event) => {
    e.preventDefault();
    if (!body.trim() || busy) return;
    setBusy(true);
    setErr("");
    try {
      const r = await call<Comment>("POST", "/api/v1/comments", {
        site: d.site, key: d.key, url: d.url, title: d.title, published: d.published, parent_id: parent?.id, body, lang: d.lang,
      });
      setBody("");
      onDone(r.body);
      onCancel?.();
    } catch (x) {
      setErr(errorText(s, x));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form class={"kc-box kc-composer" + (parent ? " kc-reply-form" : "")} onSubmit={submit}>
      <div class="kc-who">
        <Avatar src={viewer.avatar} />
        <span class="kc-name">{viewer.name}</span>
        {parent && (
          <span class="kc-hint">
            {s.replyTo} @{parent.author?.name || s.formerUser}
          </span>
        )}
        {onSignOut && (
          <span class="kc-menu">
            <button type="button" class="kc-link" aria-expanded={menu} onClick={() => setMenu(!menu)}>
              ···
            </button>
            {menu && (
              <span class="kc-pop">
                <a href={d.public_url + "/me"} target="_blank" rel="noopener">
                  {s.readerCenter}
                </a>
                <button type="button" class="kc-link" onClick={onSignOut}>
                  {s.signOut}
                </button>
              </span>
            )}
          </span>
        )}
      </div>
      <Editor ctx={ctx} value={body} onInput={setBody} autoFocus={!!parent} />
      <div class="kc-actions">
        <span class="kc-error" role="alert">
          {err}
        </span>
        {onCancel && (
          <button type="button" class="kc-link" onClick={onCancel}>
            {s.cancel}
          </button>
        )}
        <button type="submit" class="kc-primary" disabled={busy || !body.trim()}>
          {busy ? s.posting : s.post}
        </button>
      </div>
    </form>
  );
}

function GuestComposer({ ctx, onDone, onCancel, parent }: { ctx: Ctx; onDone: (c: Comment) => void; onCancel: () => void; parent?: Comment }) {
  const { s, d } = ctx;
  const saved = (() => {
    try {
      return JSON.parse(storage.get("kc:g") ?? "{}") as { name?: string; email?: string; url?: string };
    } catch {
      return {};
    }
  })();
  const [f, setF] = useState({ name: saved.name ?? "", email: saved.email ?? "", url: saved.url ?? "", notify: false, website: "" });
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const field = (k: "name" | "email" | "url" | "website") => (e: Event) => setF({ ...f, [k]: (e.target as HTMLInputElement).value });
  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy) return;
    setErr("");
    try {
      setBusy(s.checking);
      const ch = (await call<Challenge>("GET", "/api/v1/pow/challenge?site=" + encodeURIComponent(d.site ?? ""))).body;
      const pow = await solve(ch);
      setBusy(s.posting);
      const r = await call<Comment>("POST", "/api/v1/comments", {
        site: d.site, key: d.key, url: d.url, title: d.title, published: d.published, parent_id: parent?.id, body, lang: d.lang,
        guest: { name: f.name, email: f.email, url: f.url, notify: f.notify, website: f.website, token: guestToken() }, pow,
      });
      storage.set("kc:g", JSON.stringify({ name: f.name, email: f.email, url: f.url }));
      setBody("");
      onDone(r.body);
      onCancel();
    } catch (x) {
      setErr(errorText(s, x));
    } finally {
      setBusy("");
    }
  };
  return (
    <form class="kc-box kc-guest" onSubmit={submit}>
      <div class="kc-fields">
        <input required maxLength={40} placeholder={s.name} aria-label={s.name} value={f.name} onInput={field("name")} />
        <input required type="email" maxLength={254} placeholder={s.email} aria-label={s.email} value={f.email} onInput={field("email")} />
        <input type="url" maxLength={300} placeholder={s.site} aria-label={s.site} value={f.url} onInput={field("url")} />
        <input class="kc-hp" tabIndex={-1} autoComplete="off" aria-hidden="true" value={f.website} onInput={field("website")} />
      </div>
      <Editor ctx={ctx} value={body} onInput={setBody} autoFocus />
      <div class="kc-actions">
        <label class="kc-check">
          <input type="checkbox" checked={f.notify} onChange={(e) => setF({ ...f, notify: (e.target as HTMLInputElement).checked })} />
          {s.notify}
        </label>
        <span class="kc-error" role="alert">
          {err}
        </span>
        <button type="button" class="kc-link" onClick={onCancel}>
          {s.cancel}
        </button>
        <button type="submit" class="kc-primary" disabled={!!busy || !body.trim()}>
          {busy || s.post}
        </button>
      </div>
    </form>
  );
}

function ManagerBar({ ctx, locked }: { ctx: Ctx; locked: boolean }) {
  const { s, d } = ctx;
  const toggle = async () => {
    await call("PUT", "/api/v1/threads/lock", { site: d.site, key: d.key, locked: !locked }).catch(() => {});
    ctx.setPage((p) => ({ ...p, locked: !locked }));
  };
  return (
    <div class="kc-manager">
      <button type="button" class="kc-link" onClick={toggle}>
        {locked ? s.unlock : s.lock}
      </button>
      <a href={d.public_url + "/console/sites/" + d.site + "/comments"} target="_blank" rel="noopener">
        {s.console}
      </a>
    </div>
  );
}

function Item({ ctx, c, replying, onPosted }: { ctx: Ctx; c: Comment; replying: Comment | null; onPosted: (c: Comment) => void }) {
  const { s } = ctx;
  const [more, setMore] = useState<{ list: Comment[]; cursor: string | null } | null>(null);
  const replies = [...(c.replies ?? []), ...(more?.list ?? [])];
  const shown = replies.length;
  const remaining = (c.reply_count ?? 0) - shown;
  const loadMore = async () => {
    const cursor = more ? more.cursor : c.replies_cursor;
    const r = await read<{ data: Comment[]; next_cursor: string | null }>(`/api/v1/comments/${c.id}/replies?` + q({ cursor }));
    setMore({ list: [...(more?.list ?? []), ...r.data], cursor: r.next_cursor });
  };
  return (
    <li class="kc-thread">
      <One ctx={ctx} c={c} />
      {replying?.id === c.id && <ReplyForm ctx={ctx} parent={c} onPosted={onPosted} />}
      {shown > 0 && (
        <ol class="kc-replies">
          {replies.map((r) => (
            <li key={r.id}>
              <One ctx={ctx} c={r} />
              {replying?.id === r.id && <ReplyForm ctx={ctx} parent={r} onPosted={onPosted} />}
            </li>
          ))}
        </ol>
      )}
      {remaining > 0 && (
        <button type="button" class="kc-link kc-expand" onClick={loadMore}>
          {s.moreReplies(remaining)}
        </button>
      )}
    </li>
  );
}

function ReplyForm({ ctx, parent, onPosted }: { ctx: Ctx; parent: Comment; onPosted: (c: Comment) => void }) {
  if (ctx.viewer) return <Composer ctx={ctx} viewer={ctx.viewer} parent={parent} onDone={onPosted} onCancel={() => ctx.setReplying(null)} />;
  return <GuestComposer ctx={ctx} parent={parent} onDone={onPosted} onCancel={() => ctx.setReplying(null)} />;
}

function One({ ctx, c }: { ctx: Ctx; c: Comment }) {
  const { s, d, viewer, manager } = ctx;
  const [editing, setEditing] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [reporting, setReporting] = useState(false);
  const [err, setErr] = useState("");
  if (c.placeholder) {
    return (
      <article id={"kc-" + c.id} class="kc-comment kc-gone">
        {c.placeholder === "deleted" ? s.deletedPlaceholder : s.hiddenPlaceholder}
      </article>
    );
  }
  const a = c.author!;
  const mine = !!viewer && a.kind === "user" && a.id === viewer.author_id;
  const editable = mine && Date.now() - Date.parse(c.created_at) < 24 * 3600 * 1000;
  const canReply = ctx.writable && (!!viewer || (d.guest_policy && d.guest_policy !== "off")) && (!c.status || c.status === "published");
  const act = async (fn: () => Promise<void>) => {
    setErr("");
    try {
      await fn();
    } catch (x) {
      setErr(errorText(s, x));
    }
  };
  const moderate = (action: string) =>
    act(async () => ctx.replace((await call<Comment>("POST", `/api/v1/comments/${c.id}/moderation`, { site: d.site, action })).body));
  const name = a.kind === "former_user" || !a.name ? s.formerUser : a.name;
  return (
    <article id={"kc-" + c.id} class={"kc-comment" + (ctx.highlight === c.id ? " kc-focus" : "")}>
      <Avatar src={a.avatar} />
      <div class="kc-main">
        <div class="kc-meta">
          {a.url ? (
            <a class="kc-name" href={a.url} rel="ugc nofollow noopener noreferrer" target="_blank">
              {name}
            </a>
          ) : (
            <span class="kc-name">{name}</span>
          )}
          {a.badge && <span class="kc-badge">{a.badge === "owner" ? s.owner : s.moderator}</span>}
          {c.reply_to && (
            <span class="kc-hint">
              {s.replyTo} @{c.reply_to.name || s.formerUser}
            </span>
          )}
          <time dateTime={c.created_at} title={new Date(c.created_at).toLocaleString(d.lang)}>
            {relative(c.created_at, d.lang)}
          </time>
          {c.edited_at && <span class="kc-hint">{s.edited}</span>}
          {c.pinned && <span class="kc-badge">{s.pinned}</span>}
          {manager && c.status && c.status !== "published" && <span class="kc-badge kc-status">{s.status[c.status]}</span>}
        </div>
        {editing !== null ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act(async () => {
                ctx.replace((await call<Comment>("PATCH", `/api/v1/comments/${c.id}`, { site: d.site, body: editing })).body);
                setEditing(null);
              });
            }}
          >
            <Editor ctx={ctx} value={editing} onInput={setEditing} autoFocus />
            <div class="kc-actions">
              <button type="button" class="kc-link" onClick={() => setEditing(null)}>
                {s.cancel}
              </button>
              <button type="submit" class="kc-primary">
                {s.save}
              </button>
            </div>
          </form>
        ) : (
          <Html class="kc-body" html={c.body_html ?? ""} />
        )}
        <div class="kc-row">
          {canReply && (
            <button type="button" class="kc-link" onClick={() => ctx.setReplying(c)}>
              {s.reply}
            </button>
          )}
          {viewer && !mine && (
            <button type="button" class="kc-link" onClick={() => setReporting(!reporting)}>
              {s.report}
            </button>
          )}
          {editable && editing === null && (
            <button type="button" class="kc-link" onClick={() => void act(async () => setEditing(await source(c, d.site ?? "")))}>
              {s.edit}
            </button>
          )}
          {mine &&
            (confirm ? (
              <>
                <span class="kc-hint">{s.deleteConfirm}</span>
                <button
                  type="button"
                  class="kc-link kc-danger"
                  onClick={() =>
                    void act(async () => {
                      await call("DELETE", `/api/v1/comments/${c.id}?site=${encodeURIComponent(d.site ?? "")}`);
                      ctx.replace({ ...c, placeholder: "deleted" });
                      ctx.setPage((p) => ({ ...p, count: Math.max(0, p.count - 1) }));
                    })
                  }
                >
                  {s.delete}
                </button>
                <button type="button" class="kc-link" onClick={() => setConfirm(false)}>
                  {s.cancel}
                </button>
              </>
            ) : (
              <button type="button" class="kc-link" onClick={() => setConfirm(true)}>
                {s.delete}
              </button>
            ))}
          {manager && (
            <span class="kc-mod">
              {c.status === "pending" && <ModButton label={s.approve} onClick={() => moderate("approve")} />}
              {(c.status === "hidden" || c.status === "spam") && <ModButton label={s.restore} onClick={() => moderate("restore")} />}
              {(c.status === "published" || c.status === "pending") && <ModButton label={s.hide} onClick={() => moderate("hide")} />}
              {c.status !== "spam" && <ModButton label={s.spam} onClick={() => moderate("spam")} />}
              {!c.root_id && c.status === "published" && <ModButton label={c.pinned ? s.unpin : s.pin} onClick={() => moderate(c.pinned ? "unpin" : "pin")} />}
            </span>
          )}
          <span class="kc-error" role="alert">
            {err}
          </span>
        </div>
        {reporting && <ReportForm ctx={ctx} c={c} onDone={() => setReporting(false)} />}
      </div>
    </article>
  );
}

// The author's Markdown, for editing.
async function source(c: Comment, site: string): Promise<string> {
  return (await call<{ body: string }>("GET", `/api/v1/comments/${c.id}/source?site=${encodeURIComponent(site)}`)).body.body;
}

function ModButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button type="button" class="kc-link" onClick={onClick}>
      {label}
    </button>
  );
}

function ReportForm({ ctx, c, onDone }: { ctx: Ctx; c: Comment; onDone: () => void }) {
  const { s, d } = ctx;
  const [reason, setReason] = useState("spam");
  const [note, setNote] = useState("");
  const [msg, setMsg] = useState("");
  const submit = async (e: Event) => {
    e.preventDefault();
    try {
      await call("POST", `/api/v1/comments/${c.id}/reports`, { site: d.site, reason, note });
      setMsg(s.reported);
      setTimeout(onDone, 1500);
    } catch (x) {
      setMsg(errorText(s, x));
    }
  };
  return (
    <form class="kc-report" onSubmit={submit}>
      <select value={reason} onChange={(e) => setReason((e.target as HTMLSelectElement).value)} aria-label={s.report}>
        {Object.entries(s.reasons).map(([k, v]) => (
          <option value={k}>{v}</option>
        ))}
      </select>
      <input maxLength={500} placeholder={s.reportNote} aria-label={s.reportNote} value={note} onInput={(e) => setNote((e.target as HTMLInputElement).value)} />
      <button type="submit" class="kc-primary">
        {s.report}
      </button>
      <button type="button" class="kc-link" onClick={onDone}>
        {s.cancel}
      </button>
      <span class="kc-hint" role="status">
        {msg}
      </span>
    </form>
  );
}
