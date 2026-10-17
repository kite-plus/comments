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
  style?: string | null; // JSON of StyleVars
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

/**
 * The style variables a host page may set on <kite-comments> (or any
 * ancestor) as --kite-comments-<name>, with the kind of value each takes.
 */
export const STYLE_VARS = {
  accent: "color",
  text: "color",
  muted: "color",
  line: "color",
  surface: "color",
  button: "color",
  "button-text": "color",
  font: "font",
  "ui-font": "font",
  "font-size": "length",
  radius: "length",
  decor: "keyword",
} as const;

export type StyleVars = Partial<Record<keyof typeof STYLE_VARS, string>>;

// Values go into the iframe's custom properties, so only plain colors,
// lengths, font lists and keywords pass: nothing that loads (url(), image())
// or refers elsewhere (var(), attr()).
const VALID = {
  color: (v: string) => v.length <= 64 && /^(#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})|[a-z]+|(rgba?|hsla?|oklch|oklab|lab|lch)\([0-9a-z.,%\s/+-]*\))$/i.test(v),
  font: (v: string) => v.length <= 200 && /^[^;{}()<>\\:@!]+$/.test(v),
  length: (v: string) => /^(0|\d{1,3}(\.\d{1,4})?(px|rem|em))$/.test(v),
  keyword: (v: string) => v === "none" || v === "auto",
};

/** Keeps the known variables whose values are of their kind. */
export function cleanStyle(raw: unknown): StyleVars {
  const out: StyleVars = {};
  if (!raw || typeof raw !== "object") return out;
  for (const [k, kind] of Object.entries(STYLE_VARS) as [keyof typeof STYLE_VARS, keyof typeof VALID][]) {
    const v = (raw as Record<string, unknown>)[k];
    if (typeof v === "string" && VALID[kind](v.trim())) out[k] = v.trim();
  }
  return out;
}

/** Reads the variables through get, e.g. a computed style's getPropertyValue. */
export function readStyle(get: (property: string) => string): StyleVars {
  const raw: Record<string, string> = {};
  for (const k of Object.keys(STYLE_VARS)) raw[k] = get("--kite-comments-" + k);
  return cleanStyle(raw);
}
