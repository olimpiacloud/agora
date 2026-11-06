import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";

const URL_BASE = process.env.CARBON_URL || "http://127.0.0.1:7788";
const HOME = process.env.CARBON_HOME || "/var/lib/carbon";
const STATE_DIR = join(homedir(), ".carbon-paseo");
const STATE_FILE = join(STATE_DIR, "state.json");

export const CARBON_BIN = ["/usr/local/bin/carbon", "/projects/carbon/bin/carbon"].find((p) => existsSync(p)) || "carbon";

interface State {
  workspaces: Record<string, string>;
  agents: Record<string, { cursor: string; token: string; workspace: string }>;
}

function loadState(): State {
  try {
    return JSON.parse(readFileSync(STATE_FILE, "utf8"));
  } catch {
    return { workspaces: {}, agents: {} };
  }
}

function saveState(s: State) {
  mkdirSync(STATE_DIR, { recursive: true, mode: 0o700 });
  writeFileSync(STATE_FILE, JSON.stringify(s, null, 2), { mode: 0o600 });
}

export function adminToken(): string {
  return readFileSync(join(HOME, "admin.token"), "utf8").trim();
}

export class CarbonError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function call<T = any>(method: string, path: string, body?: unknown, token = adminToken()): Promise<T> {
  const r = await fetch(URL_BASE + path, {
    method,
    headers: { Authorization: `Bearer ${token}`, ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await r.text();
  if (!r.ok) {
    let msg = text;
    try {
      msg = JSON.parse(text).error || text;
    } catch {}
    throw new CarbonError(r.status, msg);
  }
  return (text ? JSON.parse(text) : undefined) as T;
}

export async function bytes(path: string): Promise<{ mime: string; data: Buffer }> {
  const r = await fetch(URL_BASE + path, { headers: { Authorization: `Bearer ${adminToken()}` } });
  if (!r.ok) throw new CarbonError(r.status, await r.text());
  return { mime: r.headers.get("content-type") || "image/jpeg", data: Buffer.from(await r.arrayBuffer()) };
}

export function wsURL(path: string): string {
  return URL_BASE.replace(/^http/, "ws") + path;
}

export async function linkedWorkspace(paseoWorkspaceId: string, name: string, create: boolean): Promise<string | null> {
  const st = loadState();
  const known = st.workspaces[paseoWorkspaceId];
  if (known) {
    try {
      const w = await call("GET", `/v1/workspaces/${known}`);
      if (w.state !== "destroyed") return known;
    } catch {}
  }
  const all: any[] = await call("GET", "/v1/workspaces");
  const found = all.find((w) => w.labels?.paseo_workspace === paseoWorkspaceId && w.state !== "destroyed");
  if (found) {
    st.workspaces[paseoWorkspaceId] = found.id;
    saveState(st);
    return found.id;
  }
  if (!create) return null;
  const w = await call("POST", "/v1/workspaces", { name: name || paseoWorkspaceId, labels: { paseo_workspace: paseoWorkspaceId } });
  st.workspaces[paseoWorkspaceId] = w.id;
  saveState(st);
  return w.id;
}

export async function agentCursor(agentId: string, title: string | null, workspace: string): Promise<{ cursor: string; token: string }> {
  const st = loadState();
  const known = st.agents[agentId];
  if (known && known.workspace === workspace) {
    try {
      await call("GET", "/v1/me", undefined, known.token);
      return known;
    } catch {}
  }
  const r = await call("POST", "/v1/cursors", {
    name: title || `paseo-${agentId.slice(0, 8)}`,
    workspace,
    agent: "paseo",
    permissions: ["desktop", "browser", "process", "spawn", "message", "workspace"],
    labels: { paseo_agent: agentId, keep_tabs: "true" },
  });
  const rec = { cursor: r.cursor.id, token: r.token, workspace };
  st.agents[agentId] = rec;
  saveState(st);
  return rec;
}

export function nameFromCwd(cwd: string): string {
  return basename(cwd) || "paseo";
}
