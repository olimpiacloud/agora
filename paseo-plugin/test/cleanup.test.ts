import { describe, expect, test } from "bun:test";
import { allWorkspaceIds, goneAgentIds, idleWorkspace, orphanWorkspaces, stillOrphan, withinGrace, type AgentState, type AgoraWorkspace, type WorkspacePage } from "../server/cleanup";

const ws = (id: string, paseo: string | null, extra: Partial<AgoraWorkspace> = {}): AgoraWorkspace => ({
  id,
  state: "created",
  cursors: 0,
  viewers: 0,
  labels: paseo ? { paseo_workspace: paseo } : {},
  ...extra,
});

describe("orphanWorkspaces", () => {
  test("returns idle workspaces linked to Paseo workspaces that no longer exist", () => {
    const all = [ws("a", "wks_live"), ws("b", "wks_gone"), ws("c", "wks_gone", { state: "suspended" })];
    expect(orphanWorkspaces(all, new Set(["wks_live"])).map((w) => w.id)).toEqual(["b", "c"]);
  });

  test("never touches anything when Paseo reports no workspaces", () => {
    expect(orphanWorkspaces([ws("a", "wks_gone")], new Set())).toEqual([]);
  });

  test("keeps unlabeled, busy and destroyed workspaces", () => {
    const all = [
      ws("manual", null),
      ws("cursors", "wks_gone", { cursors: 1 }),
      ws("watched", "wks_gone", { viewers: 2 }),
      ws("dead", "wks_gone", { state: "destroyed" }),
    ];
    expect(orphanWorkspaces(all, new Set(["wks_live"]))).toEqual([]);
  });
});

describe("idleWorkspace", () => {
  test("is false for destroyed or busy workspaces", () => {
    expect(idleWorkspace(ws("a", "x"))).toBe(true);
    expect(idleWorkspace(ws("a", "x", { state: "destroyed" }))).toBe(false);
    expect(idleWorkspace(ws("a", "x", { cursors: 1 }))).toBe(false);
  });
});

describe("allWorkspaceIds", () => {
  test("follows the pages to the end", async () => {
    const calls: (string | undefined)[] = [];
    const pages: WorkspacePage[] = [
      { entries: [{ id: "a" }], pageInfo: { hasMore: true, nextCursor: "c1" } },
      { entries: [{ id: "b" }], pageInfo: { hasMore: false } },
    ];
    const ids = await allWorkspaceIds(async (c) => {
      calls.push(c);
      return pages[calls.length - 1];
    });
    expect([...ids].sort()).toEqual(["a", "b"]);
    expect(calls).toEqual([undefined, "c1"]);
  });

  test("rejects a page that has more but no cursor", async () => {
    await expect(allWorkspaceIds(async () => ({ entries: [{ id: "a" }], pageInfo: { hasMore: true } }))).rejects.toThrow();
  });

  test("rejects when the listing fails", async () => {
    await expect(
      allWorkspaceIds(async () => {
        throw new Error("down");
      }),
    ).rejects.toThrow();
  });

  test("rejects after the page limit", async () => {
    let calls = 0;
    await expect(
      allWorkspaceIds(async () => {
        calls++;
        return { entries: [], pageInfo: { hasMore: true, nextCursor: "x" } };
      }, 3),
    ).rejects.toThrow();
    expect(calls).toBe(3);
  });

  test("accepts a single page without page info", async () => {
    const ids = await allWorkspaceIds(async () => ({ entries: [{ id: "a" }, { id: "b" }] }));
    expect([...ids].sort()).toEqual(["a", "b"]);
  });
});

describe("goneAgentIds", () => {
  const agents: Record<string, AgentState | Error> = {
    closed: { archivedAt: null, status: "closed" },
    archived: { archivedAt: "2026-01-01", status: "idle" },
    missing: null,
    running: { archivedAt: null, status: "running" },
    broken: new Error("unreachable"),
    skipped: { archivedAt: null, status: "closed" },
  };
  const refreshed: string[] = [];
  const refresh = async (id: string): Promise<AgentState> => {
    refreshed.push(id);
    const a = agents[id];
    if (a instanceof Error) throw a;
    return a;
  };

  test("returns closed, archived and missing agents only", async () => {
    const gone = await goneAgentIds(Object.keys(agents), refresh, (id) => id === "skipped");
    expect(gone.sort()).toEqual(["archived", "closed", "missing"]);
  });

  test("never refreshes a skipped agent", () => {
    expect(refreshed).not.toContain("skipped");
    expect(refreshed.sort()).toEqual(["archived", "broken", "closed", "missing", "running"]);
  });
});

describe("withinGrace", () => {
  test("is true only for a recent open", () => {
    expect(withinGrace(undefined, 1500, 1000)).toBe(false);
    expect(withinGrace(1000, 1500, 1000)).toBe(true);
    expect(withinGrace(1000, 2000, 1000)).toBe(false);
  });
});

describe("stillOrphan", () => {
  test("holds only for an idle workspace with the same label", () => {
    expect(stillOrphan(ws("a", "wks_gone"), "wks_gone")).toBe(true);
    expect(stillOrphan(ws("a", "wks_other"), "wks_gone")).toBe(false);
    expect(stillOrphan(ws("a", null), "wks_gone")).toBe(false);
    expect(stillOrphan(ws("a", "wks_gone", { cursors: 1 }), "wks_gone")).toBe(false);
    expect(stillOrphan(ws("a", "wks_gone", { viewers: 1 }), "wks_gone")).toBe(false);
    expect(stillOrphan(ws("a", "wks_gone", { state: "destroyed" }), "wks_gone")).toBe(false);
  });
});
