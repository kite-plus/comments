// The widget's side of the login protocol (login.md section 4): a verifier
// prepared before the click, the popup handshake, polling the exchange, and
// the full-page redirect fallback.
import { call, randomToken, setToken, storage, type Viewer } from "./api";
import { inAppBrowser, KC } from "../lib";

const b64url = (b: ArrayBuffer | Uint8Array) =>
  btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

interface Pair {
  verifier: string;
  challenge: string;
}

async function newPair(): Promise<Pair> {
  const verifier = b64url(crypto.getRandomValues(new Uint8Array(32)));
  const challenge = b64url(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)));
  return { verifier, challenge };
}

export type LoginState = "idle" | "waiting" | "fallback";

export interface LoginOptions {
  site: string;
  origin: string; // the service's origin
  returnURL: string; // the article, for the redirect fallback
  onViewer: (v: Viewer) => void;
  onState: (s: LoginState) => void;
}

const TX_TTL = 30 * 60 * 1000;

export class Login {
  private pair?: Pair;
  private preparing: Promise<void>;
  private popup: Window | null = null;
  private verifier?: string;
  private started = 0;
  private timer?: number;
  private readyTimer?: number;
  private begun = false;
  private stopped = true;

  constructor(private o: LoginOptions) {
    this.preparing = this.prepare();
    addEventListener("message", (e) => this.message(e));
    document.addEventListener("visibilitychange", () => {
      if (!document.hidden && !this.stopped) this.poll(0);
    });
  }

  private async prepare() {
    this.pair = await newPair();
  }

  /** Starts sign-in; must run in the click handler's synchronous part. */
  start() {
    if (inAppBrowser(navigator.userAgent)) return this.redirect();
    this.begun = false;
    // A fresh name each time, so no other page can guess and target it.
    this.popup = window.open(this.o.origin + "/auth/popup", "kc-login-" + randomToken(12), "popup,width=480,height=720");
    if (!this.popup) return this.o.onState("fallback");
    this.o.onState("waiting");
    clearTimeout(this.readyTimer);
    this.readyTimer = window.setTimeout(() => {
      if (!this.begun) this.o.onState("fallback");
    }, 3000);
  }

  private message(e: MessageEvent) {
    const d = e.data;
    if (!this.popup || e.source !== this.popup || e.origin !== this.o.origin || !d || d.kc !== KC) return;
    if (d.type === "ready" && !this.begun) {
      this.begun = true;
      clearTimeout(this.readyTimer);
      void this.begin(this.popup);
    } else if (d.type === "done") {
      this.poll(0);
    }
  }

  // A click right after load can open the popup before the pair is ready.
  private async begin(popup: Window) {
    if (!this.pair) await this.preparing;
    const p = this.pair;
    if (!p) return;
    popup.postMessage({ kc: KC, type: "begin", site: this.o.site, challenge: p.challenge }, this.o.origin);
    this.verifier = p.verifier;
    this.pair = undefined;
    this.preparing = this.prepare();
    this.started = Date.now();
    this.stopped = false;
    this.poll(1000);
  }

  /** The fallback: the widget submits a top-level form (login.md 4.4). */
  redirect() {
    const p = this.pair;
    if (!p) return;
    const saved = storage.set("kc:v:" + this.o.site, JSON.stringify({ v: p.verifier, exp: Date.now() + TX_TTL }));
    if (!saved) {
      alertStorage();
      return;
    }
    const f = document.createElement("form");
    f.method = "post";
    f.action = this.o.origin + "/auth/redirect";
    f.target = "_top";
    for (const [k, v] of Object.entries({ site: this.o.site, challenge: p.challenge, return: this.o.returnURL })) {
      const i = document.createElement("input");
      i.type = "hidden";
      i.name = k;
      i.value = v;
      f.append(i);
    }
    document.body.append(f);
    f.submit();
  }

  /** On load, finishes a redirect login whose verifier this frame stored. */
  async resume() {
    const raw = storage.get("kc:v:" + this.o.site);
    if (!raw) return;
    try {
      const { v, exp } = JSON.parse(raw) as { v: string; exp: number };
      if (Date.now() > exp) return storage.del("kc:v:" + this.o.site);
      const r = await this.redeem(v);
      if (r !== "pending") storage.del("kc:v:" + this.o.site);
    } catch {
      storage.del("kc:v:" + this.o.site);
    }
  }

  private poll(delay: number) {
    clearTimeout(this.timer);
    if (this.stopped || !this.verifier) return;
    this.timer = window.setTimeout(async () => {
      if (document.hidden) return;
      if (Date.now() - this.started > TX_TTL) return this.stop();
      const r = await this.redeem(this.verifier!).catch(() => "pending" as const);
      if (r !== "pending") return this.stop();
      // Every second for 15 seconds, then every 3 (login.md section 4.5).
      this.poll(Date.now() - this.started < 15000 ? 1000 : 3000);
    }, delay);
  }

  private stop() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.o.onState("idle");
  }

  private async redeem(verifier: string): Promise<"pending" | "done" | "gone"> {
    try {
      const r = await call<{ token: string; viewer: Viewer }>("POST", "/api/v1/widget/sessions", { site: this.o.site, verifier }, false);
      if (r.status === 202) return "pending";
      await this.carrier(r.body.token);
      this.o.onViewer(r.body.viewer);
      return "done";
    } catch (e) {
      return (e as { status?: number }).status === 410 ? "gone" : "pending";
    }
  }

  /** Keeps the token only when the partitioned cookie did not stick. */
  private async carrier(t: string) {
    setToken(null);
    const r = await call<{ viewer: Viewer | null }>("GET", "/api/v1/widget/viewer?site=" + encodeURIComponent(this.o.site), undefined, false);
    if (r.body.viewer) {
      storage.del("kc:t:" + this.o.site);
      return;
    }
    setToken(t);
    storage.set("kc:t:" + this.o.site, t);
  }
}

function alertStorage() {
  document.dispatchEvent(new CustomEvent("kc:storage-off"));
}
