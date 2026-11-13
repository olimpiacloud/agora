import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { mcpEnv, removeTokenFile, tokenPath, writeTokenFile } from "../server/tokenfile";

let root: string;
let dir: string;

beforeEach(() => {
  root = mkdtempSync(join(tmpdir(), "agora-tok-"));
  dir = join(root, "tokens");
});

afterEach(() => {
  rmSync(root, { recursive: true, force: true });
});

describe("writeTokenFile", () => {
  test("writes the token with mode 0600 inside a 0700 directory", () => {
    const path = writeTokenFile(dir, "agent-1", "secret");
    expect(dirname(path)).toBe(dir);
    expect(readFileSync(path, "utf8")).toBe("secret\n");
    expect(statSync(path).mode & 0o777).toBe(0o600);
    expect(statSync(dir).mode & 0o777).toBe(0o700);
  });

  test("replaces the token and leaves no temporary file", () => {
    writeTokenFile(dir, "agent-1", "first");
    const path = writeTokenFile(dir, "agent-1", "second");
    expect(readFileSync(path, "utf8")).toBe("second\n");
    expect(readdirSync(dir)).toHaveLength(1);
  });

  test("keeps a hostile id inside the directory", () => {
    expect(dirname(tokenPath(dir, "../../etc/x"))).toBe(dir);
  });
});

describe("removeTokenFile", () => {
  test("deletes the file and tolerates a missing one", () => {
    const path = writeTokenFile(dir, "agent-1", "secret");
    removeTokenFile(dir, "agent-1");
    expect(existsSync(path)).toBe(false);
    expect(() => removeTokenFile(dir, "agent-1")).not.toThrow();
  });
});

describe("mcpEnv", () => {
  test("passes the token file and never the token", () => {
    const env = mcpEnv("http://127.0.0.1:7788", "/tmp/x.token", "ws_1", false);
    expect(Object.keys(env).sort()).toEqual(["AGORA_TOKEN_FILE", "AGORA_URL", "AGORA_WORKSPACE"]);
    expect("AGORA_TOKEN" in env).toBe(false);
    const lean = mcpEnv("http://127.0.0.1:7788", "/tmp/x.token", "ws_1", true);
    expect(lean.AGORA_LEAN).toBe("1");
    expect("AGORA_TOKEN" in lean).toBe(false);
  });
});
