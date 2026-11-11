import { describe, expect, test } from "bun:test";
import { viewerMsg, type ViewerMsg } from "../shared/agora";
import { click, createSeat, createSender, createWheel, isPasteKey, keyName, PASTE_KEYS, tapKeys, throttle, wheelSteps } from "../client/input";

const key = (code: string, k: string, mods: { ctrlKey?: boolean; metaKey?: boolean } = {}) => ({ code, key: k, ctrlKey: !!mods.ctrlKey, metaKey: !!mods.metaKey });

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe("keyName", () => {
  test("maps letters and digits by physical code", () => {
    expect(keyName(key("KeyA", "A"))).toEqual({ key: "a" });
    expect(keyName(key("KeyQ", "a"))).toEqual({ key: "q" });
    expect(keyName(key("Digit1", "!"))).toEqual({ key: "1" });
    expect(keyName(key("Numpad7", "7"))).toEqual({ key: "7" });
  });

  test("maps punctuation codes like the web viewer", () => {
    expect(keyName(key("Slash", "?"))).toEqual({ key: "/" });
    expect(keyName(key("Space", " "))).toEqual({ key: "space" });
    expect(keyName(key("IntlBackslash", "<"))).toEqual({ key: "<" });
  });

  test("falls back to text for unmapped printable keys and to the key name otherwise", () => {
    expect(keyName(key("", "é"))).toEqual({ text: "é" });
    expect(keyName(key("", "é", { ctrlKey: true }))).toEqual({ key: "é" });
    expect(keyName(key("Enter", "Enter"))).toEqual({ key: "Enter" });
    expect(keyName(key("ShiftLeft", "Shift"))).toEqual({ key: "Shift" });
    expect(keyName(key("ArrowLeft", "ArrowLeft"))).toEqual({ key: "ArrowLeft" });
  });

  test("detects paste shortcuts", () => {
    expect(isPasteKey(key("KeyV", "v", { ctrlKey: true }))).toBe(true);
    expect(isPasteKey(key("KeyV", "v", { metaKey: true }))).toBe(true);
    expect(isPasteKey(key("KeyV", "v"))).toBe(false);
  });
});

describe("seat", () => {
  test("sends up only for buttons that are down", () => {
    const s = createSeat();
    expect(s.up(1, 2, 0)).toEqual([]);
    expect(s.down(1, 2, 0)).toEqual([{ t: "down", x: 1, y: 2, b: 0 }]);
    expect(s.down(1, 2, 0)).toEqual([]);
    expect(s.up(3, 4, 0)).toEqual([{ t: "up", x: 3, y: 4, b: 0 }]);
    expect(s.held()).toBe(0);
  });

  test("releases pressed buttons and held keys at the last position", () => {
    const s = createSeat();
    s.down(10, 10, 0);
    s.down(10, 10, 2);
    s.move(50, 60);
    s.key(key("ControlLeft", "Control", { ctrlKey: true }), true);
    s.key(key("KeyC", "c", { ctrlKey: true }), true);
    expect(s.releaseAll()).toEqual([
      { t: "up", x: 50, y: 60, b: 0 },
      { t: "up", x: 50, y: 60, b: 2 },
      { t: "key", key: "c", down: false },
      { t: "key", key: "Control", down: false },
    ]);
    expect(s.releaseAll()).toEqual([]);
  });

  test("lost pointer capture releases buttons but keeps keys", () => {
    const s = createSeat();
    s.down(5, 5, 0);
    s.key(key("ShiftLeft", "Shift"), true);
    expect(s.releaseButtons()).toEqual([{ t: "up", x: 5, y: 5, b: 0 }]);
    expect(s.held()).toBe(1);
  });

  test("key up is sent only for keys sent down, text never holds", () => {
    const s = createSeat();
    expect(s.key(key("", "é"), true)).toEqual([{ t: "text", s: "é" }]);
    expect(s.key(key("", "é"), false)).toEqual([]);
    expect(s.key(key("KeyV", "v"), false)).toEqual([]);
    expect(s.key(key("KeyA", "a"), true)).toEqual([{ t: "key", key: "a", down: true }]);
    expect(s.key(key("KeyA", "a"), false)).toEqual([{ t: "key", key: "a", down: false }]);
  });
});

describe("message builders", () => {
  test("produce messages the server schema accepts", () => {
    const all: ViewerMsg[] = [...click(1, 2, 2), ...tapKeys("ctrl+l"), ...wheelSteps(3, 4, -5), ...PASTE_KEYS, { t: "text", s: "hi" }, { t: "clip", s: "x" }, { t: "take" }, { t: "release" }];
    for (const m of all) expect(viewerMsg.parse(m)).toEqual(m);
  });

  test("tapKeys presses in order and releases in reverse", () => {
    expect(tapKeys("ctrl+shift+t")).toEqual([
      { t: "key", key: "ctrl", down: true },
      { t: "key", key: "shift", down: true },
      { t: "key", key: "t", down: true },
      { t: "key", key: "t", down: false },
      { t: "key", key: "shift", down: false },
      { t: "key", key: "ctrl", down: false },
    ]);
  });

  test("wheel sends one message per notch and keeps the remainder", () => {
    const w = createWheel();
    expect(w(1, 1, 0, 30, 0)).toEqual([]);
    expect(w(1, 1, 0, 100, 0)).toEqual([
      { t: "wheel", x: 1, y: 1, dy: 1 },
      { t: "wheel", x: 1, y: 1, dy: 1 },
    ]);
    expect(w(1, 1, 0, -3, 1)).toEqual([{ t: "wheel", x: 1, y: 1, dy: -1 }]);
    expect(w(1, 1, 0, -10, 0)).toEqual([{ t: "wheel", x: 1, y: 1, dy: -1 }]);
    expect(w(1, 1, -20, 0, 0)).toEqual([{ t: "wheel", x: 1, y: 1, dx: -1 }]);
  });
});

describe("throttle", () => {
  test("sends the first value at once and always the last one", async () => {
    const out: number[] = [];
    const t = throttle(20, (v: number) => out.push(v));
    t.push(1);
    t.push(2);
    t.push(3);
    expect(out).toEqual([1]);
    await sleep(40);
    expect(out).toEqual([1, 3]);
  });

  test("flush sends the pending value now and cancel drops it", async () => {
    const out: number[] = [];
    const t = throttle(50, (v: number) => out.push(v));
    t.push(1);
    t.push(2);
    t.flush();
    expect(out).toEqual([1, 2]);
    t.push(3);
    t.cancel();
    await sleep(70);
    expect(out).toEqual([1, 2]);
  });
});

describe("sender", () => {
  test("keeps one batch in flight, preserves order and coalesces moves", async () => {
    const batches: ViewerMsg[][] = [];
    let release: () => void = () => {};
    const s = createSender((msgs) => {
      batches.push(msgs);
      return new Promise<void>((r) => (release = r));
    });
    s.push([{ t: "down", x: 0, y: 0, b: 0 }]);
    s.push([{ t: "move", x: 1, y: 1 }]);
    s.push([{ t: "move", x: 2, y: 2 }]);
    s.push([{ t: "up", x: 2, y: 2, b: 0 }]);
    expect(batches).toEqual([[{ t: "down", x: 0, y: 0, b: 0 }]]);
    expect(s.pending()).toBe(2);
    release();
    await sleep(0);
    expect(batches[1]).toEqual([
      { t: "move", x: 2, y: 2 },
      { t: "up", x: 2, y: 2, b: 0 },
    ]);
    release();
  });

  test("a hung request does not stall later input", async () => {
    const batches: ViewerMsg[][] = [];
    const s = createSender((msgs) => {
      batches.push(msgs);
      return batches.length === 1 ? new Promise<void>(() => {}) : Promise.resolve();
    }, 20);
    s.push([{ t: "take" }]);
    s.push([{ t: "release" }]);
    await sleep(40);
    expect(batches).toEqual([[{ t: "take" }], [{ t: "release" }]]);
    expect(s.pending()).toBe(0);
  });

  test("a failed request does not stall later input", async () => {
    const batches: ViewerMsg[][] = [];
    const s = createSender(async (msgs) => {
      batches.push(msgs);
      if (batches.length === 1) throw new Error("boom");
    });
    s.push([{ t: "take" }]);
    s.push([{ t: "release" }]);
    await sleep(0);
    await sleep(0);
    expect(batches).toEqual([[{ t: "take" }], [{ t: "release" }]]);
  });
});
