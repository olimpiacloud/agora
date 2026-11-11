import { chmodSync, mkdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

export function tokenPath(dir: string, agentId: string): string {
  return join(dir, `${agentId.replace(/[^A-Za-z0-9_-]/g, "_")}.token`);
}

export function writeTokenFile(dir: string, agentId: string, token: string): string {
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  chmodSync(dir, 0o700);
  const path = tokenPath(dir, agentId);
  const tmp = `${path}.${process.pid}.tmp`;
  writeFileSync(tmp, `${token}\n`, { mode: 0o600 });
  chmodSync(tmp, 0o600);
  renameSync(tmp, path);
  return path;
}

export function removeTokenFile(dir: string, agentId: string): void {
  rmSync(tokenPath(dir, agentId), { force: true });
}

export function mcpEnv(url: string, tokenFile: string, workspace: string, lean: boolean): Record<string, string> {
  const env: Record<string, string> = { AGORA_URL: url, AGORA_TOKEN_FILE: tokenFile, AGORA_WORKSPACE: workspace };
  if (lean) env.AGORA_LEAN = "1";
  return env;
}
