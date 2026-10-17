import { describe, expect, it } from "vitest";
import { randomToken } from "../src/app/api";

describe("tokens", () => {
  it("makes URL-safe random tokens", () => {
    const a = randomToken();
    expect(a).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(randomToken()).not.toBe(a);
    expect(randomToken(12)).toMatch(/^[A-Za-z0-9_-]{16}$/);
  });

});
