import { afterEach, describe, expect, it, vi } from "vitest";
import { guestToken, randomToken } from "../src/app/api";

describe("tokens", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("makes URL-safe random tokens", () => {
    const a = randomToken();
    expect(a).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(randomToken()).not.toBe(a);
    expect(randomToken(12)).toMatch(/^[A-Za-z0-9_-]{16}$/);
  });

  it("keeps one guest token per browser", () => {
    const m = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
    });
    const t = guestToken();
    expect(t).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(guestToken()).toBe(t);
    m.set("kc:gt", "tampered");
    expect(guestToken()).not.toBe("tampered");
  });
});
