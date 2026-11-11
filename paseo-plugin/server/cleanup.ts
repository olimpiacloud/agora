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
