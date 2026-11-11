import { describe, expect, test } from "bun:test";
import { idleWorkspace, orphanWorkspaces, type AgoraWorkspace } from "../server/cleanup";

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
