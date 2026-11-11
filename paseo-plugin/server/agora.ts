import type { PluginHookContext } from "@getpaseo/plugin/server";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";
import { idleWorkspace, orphanWorkspaces, type AgoraWorkspace } from "./cleanup";
import { removeTokenFile, writeTokenFile } from "./tokenfile";

const URL_BASE = process.env.AGORA_URL || "http://127.0.0.1:7788";
const HOME = process.env.AGORA_HOME || "/var/lib/agora";
const STATE_DIR = join(homedir(), ".agora-paseo");
const STATE_FILE = join(STATE_DIR, "state.json");
const TOKEN_DIR = join(STATE_DIR, "tokens");

export const AGORA_BIN = process.env.AGORA_BIN || (existsSync("/usr/local/bin/agora") ? "/usr/local/bin/agora" : "agora");

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

function updateState(f: (s: State) => void) {
  const s = loadState();
  f(s);
  saveState(s);
}

const REQUEST_TIMEOUT_MS = 10_000;

const chains = new Map<string, Promise<unknown>>();

function serialize<T>(key: string, f: () => Promise<T>): Promise<T> {
  const run = (chains.get(key) ?? Promise.resolve()).then(f);
  const tail = run.catch(() => {});
  chains.set(key, tail);
  tail.then(() => {
    if (chains.get(key) === tail) chains.delete(key);
  });
  return run;
}

export function adminToken(): string {
  return readFileSync(join(HOME, "admin.token"), "utf8").trim();
}

export class AgoraError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function timed(url: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch (e) {
    if (e instanceof Error && (e.name === "TimeoutError" || e.name === "AbortError")) {
      throw new AgoraError(504, `agora daemon did not respond within ${REQUEST_TIMEOUT_MS / 1000}s`);
    }
    throw e;
  }
}

export async function call<T = any>(method: string, path: string, body?: unknown, token = adminToken()): Promise<T> {
  const r = await timed(URL_BASE + path, {
    method,
    signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    headers: { Authorization: `Bearer ${token}`, ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await r.text();
  if (!r.ok) {
    let msg = text;
    try {
      msg = JSON.parse(text).error || text;
    } catch {}
    throw new AgoraError(r.status, msg);
  }
  return (text ? JSON.parse(text) : undefined) as T;
}

export async function bytes(path: string): Promise<{ mime: string; data: Buffer }> {
  const r = await timed(URL_BASE + path, { headers: { Authorization: `Bearer ${adminToken()}` }, signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
  if (!r.ok) throw new AgoraError(r.status, await r.text());
  return { mime: r.headers.get("content-type") || "image/jpeg", data: Buffer.from(await r.arrayBuffer()) };
}

export function wsURL(path: string): string {
  return URL_BASE.replace(/^http/, "ws") + path;
}

export function linkedWorkspace(paseoWorkspaceId: string, name: string, create: boolean): Promise<string | null> {
  return serialize(`workspace:${paseoWorkspaceId}`, () => linkWorkspace(paseoWorkspaceId, name, create));
}

async function linkWorkspace(paseoWorkspaceId: string, name: string, create: boolean): Promise<string | null> {
  const known = loadState().workspaces[paseoWorkspaceId];
  if (known) {
    try {
      const w = await call("GET", `/v1/workspaces/${known}`);
      if (w.state !== "destroyed") return known;
    } catch {}
  }
  const all: any[] = await call("GET", "/v1/workspaces");
  const found = all.find((w) => w.labels?.paseo_workspace === paseoWorkspaceId && w.state !== "destroyed");
  if (found) {
    updateState((st) => {
      st.workspaces[paseoWorkspaceId] = found.id;
    });
    return found.id;
  }
  if (!create) return null;
  const w = await call("POST", "/v1/workspaces", { name: name || paseoWorkspaceId, labels: { paseo_workspace: paseoWorkspaceId } });
  updateState((st) => {
    st.workspaces[paseoWorkspaceId] = w.id;
  });
  return w.id;
}

export function agentCursor(agentId: string, title: string | null, workspace: string): Promise<{ cursor: string; token: string }> {
  return serialize(`agent:${agentId}`, () => ensureAgentCursor(agentId, title, workspace));
}

async function ensureAgentCursor(agentId: string, title: string | null, workspace: string): Promise<{ cursor: string; token: string }> {
  const known = loadState().agents[agentId];
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
  updateState((st) => {
    st.agents[agentId] = rec;
  });
  return rec;
}

export function agentTokenFile(agentId: string, token: string): string {
  return writeTokenFile(TOKEN_DIR, agentId, token);
}

export function releaseAgentCursor(agentId: string): Promise<void> {
  return serialize(`agent:${agentId}`, () => dropAgentCursor(agentId));
}

async function dropAgentCursor(agentId: string): Promise<void> {
  removeTokenFile(TOKEN_DIR, agentId);
  const known = loadState().agents[agentId];
  if (!known) return;
  try {
    await call("DELETE", `/v1/cursors/${encodeURIComponent(known.cursor)}`);
  } catch (e) {
    if (!(e instanceof AgoraError && e.status === 404)) throw e;
  }
  updateState((st) => {
    delete st.agents[agentId];
  });
}

export function nameFromCwd(cwd: string): string {
  return basename(cwd) || "paseo";
}

async function retireWorkspace(w: AgoraWorkspace): Promise<void> {
  const purge = w.state === "created" ? "?purge=1" : "";
  await call("DELETE", `/v1/workspaces/${encodeURIComponent(w.id)}${purge}`);
}

function forgetWorkspace(paseoWorkspaceId: string) {
  updateState((s) => {
    delete s.workspaces[paseoWorkspaceId];
  });
}

export async function releasePaseoWorkspace(paseoWorkspaceId: string): Promise<number> {
  return serialize(`workspace:${paseoWorkspaceId}`, async () => {
    const all: AgoraWorkspace[] = await call("GET", "/v1/workspaces");
    let n = 0;
    for (const w of all) {
      if (w.labels?.paseo_workspace !== paseoWorkspaceId || !idleWorkspace(w)) continue;
      await retireWorkspace(w);
      n++;
    }
    forgetWorkspace(paseoWorkspaceId);
    return n;
  });
}

async function livePaseoWorkspaces(paseo: PluginHookContext["paseo"]): Promise<Set<string>> {
  const live = new Set<string>();
  let cursor: string | undefined;
  for (let i = 0; i < 50; i++) {
    const r = await paseo.workspaces.list({ filter: { includeBackground: true, includeInternal: true }, page: { limit: 200, cursor } });
    for (const w of r.entries) live.add(w.id);
    if (!r.pageInfo?.hasMore || !r.pageInfo.nextCursor) return live;
    cursor = r.pageInfo.nextCursor;
  }
  throw new Error("too many Paseo workspace pages");
}

export async function reconcileWorkspaces(paseo: PluginHookContext["paseo"]): Promise<number> {
  const live = await livePaseoWorkspaces(paseo);
  const all: AgoraWorkspace[] = await call("GET", "/v1/workspaces");
  let n = 0;
  for (const w of orphanWorkspaces(all, live)) {
    const p = w.labels!.paseo_workspace;
    await serialize(`workspace:${p}`, async () => {
      await retireWorkspace(w);
      forgetWorkspace(p);
    }).then(
      () => n++,
      () => {},
    );
  }
  return n;
}

const RECONCILE_EVERY = 10 * 60 * 1000;
let reconciledAt = 0;

export async function reconcileAgentCursors(paseo: PluginHookContext["paseo"]): Promise<number | null> {
  if (Date.now() - reconciledAt < RECONCILE_EVERY) return null;
  reconciledAt = Date.now();
  const cursors: any[] = await call("GET", "/v1/cursors?active=1");
  const ids = new Set<string>(Object.keys(loadState().agents));
  for (const c of cursors) if (c.labels?.paseo_agent) ids.add(c.labels.paseo_agent);
  let removed = 0;
  for (const id of ids) {
    let gone: boolean;
    try {
      const r = await paseo.agents.ref(id).refresh();
      gone = !r || r.agent.archivedAt != null || r.agent.status === "closed";
    } catch {
      continue;
    }
    if (!gone) continue;
    for (const c of cursors) {
      if (c.labels?.paseo_agent !== id) continue;
      await call("DELETE", `/v1/cursors/${encodeURIComponent(c.id)}`).catch(() => {});
      removed++;
    }
    await releaseAgentCursor(id).catch(() => {});
  }
  return removed;
}
