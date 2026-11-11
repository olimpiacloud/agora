import { defineRpc } from "@getpaseo/plugin";
import { z } from "zod";

export const agoraWorkspace = z.object({
  id: z.string(),
  name: z.string(),
  state: z.string(),
  cursors: z.number(),
  viewers: z.number(),
  linked: z.boolean(),
});

export const listWorkspaces = defineRpc({
  name: "agora.workspaces",
  input: z.object({ paseoWorkspaceId: z.string().optional() }),
  output: z.object({ items: z.array(agoraWorkspace), linked: z.string().nullable(), error: z.string().nullable() }),
});

export const ensureWorkspace = defineRpc({
  name: "agora.ensure_workspace",
  input: z.object({ paseoWorkspaceId: z.string(), name: z.string() }),
  output: z.object({ id: z.string() }),
});

export const getShot = defineRpc({
  name: "agora.shot",
  input: z.object({ id: z.string() }),
  output: z.object({ mime: z.string(), base64: z.string() }),
});

const point = { x: z.number(), y: z.number() };

export const viewerMsg = z.discriminatedUnion("t", [
  z.object({ t: z.literal("move"), ...point }),
  z.object({ t: z.enum(["down", "up"]), ...point, b: z.number().int().min(0).max(31) }),
  z.object({ t: z.literal("wheel"), ...point, dx: z.number().optional(), dy: z.number().optional() }),
  z.object({ t: z.literal("key"), key: z.string().min(1).max(32), down: z.boolean() }),
  z.object({ t: z.enum(["text", "clip"]), s: z.string().max(65536) }),
  z.object({ t: z.enum(["take", "release"]) }),
]);

export type ViewerMsg = z.output<typeof viewerMsg>;

export const liveInput = defineRpc({
  name: "agora.live_input",
  input: z.object({
    workspace: z.string(),
    tier: z.enum(["card", "tile", "full"]),
    maxWidth: z.number().optional(),
    msgs: z.array(viewerMsg).min(1).max(256),
  }),
  output: z.object({ ok: z.boolean(), error: z.string().nullable() }),
});

export const control = defineRpc({
  name: "agora.control",
  input: z.object({ workspace: z.string(), op: z.enum(["resume", "suspend"]) }),
  output: z.object({ ok: z.boolean(), error: z.string().nullable() }),
});

export const agoraCursor = z.object({
  id: z.string(),
  name: z.string(),
  status: z.string(),
  detail: z.string(),
  color: z.string(),
  x: z.number(),
  y: z.number(),
  goal: z.string(),
  parent: z.string(),
});

export const liveCursor = agoraCursor.extend({ agent: z.string() });

export const live = defineRpc({
  name: "agora.live",
  input: z.object({
    workspace: z.string(),
    cursor: z.string().optional(),
    tier: z.enum(["card", "tile", "full"]),
    maxWidth: z.number().optional(),
    metaOnly: z.boolean().optional(),
    after: z.number(),
    metaAfter: z.number(),
  }),
  output: z.object({
    seq: z.number(),
    frame: z.object({ w: z.number(), h: z.number(), b64: z.string() }).nullable(),
    region: z.object({ x: z.number(), y: z.number(), w: z.number(), h: z.number(), sw: z.number(), sh: z.number() }),
    status: z.string().nullable(),
    viewer: z.string().nullable(),
    metaSeq: z.number(),
    cursors: z.array(liveCursor).nullable(),
    locked: z.string().nullable(),
  }),
});

export const resolveWorkspace = defineRpc({
  name: "agora.resolve_workspace",
  input: z.object({ paseoWorkspaceId: z.string() }),
  output: z.object({ id: z.string().nullable() }),
});

export const BROWSE_RE = /agora__browser_(open|navigate)$/;

export const SHOT_RE = /agora-shot:(shot_[0-9a-f]+)/;
