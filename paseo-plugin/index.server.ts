import type { PluginServerContext } from "@getpaseo/plugin/server";
import { agentCursor, bytes, call, CARBON_BIN, linkedWorkspace, nameFromCwd } from "./server/carbon";
import { control, ensureWorkspace, getShot, listCursors, listWorkspaces, liveFrame, sendInput } from "./shared/carbon";
import { pollFrame } from "./server/live";

const MCP_PROVIDERS = ["claude", "codex"];

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export default function contribute(server: PluginServerContext) {
  server.handle(listWorkspaces, async ({ paseoWorkspaceId }) => {
    try {
      const all: any[] = await call("GET", "/v1/workspaces");
      const linked = paseoWorkspaceId ? await linkedWorkspace(paseoWorkspaceId, "", false) : null;
      return {
        items: all
          .filter((w) => w.state !== "destroyed")
          .map((w) => ({ id: w.id, name: w.name, state: w.state, cursors: w.cursors || 0, viewers: w.viewers || 0, linked: w.id === linked })),
        linked,
        error: null,
      };
    } catch (e) {
      return { items: [], linked: null, error: errText(e) };
    }
  });

  server.handle(ensureWorkspace, async ({ paseoWorkspaceId, name }) => {
    const id = await linkedWorkspace(paseoWorkspaceId, name, true);
    return { id: id! };
  });

  server.handle(getShot, async ({ id }) => {
    const r = await bytes(`/v1/shots/${encodeURIComponent(id)}`);
    return { mime: r.mime, base64: r.data.toString("base64") };
  });

  server.handle(sendInput, async (input) => {
    try {
      const { workspace, ...rest } = input;
      await call("POST", `/v1/workspaces/${workspace}/input`, rest);
      return { ok: true, error: null };
    } catch (e) {
      return { ok: false, error: errText(e) };
    }
  });

  server.handle(control, async ({ workspace, op }) => {
    try {
      await call("POST", `/v1/workspaces/${workspace}/${op}`, {});
      return { ok: true, error: null };
    } catch (e) {
      return { ok: false, error: errText(e) };
    }
  });

  server.handle(listCursors, async ({ workspace }) => {
    const cs: any[] = await call("GET", `/v1/cursors?workspace=${workspace}&active=1`);
    const locks: any[] = await call("GET", `/v1/workspaces/${workspace}/locks`).catch(() => []);
    const ws = locks.find((l) => l.surface === "workspace" && !l.expires);
    return {
      items: cs.map((c) => ({ id: c.id, name: c.name, status: c.status, detail: c.detail || "", color: c.color, x: c.x || 0, y: c.y || 0, goal: c.goal || "", parent: c.parent || "" })),
      locked: ws ? ws.holder : null,
    };
  });

  server.handle(liveFrame, pollFrame);

  server.addHook("agent.open.before", async ({ request }) => {
    if (request.purpose === "history") return;
    if (!MCP_PROVIDERS.some((p) => request.provider === p || request.provider.startsWith(p + "/"))) return;
    if (request.labels?.["carbon/enabled"] === "false" || !request.workspaceId) return;
    try {
      const ws = await linkedWorkspace(request.workspaceId, nameFromCwd(request.cwd), true);
      if (!ws) return;
      const cur = await agentCursor(request.agentId, request.title ?? null, ws);
      return {
        ...request,
        mcpServers: {
          ...request.mcpServers,
          carbon: {
            type: "stdio" as const,
            command: CARBON_BIN,
            args: ["mcp"],
            env: { CARBON_URL: process.env.CARBON_URL || "http://127.0.0.1:7788", CARBON_TOKEN: cur.token, CARBON_WORKSPACE: ws },
          },
        },
      };
    } catch (e) {
      console.error("carbon: could not attach MCP server:", errText(e));
      return;
    }
  });

  return () => {};
}
