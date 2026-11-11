import { type PluginClientContext, type PluginTimelineItemProps, useAgent, useRpc } from "@getpaseo/plugin/client";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Image, Pressable, Text, View } from "react-native";
import { z } from "zod";
import { getShot, resolveWorkspace } from "../shared/agora";
import { cardBadge } from "./badge";
import { DesktopView, statusColor, useIsNewestCard, useLive } from "./stream";

export const cardSchema = z.object({
  mode: z.enum(["shot", "browse"]),
  call: z.string(),
  tool: z.string(),
  status: z.string(),
  shot: z.string().nullable(),
  inline: z.string().nullable(),
  mime: z.string().nullable(),
  url: z.string().nullable(),
});

export function makeLiveCard(client: PluginClientContext) {
  return function LiveCard({ item, theme, layout, agentId, timestamp }: PluginTimelineItemProps<z.output<typeof cardSchema>>) {
    const { mode, call, shot, inline, mime, tool, status, url } = item.data;
    const workspaceId = useAgent(agentId, (a) => a.workspaceId);
    const resolve = useRpc(resolveWorkspace);
    const fetchShot = useRpc(getShot);
    const [paused, setPaused] = useState(false);
    const wsq = useQuery({
      queryKey: ["agora-ws", workspaceId],
      queryFn: () => resolve({ paseoWorkspaceId: workspaceId! }),
      enabled: !!workspaceId,
      staleTime: 60000,
    });
    const agoraWs = wsq.data?.id ?? null;
    const newest = useIsNewestCard(agoraWs, call || shot || String(timestamp.getTime()), timestamp.getTime());
    const streaming = newest && !paused;
    const lv = useLive({ workspace: agoraWs, tier: "card", meta: true, enabled: streaming });
    const sq = useQuery({
      queryKey: ["agora-shot", shot],
      queryFn: () => fetchShot({ id: shot! }),
      enabled: !!shot && !inline && !lv.frame,
      staleTime: Infinity,
      retry: 1,
    });
    const stored = inline ? `data:${mime || "image/jpeg"};base64,${inline}` : sq.data ? `data:${sq.data.mime};base64,${sq.data.base64}` : null;
    const mine = lv.cursors.find((c) => c.agent === agentId);
    const others = lv.cursors.filter((c) => c.id !== mine?.id);
    const styles = useMemo(
      () => ({
        card: { borderWidth: 1, borderColor: theme.colors.border, backgroundColor: theme.colors.surface1, borderRadius: 10, overflow: "hidden" as const, width: "100%" as const, maxWidth: mode === "browse" ? 460 : 720, marginVertical: 4 },
        head: { flexDirection: "row" as const, alignItems: "center" as const, gap: 8, paddingHorizontal: 10, paddingVertical: 6 },
        title: { color: theme.colors.foreground, fontSize: 12, flexShrink: 1 },
        muted: { color: theme.colors.foregroundMuted, fontSize: 12 },
        link: { color: theme.colors.accent, fontSize: 12 },
        badge: { flexDirection: "row" as const, alignItems: "center" as const, gap: 4, paddingHorizontal: 6, paddingVertical: 1, borderRadius: 6, borderWidth: 1, borderColor: theme.colors.border },
        dot: { width: 6, height: 6, borderRadius: 3 },
        agents: { flexDirection: "row" as const, flexWrap: "wrap" as const, gap: 6, paddingHorizontal: 10, paddingVertical: 6, borderTopWidth: 1, borderColor: theme.colors.border },
      }),
      [theme, mode],
    );
    const open = () => workspaceId && client.openPanel("live", { workspaceId });
    const label = mode === "browse" ? url || tool.replace(/^.*agora__/, "") : tool.replace(/^mcp__agora__/, "agora · ");
    const hasLive = !!lv.frame && !!lv.region;
    const badge = cardBadge(streaming, newest, lv.status);
    const showPreview = mode === "shot" || hasLive;
    return (
      <View style={styles.card}>
        <View style={styles.head}>
          <Text style={styles.title} numberOfLines={1}>
            {mode === "browse" ? (tool.endsWith("navigate") ? "navigate · " : "open · ") : ""}
            {label}
          </Text>
          <View style={{ flex: 1 }} />
          {hasLive && (
            <Pressable accessibilityRole="button" accessibilityLabel={streaming ? "Pause live preview" : "Resume live preview"} onPress={() => setPaused(!paused)} style={styles.badge} disabled={!newest}>
              <View style={[styles.dot, { backgroundColor: badge.live ? theme.colors.statusDanger : theme.colors.foregroundMuted }]} />
              <Text style={styles.muted}>{badge.label}</Text>
            </Pressable>
          )}
          {workspaceId && (
            <Pressable accessibilityRole="button" accessibilityLabel="Open Agora panel" onPress={open}>
              <Text style={styles.link}>Open ↗</Text>
            </Pressable>
          )}
        </View>
        {showPreview && (
          <Pressable accessibilityRole="button" accessibilityLabel="Open Agora panel" onPress={open}>
            {hasLive ? (
              <DesktopView theme={theme} frame={lv.frame} region={lv.region} cursors={lv.cursors} highlight={mine?.id} small={layout.compact} />
            ) : stored ? (
              <Image source={{ uri: stored }} style={{ width: "100%", aspectRatio: 16 / 10, backgroundColor: theme.colors.surface2 }} resizeMode="contain" />
            ) : (
              <Text style={[styles.muted, { padding: 10 }]}>{status === "running" ? "capturing…" : sq.isError ? "screenshot expired from Agora's cache" : "loading…"}</Text>
            )}
          </Pressable>
        )}
        {hasLive && lv.cursors.length > 0 && (
          <View style={styles.agents}>
            {[...(mine ? [mine] : []), ...others].map((c) => (
              <View key={c.id} style={{ flexDirection: "row", alignItems: "center", gap: 5, maxWidth: "100%" }}>
                <View style={[styles.dot, { width: 8, height: 8, borderRadius: 2, backgroundColor: c.color }]} />
                <Text style={[styles.muted, { color: theme.colors.foreground }]} numberOfLines={1}>
                  {c.name}
                  {c.id === mine?.id ? " (this agent)" : ""}
                </Text>
                <Text style={[styles.muted, { color: statusColor(theme, c.status) }]}>{c.status.replace("_", " ")}</Text>
              </View>
            ))}
          </View>
        )}
      </View>
    );
  };
}
