import { describe, expect, it } from "vitest";
import { solve } from "../src/app/pow";

describe("proof of work", () => {
  it("finds the hidden number as the server checks it", async () => {
    const salt = "abc123?expires=1&site=s";
    const number = 1234;
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(salt + number));
    const challenge = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
    const got = await solve({ algorithm: "SHA-256", challenge, maxnumber: 5000, salt, signature: "sig" });
    expect(got).toEqual({ challenge, salt, signature: "sig", number });
  });
});
