import type { PluginTheme } from "@getpaseo/plugin";
import { type PluginWorkspacePanelProps, useRpc, useTabState, useWorkspace } from "@getpaseo/plugin/client";
import { useEffect, useMemo, useState } from "react";
import { Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { z } from "zod";
import { control, ensureWorkspace, listWorkspaces, sendInput } from "../shared/agora";
import { type Cur, DesktopView, statusColor, useLive } from "./stream";

const tabSchema = z.object({ workspace: z.string(), view: z.enum(["desktop", "wall"]).optional() });

function AgentTile({ theme, workspace, cursor, width, compact, onOpen }: { theme: PluginTheme; workspace: string; cursor: Cur; width: number; compact: boolean; onOpen: () => void }) {
  const lv = useLive({ workspace, cursor: cursor.id, tier: "tile", enabled: true });
  const cursors = lv.cursors.length ? lv.cursors : [cursor];
  const self = cursors.find((c) => c.id === cursor.id) || cursor;
  return (
    <View style={{ width, borderWidth: 1, borderColor: self.status === "needs_human" ? theme.colors.statusWarning : theme.colors.border, borderRadius: 10, overflow: "hidden", backgroundColor: theme.colors.surface1 }}>
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
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>{lv.status || "waiting for frames…"}</Text>
          </View>
        )}
      </Pressable>
      <Text numberOfLines={compact ? 2 : 1} style={{ color: theme.colors.foregroundMuted, fontSize: 12, paddingHorizontal: 10, paddingVertical: 6 }}>
        {self.detail || self.goal || " "}
      </Text>
    </View>
  );
}

export function LivePanel({ theme, layout, workspaceId, tab }: PluginWorkspacePanelProps) {
  const wsName = useWorkspace(workspaceId, (w) => w.title || w.name) || "workspace";
  const [state, setState] = useTabState(tab, tabSchema, { workspace: "", view: "desktop" });
  const list = useRpc(listWorkspaces);
  const ensure = useRpc(ensureWorkspace);
  const input = useRpc(sendInput);
  const ctl = useRpc(control);
  const [wss, setWss] = useState<{ id: string; name: string; state: string; cursors: number; linked: boolean }[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [actErr, setActErr] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [paused, setPaused] = useState(false);
  const [width, setWidth] = useState(0);
  const ws = state.workspace;
  const view = state.view || "desktop";
  const lv = useLive({ workspace: ws || null, tier: "full", maxWidth: layout.compact ? 960 : 1440, metaOnly: view === "wall" || paused, enabled: !!ws });
  const { frame, region, cursors, locked } = lv;
  const status = actErr || lv.status;

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
      input: { flex: 1, minWidth: 140, color: theme.colors.foreground, backgroundColor: theme.colors.surface2, borderColor: theme.colors.border, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 },
      row: { flexDirection: "row" as const, alignItems: "center" as const, gap: 8, paddingVertical: 4 },
      swatch: { width: 10, height: 10, borderRadius: 3 },
    }),
    [theme, layout.compact],
  );

  const refreshList = async () => {
    const r = await list({ paseoWorkspaceId: workspaceId });
    setErr(r.error);
    setWss(r.items);
    if (!ws && r.linked) setState({ workspace: r.linked, view });
  };

  useEffect(() => {
    void refreshList();
  }, [workspaceId]);

  const act = async (a: Parameters<typeof input>[0]) => {
    const r = await input(a);
    setActErr(r.ok ? null : r.error);
  };
  const op = async (o: "control-take" | "control-return" | "resume") => {
    const r = await ctl({ workspace: ws, op: o });
    setActErr(r.ok ? null : r.error);
  };
  const needsHuman = cursors.find((c) => c.status === "needs_human");
  const iHold = !!locked;
  const pad = layout.compact ? 8 : 12;
  const cols = width >= 1100 ? 3 : width >= 620 ? 2 : 1;
  const tileW = width ? Math.floor((width - pad * 2 - (cols - 1) * pad) / cols) : 0;

  const Chip = ({ label, on, onPress }: { label: string; on?: boolean; onPress: () => void }) => (
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={on ? styles.chipOn : styles.chip}>
      <Text style={on ? styles.chipTextOn : styles.chipText}>{label}</Text>
    </Pressable>
  );

  return (
    <ScrollView style={styles.root} contentContainerStyle={{ paddingBottom: 24 }} onLayout={(e) => setWidth(e.nativeEvent.layout.width)}>
      <View style={styles.bar}>
        {wss.map((w) => (
          <Chip key={w.id} label={`${w.name}${w.linked ? " ★" : ""} · ${w.state}`} on={w.id === ws} onPress={() => setState({ workspace: w.id, view })} />
        ))}
        {!wss.some((w) => w.linked) && (
          <Chip
            label={`+ desktop for ${wsName}`}
            onPress={async () => {
              const r = await ensure({ paseoWorkspaceId: workspaceId, name: wsName });
              setState({ workspace: r.id, view });
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
              <Chip label="Take control" onPress={() => void op("control-take")} />
            </View>
          )}
          <View style={styles.bar}>
            <Chip label="Desktop" on={view === "desktop"} onPress={() => setState({ workspace: ws, view: "desktop" })} />
            <Chip label={`Agent wall (${cursors.length})`} on={view === "wall"} onPress={() => setState({ workspace: ws, view: "wall" })} />
            {view === "desktop" && <Chip label={paused ? "▶ live" : "⏸ pause"} onPress={() => setPaused(!paused)} />}
            {lv.status === "suspended" && <Chip label="Resume" on onPress={() => void op("resume")} />}
            {iHold ? <Chip label="Return control to agents" on onPress={() => void op("control-return")} /> : <Chip label="Take control" onPress={() => void op("control-take")} />}
            <Text style={styles.muted}>{status ? status : view === "desktop" && region ? `${region.sw}×${region.sh} · tap to click, long press to right click` : ""}</Text>
          </View>
          {view === "wall" ? (
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: pad, padding: pad }}>
              {cursors.length === 0 && <Text style={styles.muted}>No active agents in this workspace.</Text>}
              {tileW > 0 &&
                cursors.map((c) => (
                  <AgentTile key={c.id} theme={theme} workspace={ws} cursor={c} width={tileW} compact={layout.compact} onOpen={() => setState({ workspace: ws, view: "desktop" })} />
                ))}
            </View>
          ) : (
            <>
              <DesktopView
                theme={theme}
                frame={frame}
                region={region}
                cursors={cursors}
                onTap={(x, y) => void act({ workspace: ws, action: "click", x, y })}
                onLongTap={(x, y) => void act({ workspace: ws, action: "right_click", x, y })}
              />
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
                <Chip label="▲" onPress={() => region && void act({ workspace: ws, action: "scroll", x: region.sw / 2, y: region.sh / 2, dy: -5 })} />
                <Chip label="▼" onPress={() => region && void act({ workspace: ws, action: "scroll", x: region.sw / 2, y: region.sh / 2, dy: 5 })} />
              </View>
            </>
          )}
          <View style={{ paddingHorizontal: layout.compact ? 10 : 14, paddingTop: 6 }}>
            <Text style={styles.muted}>Agents ({cursors.length})</Text>
            {cursors.map((c) => (
              <View key={c.id} style={styles.row}>
                <View style={[styles.swatch, { backgroundColor: c.color }]} />
                <Text style={styles.text}>{c.name}</Text>
                <Text style={{ color: statusColor(theme, c.status), fontSize: 12 }}>{c.status.replace("_", " ")}</Text>
                {!!c.detail && <Text style={[styles.muted, { flexShrink: 1 }]} numberOfLines={1}>{c.detail}</Text>}
              </View>
            ))}
          </View>
        </>
      )}
    </ScrollView>
  );
}
