import type { PluginTheme } from "@getpaseo/plugin";
import { type PluginWorkspacePanelProps, useRpc, useTabState, useWorkspace } from "@getpaseo/plugin/client";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent, Platform, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { z } from "zod";
import { control, ensureWorkspace, listWorkspaces, liveInput, type ViewerMsg } from "../shared/agora";
import { createSender, tapKeys, wheelSteps } from "./input";
import { type Cur, DesktopView, statusColor, useCursors, useLive } from "./stream";

const tabSchema = z.object({ workspace: z.string(), view: z.enum(["desktop", "wall"]).optional() });

function makeStyles(theme: PluginTheme, compact: boolean) {
  return {
    root: { flex: 1, backgroundColor: theme.colors.surface0 },
    bar: { flexDirection: "row" as const, flexWrap: "wrap" as const, gap: 6, padding: compact ? 8 : 10, alignItems: "center" as const, borderBottomWidth: 1, borderColor: theme.colors.border },
    chip: { paddingHorizontal: 10, paddingVertical: 5, borderRadius: 8, borderWidth: 1, borderColor: theme.colors.border, backgroundColor: theme.colors.surface1 },
    chipOn: { paddingHorizontal: 10, paddingVertical: 5, borderRadius: 8, borderWidth: 1, borderColor: theme.colors.accent, backgroundColor: theme.colors.accent },
    chipText: { color: theme.colors.foreground, fontSize: 12 },
    chipTextOn: { color: theme.colors.accentForeground, fontSize: 12 },
    muted: { color: theme.colors.foregroundMuted, fontSize: 12 },
    text: { color: theme.colors.foreground, fontSize: 13 },
    warn: { color: theme.colors.statusWarning, fontSize: 13 },
    danger: { color: theme.colors.statusDanger, fontSize: 12 },
    banner: { padding: 10, gap: 8, flexDirection: "row" as const, alignItems: "center" as const, flexWrap: "wrap" as const, backgroundColor: theme.colors.surface2, borderBottomWidth: 1, borderColor: theme.colors.statusWarning },
    input: { flex: 1, minWidth: 140, color: theme.colors.foreground, backgroundColor: theme.colors.surface2, borderColor: theme.colors.border, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 },
    row: { flexDirection: "row" as const, alignItems: "center" as const, gap: 8, paddingVertical: 4 },
    swatch: { width: 10, height: 10, borderRadius: 3 },
  };
}

type Styles = ReturnType<typeof makeStyles>;

const Chip = memo(function Chip({ styles, label, on, onPress }: { styles: Styles; label: string; on?: boolean; onPress: () => void }) {
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={on ? styles.chipOn : styles.chip}>
      <Text style={on ? styles.chipTextOn : styles.chipText}>{label}</Text>
    </Pressable>
  );
});

type Viewport = { y: number; h: number; top: number; subs: Set<() => void> };

function notify(vp: Viewport) {
  for (const f of vp.subs) f();
}

function useVisible(vp: Viewport): [boolean, (e: LayoutChangeEvent) => void] {
  const box = useRef<{ y: number; h: number } | null>(null);
  const [visible, setVisible] = useState(false);
  const check = useRef(() => {});
  check.current = () => {
    const b = box.current;
    if (!b) return;
    const top = vp.top + b.y;
    setVisible(!vp.h || (top + b.h > vp.y - 200 && top < vp.y + vp.h + 200));
  };
  useEffect(() => {
    const f = () => check.current();
    vp.subs.add(f);
    return () => {
      vp.subs.delete(f);
    };
  }, [vp]);
  const onLayout = useCallback((e: LayoutChangeEvent) => {
    box.current = { y: e.nativeEvent.layout.y, h: e.nativeEvent.layout.height };
    check.current();
  }, []);
  return [visible, onLayout];
}

const AgentTile = memo(function AgentTile({ theme, workspace, self, cursors, width, compact, vp, onOpen }: { theme: PluginTheme; workspace: string; self: Cur; cursors: Cur[]; width: number; compact: boolean; vp: Viewport; onOpen: () => void }) {
  const [visible, onLayout] = useVisible(vp);
  const lv = useLive({ workspace, cursor: self.id, tier: "tile", enabled: visible });
  return (
    <View onLayout={onLayout} style={{ width, borderWidth: 1, borderColor: self.status === "needs_human" ? theme.colors.statusWarning : theme.colors.border, borderRadius: 10, overflow: "hidden", backgroundColor: theme.colors.surface1 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 10, paddingVertical: 6 }}>
        <View style={{ width: 10, height: 10, borderRadius: 3, backgroundColor: self.color }} />
        <Text numberOfLines={1} style={{ color: theme.colors.foreground, fontSize: 13, fontWeight: "600", flexShrink: 1 }}>
          {self.name}
        </Text>
        <Text style={{ color: statusColor(theme, self.status), fontSize: 12 }}>{self.status.replace("_", " ")}</Text>
      </View>
      <Pressable accessibilityRole="button" accessibilityLabel={`Show ${self.name} on the full desktop`} onPress={onOpen}>
        {lv.frame ? (
          <DesktopView theme={theme} frame={lv.frame} region={lv.region} cursors={cursors} highlight={self.id} small />
        ) : (
          <View style={{ aspectRatio: 16 / 10, alignItems: "center", justifyContent: "center", backgroundColor: theme.colors.surface2 }}>
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>{visible ? lv.status || "waiting for frames…" : "paused while off screen"}</Text>
          </View>
        )}
      </Pressable>
      <Text numberOfLines={compact ? 2 : 1} style={{ color: theme.colors.foregroundMuted, fontSize: 12, paddingHorizontal: 10, paddingVertical: 6 }}>
        {self.detail || self.goal || " "}
      </Text>
    </View>
  );
});

type PaneReport = { status: string | null; sw: number; sh: number; viewer: string | null };

function DesktopPane({ theme, workspace, maxWidth, paused, cursors, onInput, onGesture, onReport }: { theme: PluginTheme; workspace: string; maxWidth: number; paused: boolean; cursors: Cur[]; onInput: (msgs: ViewerMsg[]) => void; onGesture: (active: boolean) => void; onReport: (r: PaneReport) => void }) {
  const lv = useLive({ workspace, tier: "full", maxWidth, enabled: !paused });
  const sw = lv.region?.sw || 0;
  const sh = lv.region?.sh || 0;
  useEffect(() => {
    onReport({ status: lv.status, sw, sh, viewer: lv.viewer });
  }, [lv.status, sw, sh, lv.viewer, onReport]);
  return <DesktopView theme={theme} frame={lv.frame} region={lv.region} cursors={cursors} onGesture={onGesture} onInput={onInput} />;
}

const KeyBar = memo(function KeyBar({ theme, styles, sw, sh, onInput }: { theme: PluginTheme; styles: Styles; sw: number; sh: number; onInput: (msgs: ViewerMsg[]) => void }) {
  const [text, setText] = useState("");
  const submit = () => {
    if (text) onInput([{ t: "text", s: text }]);
    setText("");
  };
  return (
    <View style={styles.bar}>
      <TextInput value={text} onChangeText={setText} placeholder="type into the focused window" placeholderTextColor={theme.colors.foregroundMuted} style={styles.input} onSubmitEditing={submit} />
      <Chip styles={styles} label="Send" onPress={submit} />
      {["Enter", "Tab", "Escape", "BackSpace", "ctrl+l", "ctrl+a"].map((k) => (
        <Chip key={k} styles={styles} label={k} onPress={() => onInput(tapKeys(k === "BackSpace" ? "backspace" : k))} />
      ))}
      <Chip styles={styles} label="▲" onPress={() => sw && onInput(wheelSteps(sw / 2, sh / 2, -5))} />
      <Chip styles={styles} label="▼" onPress={() => sw && onInput(wheelSteps(sw / 2, sh / 2, 5))} />
    </View>
  );
});

const AgentList = memo(function AgentList({ theme, styles, roster, compact }: { theme: PluginTheme; styles: Styles; roster: Cur[]; compact: boolean }) {
  return (
    <View style={{ paddingHorizontal: compact ? 10 : 14, paddingTop: 6 }}>
      <Text style={styles.muted}>Agents ({roster.length})</Text>
      {roster.map((c) => (
        <View key={c.id} style={styles.row}>
          <View style={[styles.swatch, { backgroundColor: c.color }]} />
          <Text style={styles.text}>{c.name}</Text>
          <Text style={{ color: statusColor(theme, c.status), fontSize: 12 }}>{c.status.replace("_", " ")}</Text>
          {!!c.detail && (
            <Text style={[styles.muted, { flexShrink: 1 }]} numberOfLines={1}>
              {c.detail}
            </Text>
          )}
        </View>
      ))}
    </View>
  );
});

type Actions = { desktop: () => void; wall: () => void; pause: () => void; resume: () => void; take: () => void; release: () => void };

const Toolbar = memo(function Toolbar({ styles, view, paused, suspended, iHold, count, line, act }: { styles: Styles; view: "desktop" | "wall"; paused: boolean; suspended: boolean; iHold: boolean; count: number; line: string; act: Actions }) {
  return (
    <View style={styles.bar}>
      <Chip styles={styles} label="Desktop" on={view === "desktop"} onPress={act.desktop} />
      <Chip styles={styles} label={`Agent wall (${count})`} on={view === "wall"} onPress={act.wall} />
      {view === "desktop" && <Chip styles={styles} label={paused ? "▶ live" : "⏸ pause"} onPress={act.pause} />}
      {suspended && <Chip styles={styles} label="Resume" on onPress={act.resume} />}
      {iHold ? <Chip styles={styles} label="Return control to agents" on onPress={act.release} /> : <Chip styles={styles} label="Take control" onPress={act.take} />}
      <Text style={styles.muted}>{line}</Text>
    </View>
  );
});

function rosterKey(cursors: Cur[]): string {
  return cursors.map((c) => [c.id, c.name, c.status, c.detail, c.color, c.goal, c.agent].join("\u0000")).join("\u0001");
}

const NO_PANE: PaneReport = { status: "connecting", sw: 0, sh: 0, viewer: null };

export function LivePanel({ theme, layout, workspaceId, tab }: PluginWorkspacePanelProps) {
  const wsName = useWorkspace(workspaceId, (w) => w.title || w.name) || "workspace";
  const [state, setState] = useTabState(tab, tabSchema, { workspace: "", view: "desktop" });
  const list = useRpc(listWorkspaces);
  const ensure = useRpc(ensureWorkspace);
  const ctl = useRpc(control);
  const post = useRpc(liveInput);
  const [wss, setWss] = useState<{ id: string; name: string; state: string; cursors: number; linked: boolean }[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [actErr, setActErr] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  const [width, setWidth] = useState(0);
  const [gesture, setGesture] = useState(false);
  const [pane, setPane] = useState<PaneReport>(NO_PANE);
  const vp = useRef<Viewport>({ y: 0, h: 0, top: 0, subs: new Set() }).current;
  const ws = state.workspace;
  const view = state.view || "desktop";
  const maxWidth = layout.compact ? 960 : 1440;
  const meta = useCursors(ws || null, !!ws);
  const { cursors, locked } = meta;
  const key = rosterKey(cursors);
  const roster = useMemo(() => cursors, [key]);
  const styles = useMemo(() => makeStyles(theme, layout.compact), [theme, layout.compact]);

  const sender = useMemo(
    () =>
      createSender(async (msgs) => {
        try {
          const r = await post({ workspace: ws, tier: "full", maxWidth, msgs });
          setActErr(r.ok ? null : r.error);
        } catch (e) {
          setActErr(e instanceof Error ? e.message : String(e));
        }
      }),
    [post, ws, maxWidth],
  );

  useEffect(() => {
    setPane(NO_PANE);
    setActErr(null);
  }, [ws, maxWidth]);

  const refreshList = async () => {
    const r = await list({ paseoWorkspaceId: workspaceId });
    setErr(r.error);
    setWss(r.items);
    if (!ws && r.linked) setState({ workspace: r.linked, view });
  };

  useEffect(() => {
    void refreshList();
  }, [workspaceId]);

  const run = useCallback(
    async (op: "control-take" | "control-return" | "resume") => {
      const r = await ctl({ workspace: ws, op });
      setActErr(r.ok ? null : r.error);
    },
    [ws, ctl],
  );

  const act = useMemo<Actions>(
    () => ({
      desktop: () => setState({ workspace: ws, view: "desktop" }),
      wall: () => setState({ workspace: ws, view: "wall" }),
      pause: () => setPaused((p) => !p),
      resume: () => void run("resume"),
      take: () => {
        setPaused(false);
        if (view !== "desktop") setState({ workspace: ws, view: "desktop" });
        void run("control-take");
      },
      release: () => void run("control-return"),
    }),
    [ws, view, run, setState],
  );

  const onScroll = useCallback(
    (e: NativeSyntheticEvent<NativeScrollEvent>) => {
      vp.y = e.nativeEvent.contentOffset.y;
      notify(vp);
    },
    [vp],
  );
  const onRootLayout = useCallback(
    (e: LayoutChangeEvent) => {
      setWidth(e.nativeEvent.layout.width);
      vp.h = e.nativeEvent.layout.height;
      notify(vp);
    },
    [vp],
  );
  const onWallLayout = useCallback(
    (e: LayoutChangeEvent) => {
      vp.top = e.nativeEvent.layout.y;
      notify(vp);
    },
    [vp],
  );

  const needsHuman = roster.find((c) => c.status === "needs_human");
  const iHold = locked === "human:api";
  const status = actErr || (view === "desktop" ? pane.status : meta.status);
  const line = status ? status : view === "desktop" && pane.sw ? (Platform.OS === "web" ? `${pane.sw}×${pane.sh} · click the desktop to use mouse, wheel and keyboard` : `${pane.sw}×${pane.sh} · tap to click, drag to drag, long press to right click`) : "";
  const pad = layout.compact ? 8 : 12;
  const cols = width >= 1100 ? 3 : width >= 620 ? 2 : 1;
  const tileW = width ? Math.floor((width - pad * 2 - (cols - 1) * pad) / cols) : 0;

  return (
    <ScrollView scrollEnabled={Platform.OS === "web" || !gesture} style={styles.root} contentContainerStyle={{ paddingBottom: 24 }} onLayout={onRootLayout} onScroll={onScroll} scrollEventThrottle={100}>
      <View style={styles.bar}>
        {wss.map((w) => (
          <Chip key={w.id} styles={styles} label={`${w.name}${w.linked ? " ★" : ""} · ${w.state}`} on={w.id === ws} onPress={() => setState({ workspace: w.id, view })} />
        ))}
        {!wss.some((w) => w.linked) && (
          <Chip
            styles={styles}
            label={`+ desktop for ${wsName}`}
            onPress={async () => {
              const r = await ensure({ paseoWorkspaceId: workspaceId, name: wsName });
              setState({ workspace: r.id, view });
              void refreshList();
            }}
          />
        )}
        <Chip styles={styles} label="↻" onPress={() => void refreshList()} />
      </View>
      {err && <Text style={[styles.danger, { padding: 10 }]}>Agora: {err}</Text>}
      {!ws && !err && <Text style={[styles.muted, { padding: 16 }]}>Pick an Agora workspace. Agents in this Paseo workspace get one linked automatically (★).</Text>}
      {ws && (
        <>
          {needsHuman && !iHold && (
            <View style={styles.banner}>
              <Text style={styles.warn}>
                {needsHuman.name} needs you: {needsHuman.detail || "human intervention"}
              </Text>
              <Chip styles={styles} label="Take control" onPress={act.take} />
            </View>
          )}
          <Toolbar styles={styles} view={view} paused={paused} suspended={view === "desktop" && pane.status === "suspended"} iHold={iHold} count={roster.length} line={line} act={act} />
          {view === "wall" ? (
            <View onLayout={onWallLayout} style={{ flexDirection: "row", flexWrap: "wrap", gap: pad, padding: pad }}>
              {roster.length === 0 && <Text style={styles.muted}>No active agents in this workspace.</Text>}
              {tileW > 0 && cursors.map((c) => <AgentTile key={c.id} theme={theme} workspace={ws} self={c} cursors={cursors} width={tileW} compact={layout.compact} vp={vp} onOpen={act.desktop} />)}
            </View>
          ) : (
            <>
              <DesktopPane key={`${ws}|${maxWidth}`} theme={theme} workspace={ws} maxWidth={maxWidth} paused={paused} cursors={cursors} onInput={sender.push} onGesture={setGesture} onReport={setPane} />
              <KeyBar theme={theme} styles={styles} sw={pane.sw} sh={pane.sh} onInput={sender.push} />
            </>
          )}
          <AgentList theme={theme} styles={styles} roster={roster} compact={layout.compact} />
        </>
      )}
    </ScrollView>
  );
}
