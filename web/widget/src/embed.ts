// embed.js: defines <kite-comments> on the host page. It computes the page's
// address, language and theme, loads the iframe lazily and sizes it. It
// never touches credentials, cookies or storage of the host page
// (widget.md section 3).
import { KC, accentOf, focusFromHash, frameSrc, normalizeLang, pageURL, themeFromBackgrounds } from "./lib";

// The server replaces this with its own origin when it serves the file, so
// a self-hosted copy still points at the service.
const ORIGIN = "__KITE_COMMENTS_ORIGIN__";
const ATTRS = ["site", "page", "page-key", "page-title", "page-published", "theme", "accent", "font", "lang", "lazy"];

class KiteComments extends HTMLElement {
  static observedAttributes = ATTRS;
  private frame?: HTMLIFrameElement;
  private io?: IntersectionObserver;
  private mo?: MutationObserver;
  private timer?: number;
  private debounce?: number;
  private target: string | null = null;
  private onMessage = (e: MessageEvent) => this.message(e);
  private onHash = () => this.hash();
  private onScheme = () => this.themeChanged();

  connectedCallback() {
    this.style.display = "block";
    addEventListener("message", this.onMessage);
    addEventListener("hashchange", this.onHash);
    this.target = focusFromHash(location.hash);
    const login = location.hash === "#kc-login";
    if (login) history.replaceState(null, "", location.pathname + location.search);
    if (this.target || login || this.getAttribute("lazy") === "false") this.load(login);
    else if ("IntersectionObserver" in window) {
      this.io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && this.load(false), { rootMargin: "800px" });
      this.io.observe(this);
    } else if (document.readyState === "complete") this.load(false);
    else addEventListener("load", () => this.load(false), { once: true });
  }

  disconnectedCallback() {
    removeEventListener("message", this.onMessage);
    removeEventListener("hashchange", this.onHash);
    matchMedia("(prefers-color-scheme: dark)").removeEventListener("change", this.onScheme);
    this.io?.disconnect();
    this.mo?.disconnect();
    clearTimeout(this.timer);
    this.frame?.remove();
    this.frame = undefined;
  }

  attributeChangedCallback(name: string, old: string | null, value: string | null) {
    if (!this.frame || old === value) return;
    if (name === "theme" || name === "accent") this.themeChanged();
    else if (name !== "lazy") this.load(false, true);
  }

  private theme(): { mode: string; cs: string } {
    const t = this.getAttribute("theme");
    if (t === "light" || t === "dark") return { mode: t, cs: t };
    const d = document.documentElement;
    const mode = themeFromBackgrounds(getComputedStyle(document.body).backgroundColor, getComputedStyle(d).backgroundColor);
    return { mode, cs: getComputedStyle(d).colorScheme === "normal" ? mode : getComputedStyle(d).colorScheme };
  }

  private load(scroll: boolean, rebuild = false) {
    if (this.frame && !rebuild) return;
    this.io?.disconnect();
    this.frame?.remove();
    const site = this.getAttribute("site");
    if (!site) return console.warn("kite-comments: the site attribute is missing");
    const canonical = document.querySelector<HTMLLinkElement>('link[rel="canonical"]')?.href ?? null;
    const lang = normalizeLang(this.getAttribute("lang")) ?? normalizeLang(document.documentElement.lang) ?? normalizeLang(navigator.language) ?? "en";
    const { mode, cs } = this.theme();
    const f = document.createElement("iframe");
    f.src = frameSrc(ORIGIN, {
      site, url: pageURL(this.getAttribute("page"), canonical, location.href), key: this.getAttribute("page-key"),
      title: this.getAttribute("page-title") ?? document.title, published: this.getAttribute("page-published"),
      lang, theme: mode, cs, accent: accentOf(this.getAttribute("accent")), font: this.getAttribute("font"), focus: this.target,
    });
    f.title = lang === "en" ? "Comments" : "评论";
    f.setAttribute("allow", "identity-credentials-get");
    f.setAttribute("referrerpolicy", "no-referrer");
    f.style.cssText = `display:block;width:100%;min-width:320px;height:160px;border:0;color-scheme:${cs};background:transparent`;
    this.replaceChildren(f);
    this.frame = f;
    this.timer = window.setTimeout(() => this.failed(), 10000);
    if (!this.getAttribute("theme") || this.getAttribute("theme") === "auto") this.watchTheme();
    if (scroll) this.scrollIntoView({ block: "start" });
  }

  private watchTheme() {
    if (this.mo) return;
    this.mo = new MutationObserver(() => this.themeChanged());
    const opts = { attributes: true, attributeFilter: ["class", "style", "data-theme"] };
    this.mo.observe(document.documentElement, opts);
    this.mo.observe(document.body, opts);
    matchMedia("(prefers-color-scheme: dark)").addEventListener("change", this.onScheme);
  }

  private themeChanged() {
    clearTimeout(this.debounce);
    this.debounce = window.setTimeout(() => {
      const { mode, cs } = this.theme();
      if (this.frame) this.frame.style.colorScheme = cs;
      this.post({ type: "theme", mode, colorScheme: cs, accent: accentOf(this.getAttribute("accent")) });
    }, 100);
  }

  private post(msg: object) {
    this.frame?.contentWindow?.postMessage({ kc: KC, ...msg }, ORIGIN);
  }

  private hash() {
    const id = focusFromHash(location.hash);
    if (!id) return;
    this.target = id;
    if (this.frame) this.post({ type: "focus", commentId: id });
    else this.load(false);
  }

  private message(e: MessageEvent) {
    const d = e.data;
    if (!this.frame || e.source !== this.frame.contentWindow || e.origin !== ORIGIN || !d || d.kc !== KC) return;
    if (d.type === "ready") {
      clearTimeout(this.timer);
      this.querySelector(".kc-failed")?.remove();
    }
    else if (d.type === "resize" && typeof d.height === "number") this.frame.style.height = Math.max(0, Math.min(d.height, 1e5)) + "px";
    else if (d.type === "scroll" && typeof d.top === "number") {
      scrollTo({ top: this.frame.getBoundingClientRect().top + scrollY + d.top - 80, behavior: "smooth" });
    } else if (d.type === "count" && typeof d.count === "number") {
      this.dispatchEvent(new CustomEvent("kite-comments:count", { detail: { count: d.count }, bubbles: true }));
    }
  }

  private failed() {
    console.warn("kite-comments: the widget did not load. The site may be unverified, this origin may not be one of its verified origins, or the network failed.");
    const p = document.createElement("p");
    p.className = "kc-failed";
    const a = document.createElement("a");
    a.href = "#";
    a.textContent = this.frame?.title === "Comments" ? "Comments failed to load. Retry" : "评论加载失败，重试";
    a.onclick = (ev) => {
      ev.preventDefault();
      this.load(false, true);
    };
    p.append(a);
    this.prepend(p);
    if (this.frame) this.frame.style.height = "0";
  }
}

if (!customElements.get("kite-comments")) customElements.define("kite-comments", KiteComments);
