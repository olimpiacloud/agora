import { afterAll, describe, expect, mock, test } from "bun:test";

const tokens: { workspace: string; control: boolean }[] = [];

mock.module("../server/agora", () => ({
  call: async (method: string, path: string, body?: { workspace: string; control: boolean }) => {
    if (method === "POST" && path === "/v1/viewer-tokens") {
      tokens.push(body!);
      return { token: "tok" };
    }
    if (path.startsWith("/v1/cursors")) return [{ id: "cur_1", name: "a", status: "running", color: "#fff", x: 1, y: 1 }];
    return [];
  },
  wsURL: (p: string) => `ws://agora${p}`,
}));

type Listener = (ev: { data?: unknown }) => void;

class FakeSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  binaryType = "";
  onmessage: Listener | null = null;
  onerror: Listener | null = null;
  onclose: Listener | null = null;
  listeners = new Map<string, Set<Listener>>();
  constructor(public url: string) {
    FakeSocket.all.push(this);
    setTimeout(() => {
      this.readyState = 1;
      this.emit("open", {});
      this.message(JSON.stringify({ type: "hello", viewer: `jpeg_${FakeSocket.all.length}` }));
    }, 5);
  }
  emit(type: string, ev: { data?: unknown }) {
    for (const f of [...(this.listeners.get(type) || [])]) f(ev);
  }
  message(data: unknown) {
    this.onmessage?.({ data });
    this.emit("message", { data });
  }
  frame() {
    const b = new Uint8Array(12);
    const v = new DataView(b.buffer);
    v.setUint16(0, 100);
    v.setUint16(2, 50);
    v.setUint16(4, 200);
    v.setUint16(6, 100);
    this.message(b.buffer);
  }
  addEventListener(type: string, f: Listener, opts?: { once?: boolean }) {
    let set = this.listeners.get(type);
    if (!set) this.listeners.set(type, (set = new Set()));
    const wrapped: Listener = (ev) => {
      if (opts?.once) set!.delete(wrapped);
      f(ev);
    };
    set.add(wrapped);
  }
  send(s: string) {
    this.sent.push(s);
  }
  close() {
    this.readyState = 3;
    this.onclose?.({});
    this.emit("close", {});
  }
}

const realWS = globalThis.WebSocket;
(globalThis as { WebSocket: unknown }).WebSocket = FakeSocket;
const { closeAll, pollLive, sendLiveInput } = await import("../server/live");

afterAll(() => {
  closeAll();
  (globalThis as { WebSocket: unknown }).WebSocket = realWS;
});

describe("live input over the viewer socket", () => {
  test("refuses tiers without control", async () => {
    const r = await sendLiveInput({ workspace: "ws_a", tier: "tile", msgs: [{ t: "text", s: "a" }] });
    expect(r.ok).toBe(false);
    expect(FakeSocket.all.length).toBe(0);
  });

  test("opens the full hub with a control token and writes the batch in order", async () => {
    const msgs = [
      { t: "text" as const, s: "a" },
      { t: "down" as const, x: 10, y: 20, b: 0 },
      { t: "up" as const, x: 30, y: 40, b: 0 },
      { t: "key" as const, key: "a", down: true },
    ];
    const r = await sendLiveInput({ workspace: "ws_a", tier: "full", maxWidth: 1440, msgs });
    expect(r).toEqual({ ok: true, error: null });
    expect(tokens.at(-1)).toMatchObject({ workspace: "ws_a", control: true });
    expect(FakeSocket.all.length).toBe(1);
    const sock = FakeSocket.all[0];
    expect(sock.url).toContain("/v1/workspaces/ws_a/stream/jpeg?");
    expect(sock.sent.map((s) => JSON.parse(s))).toEqual(msgs);
  });

  test("reuses the hub the frame poll uses and reports its viewer id", async () => {
    const sock = FakeSocket.all[0];
    setTimeout(() => sock.frame(), 5);
    const r = await pollLive({ workspace: "ws_a", tier: "full", maxWidth: 1440, after: 0 });
    expect(FakeSocket.all.length).toBe(1);
    expect(r.viewer).toBe("jpeg_1");
    expect(r.frame).not.toBeNull();
    expect(r.cursors).toBeNull();
    await sendLiveInput({ workspace: "ws_a", tier: "full", maxWidth: 1440, msgs: [{ t: "text", s: "b" }] });
    expect(JSON.parse(sock.sent.at(-1)!)).toEqual({ t: "text", s: "b" });
  });

  test("cursor events wake meta polls but not frame-only polls", async () => {
    const sock = FakeSocket.all[0];
    const meta = await pollLive({ workspace: "ws_a", tier: "full", metaOnly: true, after: 0, metaAfter: 0 });
    let frameDone = false;
    const frame = pollLive({ workspace: "ws_a", tier: "full", maxWidth: 1440, after: 1 }).then((r) => {
      frameDone = true;
      return r;
    });
    const next = pollLive({ workspace: "ws_a", tier: "full", metaOnly: true, after: 0, metaAfter: meta.metaSeq });
    sock.message(JSON.stringify({ type: "event", event: { type: "cursor.pointer", cursor: "cur_1", data: { x: 99, y: 98 } } }));
    const woke = await next;
    expect(woke.cursors?.[0]).toMatchObject({ id: "cur_1", x: 99, y: 98 });
    expect(frameDone).toBe(false);
    sock.frame();
    const f = await frame;
    expect(f.cursors).toBeNull();
    expect(f.seq).toBe(2);
  });
});
