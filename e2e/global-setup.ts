import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";
import { createInterface } from "node:readline";

// Starts the harness, records its addresses for the tests and stops it when
// the run ends.
export default async function setup() {
  const child = spawn("go", ["run", "./e2e/harness"], { cwd: new URL("..", import.meta.url).pathname, stdio: ["pipe", "pipe", "inherit"] });
  const line: string = await new Promise((resolve, reject) => {
    const rl = createInterface({ input: child.stdout! });
    rl.once("line", resolve);
    child.once("exit", (code) => reject(new Error(`harness exited with ${code}`)));
  });
  writeFileSync(new URL("./.harness.json", import.meta.url), line);
  return async () => {
    child.stdin!.end();
    await new Promise((r) => child.once("exit", r));
  };
}
