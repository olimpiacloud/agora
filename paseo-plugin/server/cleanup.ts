export interface AgoraWorkspace {
  id: string;
  state: string;
  cursors?: number;
  viewers?: number;
  labels?: Record<string, string>;
}

export function idleWorkspace(w: AgoraWorkspace): boolean {
  return w.state !== "destroyed" && (w.cursors || 0) === 0 && (w.viewers || 0) === 0;
}

export function orphanWorkspaces(all: AgoraWorkspace[], live: Set<string>): AgoraWorkspace[] {
  if (live.size === 0) return [];
  return all.filter((w) => {
    const p = w.labels?.paseo_workspace;
    return !!p && !live.has(p) && idleWorkspace(w);
  });
}

export interface WorkspacePage {
  entries: { id: string }[];
  pageInfo?: { hasMore?: boolean; nextCursor?: string | null } | null;
}

export async function allWorkspaceIds(list: (cursor: string | undefined) => Promise<WorkspacePage>, maxPages = 50): Promise<Set<string>> {
  const ids = new Set<string>();
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const r = await list(cursor);
    for (const w of r.entries) ids.add(w.id);
    if (!r.pageInfo?.hasMore) return ids;
    if (!r.pageInfo.nextCursor) throw new Error("Paseo workspace listing ended without a cursor");
    cursor = r.pageInfo.nextCursor;
  }
  throw new Error("too many Paseo workspace pages");
}

export type AgentState = { archivedAt: string | null; status: string } | null;

export async function goneAgentIds(ids: Iterable<string>, refresh: (id: string) => Promise<AgentState>, skip: (id: string) => boolean): Promise<string[]> {
  const gone: string[] = [];
  for (const id of ids) {
    if (skip(id)) continue;
    let a: AgentState;
    try {
      a = await refresh(id);
    } catch {
      continue;
    }
    if (!a || a.archivedAt != null || a.status === "closed") gone.push(id);
  }
  return gone;
}

export function withinGrace(at: number | undefined, now: number, graceMs: number): boolean {
  return at !== undefined && now - at < graceMs;
}

export function stillOrphan(fresh: AgoraWorkspace, paseoWorkspaceId: string): boolean {
  return fresh.labels?.paseo_workspace === paseoWorkspaceId && idleWorkspace(fresh);
}
