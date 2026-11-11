import type { PluginTheme } from "@getpaseo/plugin";
import { useRpc } from "@getpaseo/plugin/client";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { type GestureResponderEvent, Image, type LayoutChangeEvent, Platform, Text, View } from "react-native";
import type { z } from "zod";
import { live, type liveCursor, type ViewerMsg } from "../shared/agora";
import { click, createSeat, createWheel, isPasteKey, PASTE_KEYS, throttle } from "./input";
import { useSmoothPoint } from "./smooth";

export type Cur = z.output<typeof liveCursor>;
export type Region = { x: number; y: number; w: number; h: number; sw: number; sh: number };
export type Frame = { w: number; h: number; uri: string };

type LiveState = { frame: Frame | null; region: Region | null; cursors: Cur[]; locked: string | null; status: string | null; viewer: string | null };

const EMPTY: LiveState = { frame: null, region: null, cursors: [], locked: null, status: "connecting", viewer: null };

export function useLive(opts: { workspace: string | null; cursor?: string; tier: "card" | "tile" | "full"; maxWidth?: number; meta?: boolean; enabled: boolean }) {
  const poll = useRpc(live);
  const [s, setS] = useState<LiveState>(EMPTY);
  const { workspace, cursor, tier, maxWidth, meta, enabled } = opts;
  useEffect(() => {
    if (!workspace || !enabled) return;
    let stop = false;
    let after = 0;
    let metaAfter = 0;
    let fails = 0;
    const loop = async () => {
      while (!stop) {
        try {
          const r = await poll({ workspace, cursor, tier, maxWidth, after, metaAfter: meta ? metaAfter : undefined });
          if (stop) return;
          fails = 0;
          after = r.seq;
          metaAfter = r.metaSeq;
          setS((p) => ({
            frame: r.frame ? { w: r.frame.w, h: r.frame.h, uri: `data:image/jpeg;base64,${r.frame.b64}` } : p.frame,
            region: r.region.w ? r.region : p.region,
            cursors: r.cursors ?? p.cursors,
            locked: meta ? r.locked : p.locked,
            status: r.status,
            viewer: r.viewer,
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
  }, [workspace, cursor, tier, maxWidth, meta, enabled]);
  return s;
}

type MetaState = { cursors: Cur[]; locked: string | null; status: string | null };

const NO_META: MetaState = { cursors: [], locked: null, status: null };

export function useCursors(workspace: string | null, enabled: boolean) {
  const poll = useRpc(live);
  const [s, setS] = useState<MetaState>(NO_META);
  useEffect(() => {
    setS(NO_META);
    if (!workspace || !enabled) return;
    let stop = false;
    let metaAfter = 0;
    let fails = 0;
    const loop = async () => {
      while (!stop) {
        try {
          const r = await poll({ workspace, tier: "full", metaOnly: true, after: 0, metaAfter });
          if (stop) return;
          fails = 0;
          metaAfter = r.metaSeq;
          setS((p) => (r.cursors || r.locked !== p.locked || p.status ? { cursors: r.cursors ?? p.cursors, locked: r.locked, status: null } : p));
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
  }, [workspace, enabled]);
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

function CursorMark({ theme, cursor: c, region, scale, width, height, dim, small }: { theme: PluginTheme; cursor: Cur; region: Region; scale: number; width: number; height: number; dim: boolean; small?: boolean }) {
  const [x, y] = useSmoothPoint(c.x, c.y);
  const lx = (x - region.x) * scale;
  const ly = (y - region.y) * scale;
  if (lx < -2 || ly < -2 || lx > width + 2 || ly > height + 2) return null;
  const busy = c.status !== "idle" && c.status !== "pending" && c.status !== "done";
  return (
    <View pointerEvents="none" style={{ position: "absolute", left: lx, top: ly, opacity: dim ? 0.75 : 1 }}>
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
}

function useWebInput(node: { current: unknown }, toScreen: (lx: number, ly: number) => [number, number], onInput: ((msgs: ViewerMsg[]) => void) | undefined, active: boolean) {
  const latest = useRef(toScreen);
  latest.current = toScreen;
  useEffect(() => {
    const el = node.current as HTMLElement | null;
    if (Platform.OS !== "web" || !active || !onInput || !el || typeof el.addEventListener !== "function") return;
    el.tabIndex = 0;
    el.style.outline = "none";
    el.style.cursor = "default";
    el.style.userSelect = "none";
    el.style.touchAction = "none";
    const send = (msgs: ViewerMsg[]) => {
      if (msgs.length) onInput(msgs);
    };
    const at = (e: MouseEvent): [number, number] => {
      const r = el.getBoundingClientRect();
      return latest.current(Math.min(Math.max(e.clientX - r.left, 0), r.width - 1), Math.min(Math.max(e.clientY - r.top, 0), r.height - 1));
    };
    const seat = createSeat();
    const wheel = createWheel();
    const moves = throttle(30, ([x, y]: [number, number]) => send(seat.move(x, y)));
    const down = (e: PointerEvent) => {
      e.preventDefault();
      el.focus({ preventScroll: true });
      el.setPointerCapture?.(e.pointerId);
      moves.flush();
      send(seat.down(...at(e), e.button));
    };
    const move = (e: PointerEvent) => moves.push(at(e));
    const up = (e: PointerEvent) => {
      moves.flush();
      send(seat.up(...at(e), e.button));
    };
    const lost = () => {
      moves.flush();
      send(seat.releaseButtons());
    };
    const wheelH = (e: WheelEvent) => {
      e.preventDefault();
      moves.flush();
      send(wheel(...at(e), e.deltaX, e.deltaY, e.deltaMode));
    };
    const menu = (e: Event) => e.preventDefault();
    const keyDown = (e: KeyboardEvent) => {
      if (isPasteKey(e)) return;
      e.preventDefault();
      send(seat.key(e, true));
    };
    const keyUp = (e: KeyboardEvent) => {
      e.preventDefault();
      send(seat.key(e, false));
    };
    const paste = (e: ClipboardEvent) => {
      const t = e.clipboardData?.getData("text/plain");
      e.preventDefault();
      if (!t) return;
      send([{ t: "clip", s: t }]);
      setTimeout(() => send(PASTE_KEYS), 60);
    };
    const blur = () => send(seat.releaseAll());
    el.addEventListener("pointerdown", down);
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", lost);
    el.addEventListener("lostpointercapture", lost);
    el.addEventListener("wheel", wheelH, { passive: false });
    el.addEventListener("contextmenu", menu);
    el.addEventListener("keydown", keyDown);
    el.addEventListener("keyup", keyUp);
    el.addEventListener("paste", paste);
    el.addEventListener("blur", blur);
    return () => {
      el.removeEventListener("pointerdown", down);
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", lost);
      el.removeEventListener("lostpointercapture", lost);
      el.removeEventListener("wheel", wheelH);
      el.removeEventListener("contextmenu", menu);
      el.removeEventListener("keydown", keyDown);
      el.removeEventListener("keyup", keyUp);
      el.removeEventListener("paste", paste);
      el.removeEventListener("blur", blur);
      moves.flush();
      send(seat.releaseAll());
    };
  }, [node, active, onInput]);
}

function pt([x, y]: [number, number]) {
  return { x, y };
}

export function DesktopView({
  theme,
  frame,
  region,
  cursors,
  highlight,
  showOthers = true,
  small,
  onGesture,
  onInput,
}: {
  theme: PluginTheme;
  frame: Frame | null;
  region: Region | null;
  cursors: Cur[];
  highlight?: string | null;
  showOthers?: boolean;
  small?: boolean;
  onGesture?: (active: boolean) => void;
  onInput?: (msgs: ViewerMsg[]) => void;
}) {
  const [boxW, setBoxW] = useState(0);
  const onLayout = (e: LayoutChangeEvent) => setBoxW(e.nativeEvent.layout.width);
  const rw = region?.w || 0;
  const rh = region?.h || 0;
  const scale = rw && boxW ? boxW / rw : 0;
  const toScreen = (lx: number, ly: number): [number, number] => [Math.round((region?.x || 0) + lx / scale), Math.round((region?.y || 0) + ly / scale)];
  const aspect = rw && rh ? rw / rh : frame ? frame.w / frame.h : 16 / 10;
  const g = useRef<{ sx: number; sy: number; px: number; py: number; lx: number; ly: number; drag: boolean; long: boolean; moved: number; timer: ReturnType<typeof setTimeout> | null } | null>(null);
  const web = Platform.OS === "web";
  const interactive = !web && !!onInput && !!frame && scale > 0;
  const surface = useRef<View>(null);
  useWebInput(surface, toScreen, onInput, web && !!frame && scale > 0);
  const drop = useRef<() => void>(() => {});
  drop.current = () => {
    const s = g.current;
    g.current = null;
    if (!s) return;
    if (s.timer) clearTimeout(s.timer);
    if (s.drag) onInput?.([{ t: "up", ...pt(toScreen(s.lx, s.ly)), b: 0 }]);
  };
  useEffect(() => () => drop.current(), []);
  const at = (e: GestureResponderEvent): [number, number] => {
    const s = g.current!;
    const lx = Math.min(Math.max(s.sx + e.nativeEvent.pageX - s.px, 0), boxW - 1);
    const ly = Math.min(Math.max(s.sy + e.nativeEvent.pageY - s.py, 0), rh * scale - 1);
    return [lx, ly];
  };
  const finish = (e: GestureResponderEvent | null) => {
    const s = g.current;
    g.current = null;
    if (!s) return;
    if (s.timer) clearTimeout(s.timer);
    if (s.drag) {
      onInput?.([{ t: "up", ...pt(toScreen(s.lx, s.ly)), b: 0 }]);
    } else if (e && !s.long) {
      onInput?.(click(...toScreen(s.sx, s.sy)));
    }
    onGesture?.(false);
  };
  const responder = interactive
    ? {
        onStartShouldSetResponder: () => true,
        onMoveShouldSetResponder: () => true,
        onResponderTerminationRequest: () => false,
        onResponderGrant: (e: GestureResponderEvent) => {
          const { locationX, locationY, pageX, pageY } = e.nativeEvent;
          const s = { sx: locationX, sy: locationY, px: pageX, py: pageY, lx: locationX, ly: locationY, drag: false, long: false, moved: 0, timer: null as ReturnType<typeof setTimeout> | null };
          s.timer = setTimeout(() => {
            if (g.current !== s || s.drag) return;
            s.long = true;
            onInput?.(click(...toScreen(s.sx, s.sy), 2));
          }, 500);
          g.current = s;
          onGesture?.(true);
        },
        onResponderMove: (e: GestureResponderEvent) => {
          const s = g.current;
          if (!s || s.long) return;
          const [lx, ly] = at(e);
          s.lx = lx;
          s.ly = ly;
          if (!s.drag) {
            if (Math.hypot(lx - s.sx, ly - s.sy) < 8) return;
            s.drag = true;
            if (s.timer) clearTimeout(s.timer);
            onInput?.([{ t: "down", ...pt(toScreen(s.sx, s.sy)), b: 0 }]);
          }
          const now = Date.now();
          if (now - s.moved < 40) return;
          s.moved = now;
          onInput?.([{ t: "move", ...pt(toScreen(lx, ly)) }]);
        },
        onResponderRelease: (e: GestureResponderEvent) => {
          if (g.current?.drag) {
            const [lx, ly] = at(e);
            g.current.lx = lx;
            g.current.ly = ly;
          }
          finish(e);
        },
        onResponderTerminate: () => finish(null),
      }
    : {};
  return (
    <View style={{ width: "100%", aspectRatio: aspect, backgroundColor: theme.colors.surface2, overflow: "hidden" }} onLayout={onLayout}>
      {frame && (
        <View ref={surface} style={{ flex: 1 }} {...responder}>
          <Image source={{ uri: frame.uri }} style={{ width: "100%", height: "100%" }} resizeMode="stretch" fadeDuration={0} />
        </View>
      )}
      {frame &&
        region &&
        scale > 0 &&
        cursors.map((c) => {
          if (!c.x && !c.y) return null;
          if (!showOthers && c.id !== highlight) return null;
          return <CursorMark key={c.id} theme={theme} cursor={c} region={region} scale={scale} width={boxW} height={rh * scale} dim={!!highlight && c.id !== highlight} small={small} />;
        })}
    </View>
  );
}
