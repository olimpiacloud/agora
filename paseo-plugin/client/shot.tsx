import { type PluginClientContext, type PluginTimelineItemProps, useAgent, useRpc } from "@getpaseo/plugin/client";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Image, Pressable, Text, View } from "react-native";
import { z } from "zod";
import { getShot } from "../shared/agora";

export const shotSchema = z.object({
  shot: z.string().nullable(),
  tool: z.string(),
  status: z.string(),
  inline: z.string().nullable(),
  mime: z.string().nullable(),
});

export function makeShotCard(client: PluginClientContext) {
  return function ShotCard({ item, theme, layout, agentId }: PluginTimelineItemProps<z.output<typeof shotSchema>>) {
    const fetchShot = useRpc(getShot);
    const workspaceId = useAgent(agentId, (a) => a.workspaceId);
    const { shot, inline, mime, tool, status } = item.data;
    const q = useQuery({
      queryKey: ["agora-shot", shot],
      queryFn: () => fetchShot({ id: shot! }),
      enabled: !!shot && !inline,
      staleTime: Infinity,
      retry: 1,
    });
    const uri = inline ? `data:${mime || "image/jpeg"};base64,${inline}` : q.data ? `data:${q.data.mime};base64,${q.data.base64}` : null;
    const styles = useMemo(
      () => ({
        card: { borderWidth: 1, borderColor: theme.colors.border, backgroundColor: theme.colors.surface1, borderRadius: 10, overflow: "hidden" as const, maxWidth: layout.compact ? "100%" as const : 720, marginVertical: 4 },
        head: { flexDirection: "row" as const, alignItems: "center" as const, gap: 8, paddingHorizontal: 10, paddingVertical: 6 },
        title: { color: theme.colors.foreground, fontSize: 12, flex: 1 },
        muted: { color: theme.colors.foregroundMuted, fontSize: 12 },
        link: { color: theme.colors.accent, fontSize: 12 },
      }),
      [theme, layout.compact],
    );
    return (
      <View style={styles.card}>
        <View style={styles.head}>
          <Text style={styles.title} numberOfLines={1}>
            {tool.replace(/^mcp__agora__/, "agora · ")}
          </Text>
          {workspaceId && (
            <Pressable accessibilityRole="button" accessibilityLabel="Open Agora live view" onPress={() => client.openPanel("live", { workspaceId })}>
              <Text style={styles.link}>Live view ↗</Text>
            </Pressable>
          )}
        </View>
        {uri ? (
          <Image source={{ uri }} style={{ width: "100%", aspectRatio: 16 / 10 }} resizeMode="contain" />
        ) : (
          <Text style={[styles.muted, { padding: 10 }]}>{status === "running" ? "capturing…" : q.isError ? "screenshot expired from Agora's cache" : "loading…"}</Text>
        )}
      </View>
    );
  };
}
