// Pure helpers shared by embed.js and the iframe app, kept free of the DOM
// so they can be tested in Node.

/** Protocol version of the host and iframe messages (widget.md section 6). */
export const KC = 1;

/**
 * Maps a language tag to one the widget has: anything starting with "zh" is
 * zh-CN, everything else en (widget.md section 2, the lang row).
 */
export function normalizeLang(tag: string | null | undefined): "zh-CN" | "en" | null {
  if (!tag || !tag.trim()) return null;
  return tag.trim().toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

/** Parses rgb()/rgba() as computed styles report them. */
export function parseRGB(css: string): [number, number, number, number] | null {
  const m = css.match(/rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)/i);
  if (!m) return null;
  let a = m[4] === undefined ? 1 : parseFloat(m[4]);
  if (m[4]?.endsWith("%")) a /= 100;
  return [+m[1], +m[2], +m[3], a];
}

/** Relative luminance of an sRGB color, 0 to 1. */
export function luminance([r, g, b]: [number, number, number, number]): number {
  const f = (c: number) => {
    c /= 255;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

/**
 * Decides dark or light from the body's and html's computed backgrounds: the
 * first that is not transparent, dark below luminance 0.4 (widget.md 7).
 */
export function themeFromBackgrounds(body: string, html: string): "light" | "dark" {
  for (const css of [body, html]) {
    const c = parseRGB(css);
    if (c && c[3] > 0) return luminance(c) < 0.4 ? "dark" : "light";
  }
  return "light";
}

/** The page's address without its fragment, relative ones resolved. */
export function pageURL(attr: string | null, canonical: string | null, location: string): string {
  const raw = attr || canonical || location;
  try {
    const u = new URL(raw, location);
    u.hash = "";
    return u.href;
  } catch {
    const u = new URL(location);
    u.hash = "";
    return u.href;
  }
}

/** The comment ID of a #kc-<id> fragment. */
export function focusFromHash(hash: string): string | null {
  const m = /^#kc-(\d{1,19})$/.exec(hash);
  return m ? m[1] : null;
}

export interface FrameParams {
  site: string;
  url: string;
  key?: string | null;
  title?: string | null;
  published?: string | null;
  lang: string;
  theme: string;
  accent?: string | null;
  font?: string | null;
  cs?: string | null;
  focus?: string | null;
}

/** The iframe's address (widget.md section 4). */
export function frameSrc(origin: string, p: FrameParams): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) if (v) q.set(k, v);
  return origin + "/embed/v1/thread?" + q.toString();
}

/** Accepts a host color only in the #RRGGBB form. */
export function accentOf(v: string | null): string | null {
  return v && /^#[0-9a-fA-F]{6}$/.test(v) ? v : null;
}

/** Whether a user agent is an in-app browser where popups do not work. */
export function inAppBrowser(ua: string): boolean {
  return /MicroMessenger|QQ\/|MQQBrowser|Weibo/i.test(ua);
}
