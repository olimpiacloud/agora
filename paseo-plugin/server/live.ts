import type { RpcInput } from "@getpaseo/plugin";
import { call, wsURL } from "./agora";
import type { live } from "../shared/agora";

type Cur = { id: string; name: string; status: string; detail: string; color: string; x: number; y: number; goal: string; parent: string; agent: string };
type Region = { x: number; y: number; w: number; h: number; sw: number; sh: number };

interface Meta {
  cursors: Cur[];
  locked: string | null;
  seq: number;
  lastUse: number;
  refreshAt: number;
  timer: ReturnType<typeof setTimeout> | null;
  hubs: Set<Hub>;
  waiters: Set<() => void>;
}

interface Hub {
  key: string;
  workspace: string;
  sock: WebSocket | null;
  seq: number;
  frame: { w: number; h: number; b64: string } | null;
  region: Region;
  gotRegion: boolean;
  status: string | null;
  lastPoll: number;
  openedAt: number;
  connected: boolean;
  asleep: boolean;
  waiters: Set<() => void>;
}

const TIERS = {
  card: { maxWidth: 720, fps: 4, control: false },
  tile: { maxWidth: 560, fps: 3, control: false },
  full: { maxWidth: 1400, fps: 8, control: true },
} as const;

const hubs = new Map<string, Hub>();
const metas = new Map<string, Meta>();

function wake(h: Hub) {
  for (const w of h.waiters) w();
  h.waiters.clear();
}

function meta(workspace: string): Meta {
  let m = metas.get(workspace);
  if (!m) {
    m = { cursors: [], locked: null, seq: 0, lastUse: Date.now(), refreshAt: 0, timer: null, hubs: new Set(), waiters: new Set() };
    metas.set(workspace, m);
    scheduleRefresh(workspace, 0);
  }
  return m;
}

const metaWake = new Map<string, ReturnType<typeof setTimeout>>();

function bumpMeta(workspace: string) {
  const m = metas.get(workspace);
  if (!m) return;
  m.seq++;
  if (metaWake.has(workspace)) return;
  metaWake.set(
    workspace,
    setTimeout(() => {
      metaWake.delete(workspace);
      const cur = metas.get(workspace);
      if (!cur) return;
      for (const h of cur.hubs) wake(h);
      for (const w of cur.waiters) w();
      cur.waiters.clear();
    }, 100),
  );
}

function scheduleRefresh(workspace: string, delay: number) {
  const m = metas.get(workspace);
  if (!m || m.timer) return;
  m.timer = setTimeout(async () => {
    m.timer = null;
    m.refreshAt = Date.now();
    try {
      const cs: any[] = await call("GET", `/v1/cursors?workspace=${workspace}&active=1`);
      const locks: any[] = await call("GET", `/v1/workspaces/${workspace}/locks`).catch(() => []);
      const ws = locks.find((l) => l.surface === "workspace" && !l.expires);
      const prev = new Map(m.cursors.map((c) => [c.id, c]));
      m.cursors = cs.map((c) => {
        const p = prev.get(c.id);
        return {
          id: c.id,
          name: c.name,
          status: c.status,
          detail: c.detail || "",
          color: c.color,
          x: p && (p.x || p.y) ? p.x : c.x || 0,
          y: p && (p.x || p.y) ? p.y : c.y || 0,
          goal: c.goal || "",
          parent: c.parent || "",
          agent: c.labels?.paseo_agent || "",
        };
      });
      m.locked = ws ? ws.holder : null;
      bumpMeta(workspace);
    } catch {}
  }, delay);
}

function onEvent(workspace: string, e: { type: string; cursor?: string; data?: Record<string, unknown> }) {
  const m = metas.get(workspace);
  if (!m) return;
  const d = e.data || {};
  if (e.type === "cursor.pointer" && e.cursor && typeof d.x === "number" && typeof d.y === "number" && d.x >= 0) {
    const c = m.cursors.find((c) => c.id === e.cursor);
    if (!c) return scheduleRefresh(workspace, 200);
    if (c.x === d.x && c.y === d.y) return;
    c.x = d.x;
    c.y = d.y;
    bumpMeta(workspace);
  } else if (e.type.startsWith("cursor.") || e.type.startsWith("control.") || e.type.startsWith("human.")) {
    scheduleRefresh(workspace, 250);
  }
}

let openChain: Promise<void> = Promise.resolve();

function open(h: Hub, cursor: string, tier: keyof typeof TIERS, maxWidth: number): Promise<void> {
  h.openedAt = Date.now() + 60000;
  const run = openChain.then(() => openNow(h, cursor, tier, maxWidth));
  openChain = run.catch(() => {});
  return run;
}

async function openNow(h: Hub, cursor: string, tier: keyof typeof TIERS, maxWidth: number) {
  const t = TIERS[tier];
  const alive = () => hubs.get(h.key) === h && !h.sock;
  if (!alive()) return;
  h.openedAt = Date.now();
  const { token } = await call("POST", "/v1/viewer-tokens", { workspace: h.workspace, control: t.control, ttl_seconds: 86400 });
  if (!alive()) return;
  const q = new URLSearchParams({ fps: String(t.fps), max_width: String(maxWidth), quality: tier === "full" ? "62" : "55", token });
  if (cursor) q.set("cursor", cursor);
  if (h.connected) q.set("wake", "0");
  const sock = new WebSocket(wsURL(`/v1/workspaces/${h.workspace}/stream/jpeg?${q}`));
  sock.binaryType = "arraybuffer";
  h.sock = sock;
  sock.onmessage = (ev) => {
    if (typeof ev.data === "string") {
      try {
        const m = JSON.parse(ev.data);
        if (m.type === "event" && m.event && m.event.type !== "cursor.action") {
          onEvent(h.workspace, m.event);
        } else if (m.type === "region") {
          h.region = { x: m.x, y: m.y, w: m.w, h: m.h, sw: m.screen_w, sh: m.screen_h };
          h.gotRegion = true;
        } else if (m.type === "bye" && m.reason === "suspended") {
          h.asleep = true;
          h.status = "suspended";
          wake(h);
        } else if (m.type === "bye") {
          h.status = `session ended: ${m.reason}`;
          wake(h);
        }
      } catch {}
      return;
    }
    const buf = Buffer.from(ev.data as ArrayBuffer);
    const fw = buf.readUInt16BE(4);
    const fh = buf.readUInt16BE(6);
    if (!h.gotRegion) h.region = { x: 0, y: 0, w: fw, h: fh, sw: fw, sh: fh };
    h.connected = true;
    h.seq++;
    h.frame = { w: buf.readUInt16BE(0), h: buf.readUInt16BE(2), b64: buf.subarray(8).toString("base64") };
    h.status = null;
    wake(h);
  };
  sock.onerror = () => {
    h.status = "connection to Agora failed";
  };
  sock.onclose = () => {
    if (h.sock === sock) h.sock = null;
    wake(h);
  };
  await new Promise<void>((resolve) => {
    const t = setTimeout(resolve, 1500);
    const done = () => {
      clearTimeout(t);
      resolve();
    };
    sock.addEventListener("message", done, { once: true });
    sock.addEventListener("close", done, { once: true });
  });
}

setInterval(() => {
  const now = Date.now();
  for (const [k, h] of hubs) {
    if (h.waiters.size === 0 && now - h.lastPoll > 12000) {
      h.sock?.close();
      hubs.delete(k);
      metas.get(h.workspace)?.hubs.delete(h);
    }
  }
  for (const [ws, m] of metas) {
    if (m.hubs.size === 0 && m.waiters.size === 0 && now - m.lastUse > 12000) {
      if (m.timer) clearTimeout(m.timer);
      metas.delete(ws);
    } else if (now - m.refreshAt > 10000) {
      scheduleRefresh(ws, 0);
    }
  }
}, 4000).unref();

export function wakeHubs(workspace: string) {
  for (const h of hubs.values()) {
    if (h.workspace !== workspace) continue;
    h.asleep = false;
    h.connected = false;
    h.openedAt = 0;
    h.status = "connecting";
    wake(h);
  }
}

export function closeAll() {
  for (const h of hubs.values()) h.sock?.close();
  hubs.clear();
  for (const m of metas.values()) if (m.timer) clearTimeout(m.timer);
  metas.clear();
}

export async function pollLive(input: RpcInput<typeof live>) {
  if (input.metaOnly) return pollMeta(input);
  const tier = input.tier;
  const maxWidth = tier === "full" ? Math.min(1920, Math.max(480, Math.round((input.maxWidth || 1400) / 240) * 240)) : TIERS[tier].maxWidth;
  const cursor = input.cursor || "";
  const key = `${input.workspace}|${cursor}|${tier}|${maxWidth}`;
  const m = meta(input.workspace);
  m.lastUse = Date.now();
  let h = hubs.get(key);
  if (!h) {
    h = { key, workspace: input.workspace, sock: null, seq: 0, frame: null, region: { x: 0, y: 0, w: 0, h: 0, sw: 0, sh: 0 }, gotRegion: false, status: "connecting", lastPoll: Date.now(), openedAt: 0, connected: false, asleep: false, waiters: new Set() };
    hubs.set(key, h);
  }
  m.hubs.add(h);
  h.lastPoll = Date.now();
  if (!h.sock && !h.asleep && Date.now() - h.openedAt > 2000) {
    try {
      await open(h, cursor, tier, maxWidth);
    } catch (e) {
      h.status = e instanceof Error ? e.message : String(e);
    }
  }
  const hub = h;
  const fresh = () => hub.seq > input.after || m.seq > input.metaAfter;
  if (!fresh()) {
    await new Promise<void>((resolve) => {
      const done = () => {
        clearTimeout(t);
        hub.waiters.delete(done);
        resolve();
      };
      const t = setTimeout(done, 8000);
      hub.waiters.add(done);
    });
  }
  hub.lastPoll = Date.now();
  const f = hub.frame;
  return {
    seq: hub.seq,
    frame: f && hub.seq > input.after ? f : null,
    region: hub.region,
    status: hub.status,
    metaSeq: m.seq,
    cursors: m.seq > input.metaAfter ? m.cursors : null,
    locked: m.locked,
  };
}

async function pollMeta(input: RpcInput<typeof live>) {
  const m = meta(input.workspace);
  m.lastUse = Date.now();
  if (m.seq <= input.metaAfter) {
    await new Promise<void>((resolve) => {
      const done = () => {
        clearTimeout(t);
        m.waiters.delete(done);
        resolve();
      };
      const t = setTimeout(done, 8000);
      m.waiters.add(done);
    });
  }
  m.lastUse = Date.now();
  return {
    seq: 0,
    frame: null,
    region: { x: 0, y: 0, w: 0, h: 0, sw: 0, sh: 0 },
    status: null,
    metaSeq: m.seq,
    cursors: m.seq > input.metaAfter ? m.cursors : null,
    locked: m.locked,
  };
}
