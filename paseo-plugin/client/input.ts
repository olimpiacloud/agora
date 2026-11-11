import type { ViewerMsg } from "../shared/agora";

export type KeyLike = { code: string; key: string; ctrlKey: boolean; metaKey: boolean };

const CODES: Record<string, string> = {
  Minus: "-",
  Equal: "=",
  BracketLeft: "[",
  BracketRight: "]",
  Backslash: "\\",
  Semicolon: ";",
  Quote: "'",
  Comma: ",",
  Period: ".",
  Slash: "/",
  Backquote: "`",
  Space: "space",
  IntlBackslash: "<",
};

export function keyName(e: KeyLike): { key?: string; text?: string } {
  const c = e.code;
  if (c.startsWith("Key") && c.length === 4) return { key: c.slice(3).toLowerCase() };
  if (c.startsWith("Digit")) return { key: c.slice(5) };
  if (CODES[c]) return { key: CODES[c] };
  if (c.startsWith("Numpad") && /^\d$/.test(c.slice(6))) return { key: c.slice(6) };
  if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) return { text: e.key };
  return { key: e.key };
}

export function isPasteKey(e: KeyLike): boolean {
  return (e.ctrlKey || e.metaKey) && e.code === "KeyV";
}

export const PASTE_KEYS: ViewerMsg[] = [
  { t: "key", key: "ctrl", down: true },
  { t: "key", key: "v", down: true },
  { t: "key", key: "v", down: false },
  { t: "key", key: "ctrl", down: false },
];

export function tapKeys(combo: string): ViewerMsg[] {
  const keys = combo.split("+").filter(Boolean);
  return [...keys.map((key): ViewerMsg => ({ t: "key", key, down: true })), ...keys.reverse().map((key): ViewerMsg => ({ t: "key", key, down: false }))];
}

export function click(x: number, y: number, b = 0): ViewerMsg[] {
  return [
    { t: "down", x, y, b },
    { t: "up", x, y, b },
  ];
}

export function wheelSteps(x: number, y: number, dy: number): ViewerMsg[] {
  const n = Math.min(Math.abs(Math.trunc(dy)), 10);
  return Array.from({ length: n }, (): ViewerMsg => ({ t: "wheel", x, y, dy: Math.sign(dy) }));
}

export function createWheel() {
  let acc = 0;
  return (x: number, y: number, deltaX: number, deltaY: number, deltaMode: number): ViewerMsg[] => {
    acc += deltaMode === 1 ? deltaY * 40 : deltaY;
    const n = Math.trunc(acc / 60);
    acc -= n * 60;
    const out = wheelSteps(x, y, n);
    if (Math.abs(deltaX) > Math.abs(deltaY)) out.push({ t: "wheel", x, y, dx: Math.sign(deltaX) });
    return out;
  };
}

export function createSeat() {
  const buttons = new Set<number>();
  const keys = new Set<string>();
  let x = 0;
  let y = 0;
  const releaseButtons = (): ViewerMsg[] => {
    const out = [...buttons].map((b): ViewerMsg => ({ t: "up", x, y, b }));
    buttons.clear();
    return out;
  };
  const releaseKeys = (): ViewerMsg[] => {
    const out = [...keys].reverse().map((key): ViewerMsg => ({ t: "key", key, down: false }));
    keys.clear();
    return out;
  };
  return {
    move(px: number, py: number): ViewerMsg[] {
      x = px;
      y = py;
      return [{ t: "move", x, y }];
    },
    down(px: number, py: number, b: number): ViewerMsg[] {
      x = px;
      y = py;
      if (b < 0 || buttons.has(b)) return [];
      buttons.add(b);
      return [{ t: "down", x, y, b }];
    },
    up(px: number, py: number, b: number): ViewerMsg[] {
      x = px;
      y = py;
      if (!buttons.delete(b)) return [];
      return [{ t: "up", x, y, b }];
    },
    key(e: KeyLike, down: boolean): ViewerMsg[] {
      const k = keyName(e);
      if (down && k.text) return [{ t: "text", s: k.text }];
      if (!k.key || k.key.length > 32) return [];
      if (down) {
        keys.add(k.key);
        return [{ t: "key", key: k.key, down: true }];
      }
      if (!keys.delete(k.key)) return [];
      return [{ t: "key", key: k.key, down: false }];
    },
    releaseButtons,
    releaseKeys,
    releaseAll(): ViewerMsg[] {
      return [...releaseButtons(), ...releaseKeys()];
    },
    held(): number {
      return buttons.size + keys.size;
    },
  };
}

export function throttle<T>(ms: number, fire: (v: T) => void, now: () => number = () => performance.now()) {
  let last = -Infinity;
  let pending: { v: T } | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const run = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    if (!pending) return;
    const { v } = pending;
    pending = null;
    last = now();
    fire(v);
  };
  return {
    push(v: T) {
      const t = now();
      if (!timer && t - last >= ms) {
        last = t;
        fire(v);
        return;
      }
      pending = { v };
      if (!timer) timer = setTimeout(run, Math.max(0, ms - (t - last)));
    },
    flush: run,
    cancel() {
      if (timer) clearTimeout(timer);
      timer = null;
      pending = null;
    },
  };
}

export function createSender(post: (msgs: ViewerMsg[]) => Promise<unknown>, timeoutMs = 4000, max = 64) {
  const queue: ViewerMsg[] = [];
  let busy = false;
  const pump = async () => {
    if (busy) return;
    busy = true;
    try {
      while (queue.length) {
        const batch = queue.splice(0, max);
        let t: ReturnType<typeof setTimeout> | undefined;
        await Promise.race([post(batch).catch(() => {}), new Promise<void>((resolve) => (t = setTimeout(resolve, timeoutMs)))]);
        clearTimeout(t);
      }
    } finally {
      busy = false;
    }
  };
  return {
    push(msgs: ViewerMsg[]) {
      for (const m of msgs) {
        if (m.t === "move" && queue[queue.length - 1]?.t === "move") queue[queue.length - 1] = m;
        else queue.push(m);
      }
      void pump();
    },
    pending(): number {
      return queue.length;
    },
  };
}
