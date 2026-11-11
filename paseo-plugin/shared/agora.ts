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

export const sendInput = defineRpc({
  name: "agora.input",
  input: z.object({
    workspace: z.string(),
    action: z.enum(["click", "double_click", "right_click", "type", "key", "scroll", "move", "down", "up"]),
    x: z.number().optional(),
    y: z.number().optional(),
    text: z.string().optional(),
    keys: z.string().optional(),
    dy: z.number().optional(),
    dx: z.number().optional(),
    button: z.number().optional(),
  }),
  output: z.object({ ok: z.boolean(), error: z.string().nullable() }),
});

export const control = defineRpc({
  name: "agora.control",
  input: z.object({ workspace: z.string(), op: z.enum(["control-take", "control-return", "resume", "suspend"]) }),
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
