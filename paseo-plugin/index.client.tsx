import type { PluginClientContext } from "@getpaseo/plugin/client";
import { LivePanel } from "./client/live";
import { cardSchema, makeLiveCard } from "./client/card";
import { BROWSE_RE, SHOT_RE } from "./shared/agora";

function findInline(v: unknown, depth = 0): { data: string; mime: string } | null {
  if (!v || depth > 6) return null;
  if (Array.isArray(v)) {
    for (const x of v) {
      const r = findInline(x, depth + 1);
      if (r) return r;
    }
    return null;
  }
  if (typeof v === "object") {
    const o = v as Record<string, unknown>;
    if (o.type === "image" && typeof o.data === "string" && o.data.length < 400000) {
      return { data: o.data, mime: typeof o.mimeType === "string" ? o.mimeType : "image/jpeg" };
    }
    for (const k of Object.keys(o)) {
      const r = findInline(o[k], depth + 1);
      if (r) return r;
    }
  }
  return null;
}

export default function contribute(client: PluginClientContext) {
  client.addWorkspacePanel({ id: "live", title: "Agora", icon: "MonitorPlay", Component: LivePanel });
  client.addCommandCenterItem({
    id: "open-live",
    title: "Agora: open live desktop",
    icon: "MonitorPlay",
    context: "workspace",
    onSelect({ openPanel }) {
      openPanel("live");
    },
  });
  client.addTimelineTransformer({
    id: "agora-live",
    query: { itemType: "tool_call" },
    transform({ item }) {
      const name = item.name || "";
      if (!/agora/i.test(name)) return undefined;
      const browse = BROWSE_RE.test(name);
      if (!browse && !/screenshot/i.test(name)) return undefined;
      let raw = "";
      try {
        raw = JSON.stringify(item.detail ?? null);
      } catch {}
      const m = browse ? null : raw.match(SHOT_RE);
      const inline = browse || m ? null : findInline(item.detail);
      if (!browse && !m && !inline && item.status !== "running") return undefined;
      const url = browse ? (raw.match(/"url"\s*:\s*"((?:[^"\\]|\\.)*)"/)?.[1] ?? null) : null;
      return {
        items: [
          {
            type: "plugin",
            kind: "agora-live",
            version: 1,
            data: { mode: browse ? "browse" : "shot", call: item.callId || "", tool: name, status: item.status, shot: m ? m[1] : null, inline: inline?.data ?? null, mime: inline?.mime ?? null, url },
          },
        ],
      };
    },
  });
  client.addTimelineRenderer({ kind: "agora-live", version: 1, schema: cardSchema, Component: makeLiveCard(client) });
  return () => {};
}
