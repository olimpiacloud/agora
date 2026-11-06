import type { PluginClientContext } from "@getpaseo/plugin/client";
import { LivePanel } from "./client/live";
import { makeShotCard, shotSchema } from "./client/shot";
import { SHOT_RE } from "./shared/carbon";

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
  client.addWorkspacePanel({ id: "live", title: "Carbon", icon: "MonitorPlay", Component: LivePanel });
  client.addCommandCenterItem({
    id: "open-live",
    title: "Carbon: open live desktop",
    icon: "MonitorPlay",
    context: "workspace",
    onSelect({ openPanel }) {
      openPanel("live");
    },
  });
  client.addTimelineTransformer({
    id: "carbon-screenshots",
    query: { itemType: "tool_call" },
    transform({ item }) {
      const name = item.name || "";
      if (!/carbon/i.test(name) || !/screenshot/i.test(name)) return undefined;
      let raw = "";
      try {
        raw = JSON.stringify(item.detail ?? null);
      } catch {}
      const m = raw.match(SHOT_RE);
      const inline = m ? null : findInline(item.detail);
      if (!m && !inline && item.status !== "running") return undefined;
      return {
        items: [
          {
            type: "plugin",
            kind: "carbon-shot",
            version: 1,
            data: { shot: m ? m[1] : null, tool: name, status: item.status, inline: inline?.data ?? null, mime: inline?.mime ?? null },
          },
        ],
      };
    },
  });
  client.addTimelineRenderer({ kind: "carbon-shot", version: 1, schema: shotSchema, Component: makeShotCard(client) });
  return () => {};
}
