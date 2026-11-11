import type { PluginHookContext, PluginServerContext } from "@getpaseo/plugin/server";
import { agentCursor, bytes, call, AGORA_BIN, linkedWorkspace, nameFromCwd, reconcileAgentCursors, reconcileWorkspaces, releaseAgentCursor, releasePaseoWorkspace } from "./server/agora";
import { control, ensureWorkspace, getShot, listWorkspaces, live, liveInput, resolveWorkspace } from "./shared/agora";
import { closeAll, pollLive, sendLiveInput, wakeHubs } from "./server/live";

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

  server.handle(control, async ({ workspace, op }) => {
    try {
      await call("POST", `/v1/workspaces/${workspace}/${op}`, {});
      if (op === "resume") wakeHubs(workspace);
      return { ok: true, error: null };
    } catch (e) {
      return { ok: false, error: errText(e) };
    }
  });

  server.handle(live, pollLive);

  server.handle(liveInput, async (input) => {
    try {
      return await sendLiveInput(input);
    } catch (e) {
      return { ok: false, error: errText(e) };
    }
  });

  server.handle(resolveWorkspace, async ({ paseoWorkspaceId }) => {
    try {
      return { id: await linkedWorkspace(paseoWorkspaceId, "", false) };
    } catch {
      return { id: null };
    }
  });

  const reconcile = (paseo: PluginHookContext["paseo"]) => {
    reconcileAgentCursors(paseo)
      .then(async (n) => {
        if (n === null) return;
        if (n) console.log(`agora: removed ${n} cursors of closed or archived Paseo agents`);
        const w = await reconcileWorkspaces(paseo);
        if (w) console.log(`agora: removed ${w} workspaces of archived Paseo workspaces`);
      })
      .catch((e) => console.error("agora: could not reconcile:", errText(e)));
  };

  server.addHook("agent.open.before", async ({ request }, { paseo }) => {
    reconcile(paseo);
    if (request.purpose === "history") return;
    if (!MCP_PROVIDERS.some((p) => request.provider === p || request.provider.startsWith(p + "/"))) return;
    if (request.labels?.["agora/enabled"] === "false" || !request.workspaceId) return;
    try {
      const ws = await linkedWorkspace(request.workspaceId, nameFromCwd(request.cwd), true);
      if (!ws) return;
      const cur = await agentCursor(request.agentId, request.title ?? null, ws);
      const env: Record<string, string> = { AGORA_URL: process.env.AGORA_URL || "http://127.0.0.1:7788", AGORA_TOKEN: cur.token, AGORA_WORKSPACE: ws };
      if (request.provider === "codex" || request.provider.startsWith("codex/")) env.AGORA_LEAN = "1";
      return {
        ...request,
        mcpServers: {
          ...request.mcpServers,
          agora: {
            type: "stdio" as const,
            command: AGORA_BIN,
            args: ["mcp"],
            env,
          },
        },
      };
    } catch (e) {
      console.error("agora: could not attach MCP server:", errText(e));
      return;
    }
  });

  const release = async ({ agent }: { agent: { id: string } }, { paseo }: PluginHookContext) => {
    reconcile(paseo);
    try {
      await releaseAgentCursor(agent.id);
    } catch (e) {
      console.error("agora: could not release cursor:", errText(e));
    }
  };
  server.addHook("agent.close.after", release);
  server.addHook("agent.archive.after", release);
  server.addHook("workspace.archive.after", async ({ workspace }) => {
    try {
      const n = await releasePaseoWorkspace(workspace.id);
      if (n) console.log(`agora: removed ${n} workspaces of archived Paseo workspace ${workspace.id}`);
    } catch (e) {
      console.error("agora: could not release workspace:", errText(e));
    }
  });

  return () => closeAll();
}
