import type { PluginTheme } from "@getpaseo/plugin";
import { useRpc } from "@getpaseo/plugin/client";
import { useEffect, useState, useSyncExternalStore } from "react";
import { Image, type LayoutChangeEvent, Pressable, Text, View } from "react-native";
import type { z } from "zod";
import { live, type liveCursor } from "../shared/agora";

export type Cur = z.output<typeof liveCursor>;
export type Region = { x: number; y: number; w: number; h: number; sw: number; sh: number };
export type Frame = { w: number; h: number; uri: string };

type LiveState = { frame: Frame | null; region: Region | null; cursors: Cur[]; locked: string | null; status: string | null };

const EMPTY: LiveState = { frame: null, region: null, cursors: [], locked: null, status: "connecting" };

export function useLive(opts: { workspace: string | null; cursor?: string; tier: "card" | "tile" | "full"; maxWidth?: number; metaOnly?: boolean; enabled: boolean }) {
  const poll = useRpc(live);
  const [s, setS] = useState<LiveState>(EMPTY);
  const { workspace, cursor, tier, maxWidth, metaOnly, enabled } = opts;
  useEffect(() => {
    if (!workspace || !enabled) return;
    let stop = false;
    let after = 0;
    let metaAfter = 0;
    let fails = 0;
    const loop = async () => {
      while (!stop) {
        try {
          const r = await poll({ workspace, cursor, tier, maxWidth, metaOnly, after, metaAfter });
          if (stop) return;
          fails = 0;
          after = r.seq;
          metaAfter = r.metaSeq;
          setS((p) => ({
            frame: r.frame ? { w: r.frame.w, h: r.frame.h, uri: `data:image/jpeg;base64,${r.frame.b64}` } : p.frame,
            region: r.region.w ? r.region : p.region,
            cursors: r.cursors ?? p.cursors,
            locked: r.locked,
            status: r.status,
          }));
        } catch (e) {
          if (stop) return;
          fails++;
          setS((p) => ({ ...p, status: e instanceof Error ? e.message : String(e) }));
          await new Promise<void>((res) => setTimeout(res, Math.min(10000, 1000 * fails)));
        }
      }
    };
    void loop();
    return () => {
      stop = true;
    };
  }, [workspace, cursor, tier, maxWidth, metaOnly, enabled]);
  return s;
}

const active = new Map<string, Map<string, number>>();
const subs = new Set<() => void>();
let version = 0;

function notify() {
  version++;
  for (const f of subs) f();
}

export function useIsNewestCard(workspace: string | null, key: string, ts: number): boolean {
  useEffect(() => {
    if (!workspace) return;
    let m = active.get(workspace);
    if (!m) active.set(workspace, (m = new Map()));
    m.set(key, ts);
    notify();
    return () => {
      m!.delete(key);
      if (!m!.size) active.delete(workspace);
      notify();
    };
  }, [workspace, key, ts]);
  useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => version,
  );
  if (!workspace) return false;
  const m = active.get(workspace);
  if (!m) return false;
  let best = "";
  let bestTs = -Infinity;
  for (const [k, t] of m) {
    if (t > bestTs || (t === bestTs && k > best)) {
      best = k;
      bestTs = t;
    }
  }
  return best === key;
}

export function statusColor(theme: PluginTheme, status: string): string {
  if (status === "needs_human" || status === "blocked") return theme.colors.statusWarning;
  if (status === "failed") return theme.colors.statusDanger;
  if (status === "executing" || status === "running" || status === "thinking") return theme.colors.statusSuccess;
  return theme.colors.foregroundMuted;
}

function Arrow({ color, outline }: { color: string; outline: string }) {
  return (
    <View style={{ position: "absolute", left: -1, top: -1, width: 16, height: 18 }}>
      <View
        style={{
          position: "absolute",
          left: 0,
          top: 0,
          width: 0,
          height: 0,
          borderTopWidth: 17,
          borderRightWidth: 12,
          borderTopColor: outline,
          borderRightColor: "transparent",
        }}
      />
      <View
        style={{
          position: "absolute",
          left: 2,
          top: 3,
          width: 0,
          height: 0,
          borderTopWidth: 11,
          borderRightWidth: 8,
          borderTopColor: color,
          borderRightColor: "transparent",
        }}
      />
    </View>
  );
}

export function DesktopView({
  theme,
  frame,
  region,
  cursors,
  highlight,
  showOthers = true,
  small,
  onTap,
  onLongTap,
}: {
  theme: PluginTheme;
  frame: Frame | null;
  region: Region | null;
  cursors: Cur[];
  highlight?: string | null;
  showOthers?: boolean;
  small?: boolean;
  onTap?: (x: number, y: number) => void;
  onLongTap?: (x: number, y: number) => void;
}) {
  const [boxW, setBoxW] = useState(0);
  const onLayout = (e: LayoutChangeEvent) => setBoxW(e.nativeEvent.layout.width);
  const rw = region?.w || 0;
  const rh = region?.h || 0;
  const scale = rw && boxW ? boxW / rw : 0;
  const toScreen = (lx: number, ly: number): [number, number] => [Math.round((region?.x || 0) + lx / scale), Math.round((region?.y || 0) + ly / scale)];
  const aspect = rw && rh ? rw / rh : frame ? frame.w / frame.h : 16 / 10;
  return (
    <View style={{ width: "100%", aspectRatio: aspect, backgroundColor: theme.colors.surface2, overflow: "hidden" }} onLayout={onLayout}>
      {frame && (
        <Pressable
          style={{ flex: 1 }}
          disabled={!onTap}
          onPress={(e) => scale && onTap?.(...toScreen(e.nativeEvent.locationX, e.nativeEvent.locationY))}
          onLongPress={(e) => scale && onLongTap?.(...toScreen(e.nativeEvent.locationX, e.nativeEvent.locationY))}
        >
          <Image source={{ uri: frame.uri }} style={{ width: "100%", height: "100%" }} resizeMode="stretch" fadeDuration={0} />
        </Pressable>
      )}
      {frame &&
        region &&
        scale > 0 &&
        cursors.map((c) => {
          if (!c.x && !c.y) return null;
          if (!showOthers && c.id !== highlight) return null;
          const lx = (c.x - region.x) * scale;
          const ly = (c.y - region.y) * scale;
          if (lx < -2 || ly < -2 || lx > boxW + 2 || ly > rh * scale + 2) return null;
          const dim = highlight && c.id !== highlight;
          const busy = c.status !== "idle" && c.status !== "pending" && c.status !== "done";
          return (
            <View key={c.id} pointerEvents="none" style={{ position: "absolute", left: lx, top: ly, opacity: dim ? 0.75 : 1 }}>
              <Arrow color={c.color} outline={theme.colors.surface0} />
              <View
                style={{
                  position: "absolute",
                  left: 12,
                  top: 14,
                  flexDirection: "row",
                  alignItems: "center",
                  gap: 4,
                  paddingHorizontal: 5,
                  paddingVertical: 1,
                  borderRadius: 4,
                  backgroundColor: c.color,
                  maxWidth: small ? 140 : 220,
                }}
              >
                <Text numberOfLines={1} style={{ color: "#ffffff", fontSize: small ? 10 : 11, fontWeight: "600", flexShrink: 1 }}>
                  {c.name}
                </Text>
                {busy && !small && (
                  <Text numberOfLines={1} style={{ color: "#ffffff", fontSize: 10, opacity: 0.85 }}>
                    {c.status.replace("_", " ")}
                  </Text>
                )}
              </View>
            </View>
          );
        })}
    </View>
  );
}
