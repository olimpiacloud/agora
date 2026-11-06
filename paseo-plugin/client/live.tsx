import { type PluginWorkspacePanelProps, useRpc, useTabState, useWorkspace } from "@getpaseo/plugin/client";
import { useEffect, useMemo, useRef, useState } from "react";
import { Image, type LayoutChangeEvent, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { z } from "zod";
import { control, ensureWorkspace, listCursors, listWorkspaces, liveFrame, sendInput } from "../shared/agora";

type Frame = { w: number; h: number; sw: number; sh: number; uri: string };
type Cur = { id: string; name: string; status: string; detail: string; color: string; x: number; y: number; goal: string; parent: string };

const tabSchema = z.object({ workspace: z.string() });

export function LivePanel({ theme, layout, workspaceId, tab }: PluginWorkspacePanelProps) {
  const wsName = useWorkspace(workspaceId, (w) => w.title || w.name) || "workspace";
  const [state, setState] = useTabState(tab, tabSchema, { workspace: "" });
  const list = useRpc(listWorkspaces);
  const ensure = useRpc(ensureWorkspace);
  const poll = useRpc(liveFrame);
  const input = useRpc(sendInput);
  const ctl = useRpc(control);
  const cursorsRpc = useRpc(listCursors);
  const [wss, setWss] = useState<{ id: string; name: string; state: string; cursors: number; linked: boolean }[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [frame, setFrame] = useState<Frame | null>(null);
  const [status, setStatus] = useState<string | null>("connecting");
  const [cursors, setCursors] = useState<Cur[]>([]);
  const [locked, setLocked] = useState<string | null>(null);
  const [box, setBox] = useState({ w: 0, h: 0 });
  const [text, setText] = useState("");
  const [paused, setPaused] = useState(false);
  const [pos, setPos] = useState<Record<string, { x: number; y: number }>>({});
  const ws = state.workspace;

  const styles = useMemo(
    () => ({
      root: { flex: 1, backgroundColor: theme.colors.surface0 },
      bar: { flexDirection: "row" as const, flexWrap: "wrap" as const, gap: 6, padding: layout.compact ? 8 : 10, alignItems: "center" as const, borderBottomWidth: 1, borderColor: theme.colors.border },
      chip: { paddingHorizontal: 10, paddingVertical: 5, borderRadius: 8, borderWidth: 1, borderColor: theme.colors.border, backgroundColor: theme.colors.surface1 },
      chipOn: { paddingHorizontal: 10, paddingVertical: 5, borderRadius: 8, borderWidth: 1, borderColor: theme.colors.accent, backgroundColor: theme.colors.accent },
      chipText: { color: theme.colors.foreground, fontSize: 12 },
      chipTextOn: { color: theme.colors.accentForeground, fontSize: 12 },
      muted: { color: theme.colors.foregroundMuted, fontSize: 12 },
      text: { color: theme.colors.foreground, fontSize: 13 },
      warn: { color: theme.colors.statusWarning, fontSize: 13 },
      danger: { color: theme.colors.statusDanger, fontSize: 12 },
      banner: { padding: 10, gap: 8, flexDirection: "row" as const, alignItems: "center" as const, flexWrap: "wrap" as const, backgroundColor: theme.colors.surface2, borderBottomWidth: 1, borderColor: theme.colors.statusWarning },
      screen: { width: "100%" as const, backgroundColor: theme.colors.surface1 },
      input: { flex: 1, minWidth: 140, color: theme.colors.foreground, backgroundColor: theme.colors.surface2, borderColor: theme.colors.border, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 },
      label: { position: "absolute" as const, paddingHorizontal: 5, paddingVertical: 1, borderRadius: 4 },
      dot: { position: "absolute" as const, width: 10, height: 10, borderRadius: 5, borderWidth: 2, borderColor: theme.colors.surface0 },
      row: { flexDirection: "row" as const, alignItems: "center" as const, gap: 8, paddingVertical: 4 },
      swatch: { width: 10, height: 10, borderRadius: 3 },
    }),
    [theme, layout.compact],
  );

  const refreshList = async () => {
    const r = await list({ paseoWorkspaceId: workspaceId });
    setErr(r.error);
    setWss(r.items);
    if (!ws && r.linked) setState({ workspace: r.linked });
  };

  useEffect(() => {
    void refreshList();
  }, [workspaceId]);

  const refreshCursors = async () => {
    if (!ws) return;
    try {
      const r = await cursorsRpc({ workspace: ws });
      setCursors(r.items);
      setLocked(r.locked);
      setPos((p) => {
        const n = { ...p };
        for (const c of r.items) if (!n[c.id] && (c.x || c.y)) n[c.id] = { x: c.x, y: c.y };
        return n;
      });
    } catch {}
  };

  useEffect(() => {
    if (!ws || paused) return;
    let stop = false;
    let after = 0;
    let evAfter = 0;
    setFrame(null);
    setStatus("connecting");
    void refreshCursors();
    const loop = async () => {
      while (!stop) {
        try {
          const r = await poll({ workspace: ws, maxWidth: layout.compact ? 900 : 1400, fps: 6, after, eventsAfter: evAfter });
          if (stop) return;
          after = r.seq;
          evAfter = r.eventSeq;
          setStatus(r.status);
          if (r.b64) setFrame({ w: r.w, h: r.h, sw: r.sw, sh: r.sh, uri: `data:image/jpeg;base64,${r.b64}` });
          let refresh = false;
          for (const e of r.events) {
            const d = (e.data || {}) as Record<string, unknown>;
            if (e.type === "cursor.pointer" && e.cursor && typeof d.x === "number" && (d.x as number) >= 0) {
              const id = e.cursor;
              setPos((p) => ({ ...p, [id]: { x: d.x as number, y: d.y as number } }));
            } else if (e.type.startsWith("cursor.") || e.type.startsWith("control.") || e.type.startsWith("human.")) {
              refresh = true;
            }
          }
          if (refresh) void refreshCursors();
        } catch (e) {
          setStatus(e instanceof Error ? e.message : String(e));
          await new Promise<void>((r) => setTimeout(() => r(), 1500));
        }
      }
    };
    void loop();
    return () => {
      stop = true;
    };
  }, [ws, paused, layout.compact]);

  const onLayout = (e: LayoutChangeEvent) => setBox({ w: e.nativeEvent.layout.width, h: e.nativeEvent.layout.height });
  const scale = frame && box.w ? box.w / frame.sw : 1;
  const act = async (a: Parameters<typeof input>[0]) => {
    const r = await input(a);
    if (!r.ok) setStatus(r.error);
  };
  const needsHuman = cursors.find((c) => c.status === "needs_human");
  const iHold = !!locked;

  const Chip = ({ label, on, onPress }: { label: string; on?: boolean; onPress: () => void }) => (
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={on ? styles.chipOn : styles.chip}>
      <Text style={on ? styles.chipTextOn : styles.chipText}>{label}</Text>
    </Pressable>
  );

  return (
    <ScrollView style={styles.root} contentContainerStyle={{ paddingBottom: 24 }}>
      <View style={styles.bar}>
        {wss.map((w) => (
          <Chip key={w.id} label={`${w.name}${w.linked ? " ★" : ""} · ${w.state}`} on={w.id === ws} onPress={() => setState({ workspace: w.id })} />
        ))}
        {!wss.some((w) => w.linked) && (
          <Chip
            label={`+ desktop for ${wsName}`}
            onPress={async () => {
              const r = await ensure({ paseoWorkspaceId: workspaceId, name: wsName });
              setState({ workspace: r.id });
              void refreshList();
            }}
          />
        )}
        <Chip label="↻" onPress={() => void refreshList()} />
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
              <Chip label="Take control" onPress={async () => { await ctl({ workspace: ws, op: "control-take" }); void refreshCursors() }} />
            </View>
          )}
          <View style={styles.bar}>
            <Chip label={paused ? "▶ live" : "⏸ pause"} onPress={() => setPaused(!paused)} />
            {iHold ? (
              <Chip label="Return control to agents" on onPress={async () => { await ctl({ workspace: ws, op: "control-return" }); void refreshCursors() }} />
            ) : (
              <Chip label="Take control" onPress={async () => { await ctl({ workspace: ws, op: "control-take" }); void refreshCursors() }} />
            )}
            <Text style={styles.muted}>{status ? status : frame ? `${frame.sw}×${frame.sh} · tap to click` : ""}</Text>
          </View>
          <View style={[styles.screen, { aspectRatio: frame ? frame.w / frame.h : 16 / 10 }]} onLayout={onLayout}>
            {frame && (
              <Pressable
                style={{ flex: 1 }}
                onPress={(e) => {
                  const x = Math.round(e.nativeEvent.locationX / scale);
                  const y = Math.round(e.nativeEvent.locationY / scale);
                  void act({ workspace: ws, action: "click", x, y });
                }}
                onLongPress={(e) => {
                  const x = Math.round(e.nativeEvent.locationX / scale);
                  const y = Math.round(e.nativeEvent.locationY / scale);
                  void act({ workspace: ws, action: "right_click", x, y });
                }}
              >
                <Image source={{ uri: frame.uri }} style={{ width: "100%", height: "100%" }} resizeMode="contain" fadeDuration={0} />
              </Pressable>
            )}
            {frame &&
              cursors.map((c) => {
                const p = pos[c.id];
                if (!p) return null;
                return (
                  <View key={c.id} pointerEvents="none" style={{ position: "absolute", left: p.x * scale, top: p.y * scale }}>
                    <View style={[styles.dot, { backgroundColor: c.color, left: -5, top: -5 }]} />
                    <View style={[styles.label, { backgroundColor: c.color, left: 8, top: 6 }]}>
                      <Text style={{ color: theme.colors.surface0, fontSize: 11 }}>{c.name}</Text>
                    </View>
                  </View>
                );
              })}
          </View>
          <View style={styles.bar}>
            <TextInput
              value={text}
              onChangeText={setText}
              placeholder="type into the focused window"
              placeholderTextColor={theme.colors.foregroundMuted}
              style={styles.input}
              onSubmitEditing={() => {
                if (text) void act({ workspace: ws, action: "type", text });
                setText("");
              }}
            />
            <Chip label="Send" onPress={() => { if (text) void act({ workspace: ws, action: "type", text }); setText("") }} />
            {["Enter", "Tab", "Escape", "BackSpace", "ctrl+l", "ctrl+a"].map((k) => (
              <Chip key={k} label={k} onPress={() => void act({ workspace: ws, action: "key", keys: k === "BackSpace" ? "backspace" : k })} />
            ))}
            <Chip label="▲" onPress={() => frame && void act({ workspace: ws, action: "scroll", x: frame.sw / 2, y: frame.sh / 2, dy: -5 })} />
            <Chip label="▼" onPress={() => frame && void act({ workspace: ws, action: "scroll", x: frame.sw / 2, y: frame.sh / 2, dy: 5 })} />
          </View>
          <View style={{ paddingHorizontal: layout.compact ? 10 : 14, paddingTop: 6 }}>
            <Text style={styles.muted}>Agents ({cursors.length})</Text>
            {cursors.map((c) => (
              <View key={c.id} style={styles.row}>
                <View style={[styles.swatch, { backgroundColor: c.color }]} />
                <Text style={styles.text}>{c.name}</Text>
                <Text style={c.status === "needs_human" || c.status === "blocked" ? styles.warn : styles.muted}>{c.status}</Text>
                {!!c.detail && <Text style={styles.muted} numberOfLines={1}>{c.detail}</Text>}
              </View>
            ))}
          </View>
        </>
      )}
    </ScrollView>
  );
}
