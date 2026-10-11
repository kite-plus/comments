// Solves the guest proof of work (moderation.md section 3.2): find the
// number whose SHA-256 with the salt gives the challenge. Batches of digests
// keep the page responsive without a worker.
export interface Challenge {
  algorithm: string;
  challenge: string;
  maxnumber: number;
  salt: string;
  signature: string;
}

const hex = (b: ArrayBuffer) => Array.from(new Uint8Array(b), (x) => x.toString(16).padStart(2, "0")).join("");

export async function solve(c: Challenge): Promise<{ challenge: string; salt: string; signature: string; number: number }> {
  const enc = new TextEncoder();
  const batch = 1000;
  for (let start = 0; start <= c.maxnumber; start += batch) {
    const end = Math.min(start + batch, c.maxnumber + 1);
    const digests = await Promise.all(
      Array.from({ length: end - start }, (_, i) => crypto.subtle.digest("SHA-256", enc.encode(c.salt + (start + i)))),
    );
    const i = digests.findIndex((d) => hex(d) === c.challenge);
    if (i >= 0) return { challenge: c.challenge, salt: c.salt, signature: c.signature, number: start + i };
  }
  throw new Error("no solution");
}
