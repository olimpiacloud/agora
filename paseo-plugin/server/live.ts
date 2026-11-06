import { call, wsURL } from "./agora";

interface LiveEvent {
  type: string;
  cursor?: string;
  data?: Record<string, unknown>;
}

interface Hub {
  key: string;
  sock: WebSocket | null;
  seq: number;
  frame: { w: number; h: number; sw: number; sh: number; b64: string } | null;
  events: { seq: number; e: LiveEvent }[];
  eventSeq: number;
  status: string | null;
  lastPoll: number;
  waiters: Set<() => void>;
}

const hubs = new Map<string, Hub>();

function wake(h: Hub) {
  for (const w of h.waiters) w();
  h.waiters.clear();
}

async function open(h: Hub, workspace: string, maxWidth: number, fps: number) {
  const { token } = await call("POST", "/v1/viewer-tokens", { workspace, control: true, ttl_seconds: 86400 });
  const url = wsURL(`/v1/workspaces/${workspace}/stream/jpeg?fps=${fps}&max_width=${maxWidth}&quality=60&token=${encodeURIComponent(token)}`);
  const sock = new WebSocket(url);
  sock.binaryType = "arraybuffer";
  h.sock = sock;
  sock.onmessage = (ev) => {
    if (typeof ev.data === "string") {
      try {
        const m = JSON.parse(ev.data);
        if (m.type === "event" && m.event && m.event.type !== "cursor.action") {
          h.eventSeq++;
          h.events.push({ seq: h.eventSeq, e: { type: m.event.type, cursor: m.event.cursor, data: m.event.data || {} } });
          if (h.events.length > 200) h.events.splice(0, h.events.length - 200);
          wake(h);
        } else if (m.type === "bye") {
          h.status = `session ended: ${m.reason}`;
          wake(h);
        }
      } catch {}
      return;
    }
    const buf = Buffer.from(ev.data as ArrayBuffer);
    h.seq++;
    h.frame = { w: buf.readUInt16BE(0), h: buf.readUInt16BE(2), sw: buf.readUInt16BE(4), sh: buf.readUInt16BE(6), b64: buf.subarray(8).toString("base64") };
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
}

setInterval(() => {
  const now = Date.now();
  for (const [k, h] of hubs) {
    if (now - h.lastPoll > 15000) {
      h.sock?.close();
      hubs.delete(k);
    }
  }
}, 5000).unref();

export async function pollFrame(input: { workspace: string; maxWidth: number; fps: number; after: number; eventsAfter: number }) {
  const maxWidth = Math.min(1920, Math.max(320, Math.round(input.maxWidth)));
  const fps = Math.min(10, Math.max(1, Math.round(input.fps)));
  const key = `${input.workspace}|${maxWidth}|${fps}`;
  let h = hubs.get(key);
  if (!h) {
    h = { key, sock: null, seq: 0, frame: null, events: [], eventSeq: 0, status: "connecting", lastPoll: Date.now(), waiters: new Set() };
    hubs.set(key, h);
  }
  h.lastPoll = Date.now();
  if (!h.sock) {
    try {
      await open(h, input.workspace, maxWidth, fps);
    } catch (e) {
      h.status = e instanceof Error ? e.message : String(e);
    }
  }
  const fresh = () => h!.seq > input.after || h!.eventSeq > input.eventsAfter;
  if (!fresh()) {
    await new Promise<void>((resolve) => {
      const t = setTimeout(resolve, 8000);
      h!.waiters.add(() => {
        clearTimeout(t);
        resolve();
      });
    });
  }
  const events = h.events.filter((x) => x.seq > input.eventsAfter).map((x) => x.e);
  const f = h.frame;
  const sendFrame = f && h.seq > input.after;
  return {
    seq: h.seq,
    w: f?.w ?? 0,
    h: f?.h ?? 0,
    sw: f?.sw ?? 0,
    sh: f?.sh ?? 0,
    b64: sendFrame ? f!.b64 : null,
    events,
    eventSeq: h.eventSeq,
    status: h.status,
  };
}
