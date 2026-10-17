import { describe, expect, it } from "vitest";
import { accentOf, cleanStyle, focusFromHash, frameSrc, inAppBrowser, luminance, normalizeLang, pageURL, parseRGB, readStyle, themeFromBackgrounds } from "../src/lib";
import { relative } from "../src/app/i18n";

describe("normalizeLang", () => {
  it("maps zh variants to zh-CN and the rest to en", () => {
    for (const t of ["zh", "zh-CN", "zh-Hans", "zh-TW", "ZH-hant", " zh "]) expect(normalizeLang(t)).toBe("zh-CN");
    for (const t of ["en", "en-US", "fr", "ja-JP"]) expect(normalizeLang(t)).toBe("en");
    expect(normalizeLang("")).toBeNull();
    expect(normalizeLang(null)).toBeNull();
  });
});

describe("theme", () => {
  it("parses computed colors", () => {
    expect(parseRGB("rgb(13, 17, 23)")).toEqual([13, 17, 23, 1]);
    expect(parseRGB("rgba(0, 0, 0, 0)")).toEqual([0, 0, 0, 0]);
    expect(parseRGB("rgb(255 255 255 / 50%)")).toEqual([255, 255, 255, 0.5]);
  });
  it("reads the first opaque background", () => {
    expect(themeFromBackgrounds("rgb(13, 17, 23)", "rgb(255, 255, 255)")).toBe("dark");
    expect(themeFromBackgrounds("rgba(0, 0, 0, 0)", "rgb(13, 17, 23)")).toBe("dark");
    expect(themeFromBackgrounds("rgba(0, 0, 0, 0)", "rgba(0, 0, 0, 0)")).toBe("light");
    expect(themeFromBackgrounds("rgb(250, 250, 250)", "")).toBe("light");
  });
  it("puts the threshold at luminance 0.4", () => {
    expect(luminance([255, 255, 255, 1])).toBeCloseTo(1);
    expect(luminance([0, 0, 0, 1])).toBe(0);
    expect(themeFromBackgrounds("rgb(128, 128, 128)", "")).toBe("dark");
    expect(themeFromBackgrounds("rgb(180, 180, 180)", "")).toBe("light");
  });
});

describe("page address", () => {
  it("prefers the attribute, then canonical, then location, without fragment", () => {
    expect(pageURL("/posts/a", null, "https://b.example/x?y#z")).toBe("https://b.example/posts/a");
    expect(pageURL(null, "https://b.example/c", "https://b.example/x#z")).toBe("https://b.example/c");
    expect(pageURL(null, null, "https://b.example/x#kc-1")).toBe("https://b.example/x");
  });
});

describe("deep links", () => {
  it("reads #kc-<id> only", () => {
    expect(focusFromHash("#kc-123")).toBe("123");
    expect(focusFromHash("#kc-login")).toBeNull();
    expect(focusFromHash("#top")).toBeNull();
  });
});

describe("iframe address", () => {
  it("carries the parameters of widget.md section 4", () => {
    const src = frameSrc("https://comments.kite.plus", { site: "s", url: "https://b.example/p", key: null, lang: "zh-CN", theme: "dark", accent: "#3d65bd", focus: "9" });
    const u = new URL(src);
    expect(u.origin + u.pathname).toBe("https://comments.kite.plus/embed/v1/thread");
    expect(u.searchParams.get("url")).toBe("https://b.example/p");
    expect(u.searchParams.get("accent")).toBe("#3d65bd");
    expect(u.searchParams.has("key")).toBe(false);
  });
  it("accepts only #RRGGBB accents", () => {
    expect(accentOf("#ABCDEF")).toBe("#ABCDEF");
    expect(accentOf("red")).toBeNull();
    expect(accentOf("#abc")).toBeNull();
  });
});

describe("host style variables", () => {
  it("keeps plain colors, lengths, fonts and keywords", () => {
    const v = readStyle((p) =>
      ({
        "--kite-comments-text": " #333 ",
        "--kite-comments-surface": "rgba(0, 0, 0, .04)",
        "--kite-comments-line": "hsl(30 10% 80% / 50%)",
        "--kite-comments-button": "rebeccapurple",
        "--kite-comments-font": `"LXGW WenKai", "霞鹜文楷", serif`,
        "--kite-comments-radius": "4px",
        "--kite-comments-font-size": "1.0625rem",
        "--kite-comments-decor": "none",
      })[p] ?? "",
    );
    expect(v).toEqual({
      text: "#333", surface: "rgba(0, 0, 0, .04)", line: "hsl(30 10% 80% / 50%)", button: "rebeccapurple",
      font: `"LXGW WenKai", "霞鹜文楷", serif`, radius: "4px", "font-size": "1.0625rem", decor: "none",
    });
  });

  it("drops anything that loads or refers elsewhere", () => {
    expect(
      cleanStyle({
        text: "url(https://evil.example/x)", muted: "var(--x)", line: "rgb(var(--r), 0, 0)", surface: "image(red)",
        font: "x; } body { display: none", "ui-font": "a</style>", radius: "calc(1px + 1px)", "font-size": "99999px",
        decor: "off", accent: "#12345", unknown: "#fff",
      }),
    ).toEqual({});
    expect(cleanStyle("#fff")).toEqual({});
    expect(cleanStyle(null)).toEqual({});
  });
});

describe("in-app browsers", () => {
  it("are recognized by user agent", () => {
    expect(inAppBrowser("Mozilla/5.0 ... MicroMessenger/8.0.40")).toBe(true);
    expect(inAppBrowser("Mozilla/5.0 ... QQ/9.0.0")).toBe(true);
    expect(inAppBrowser("Mozilla/5.0 ... Chrome/140 Safari/537.36")).toBe(false);
  });
});

describe("relative time", () => {
  it("is short for recent comments", () => {
    const now = Date.parse("2026-10-10T12:00:00Z");
    expect(relative("2026-10-10T11:59:50Z", "en", now)).toBe("just now");
    expect(relative("2026-10-10T10:00:00Z", "en", now)).toBe("2 hours ago");
    expect(relative("2026-10-09T12:00:00Z", "zh-CN", now)).toBe("昨天");
  });
});
