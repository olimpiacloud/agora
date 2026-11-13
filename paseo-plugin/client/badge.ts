export function cardBadge(streaming: boolean, newest: boolean, status: string | null): { label: string; live: boolean } {
  if (streaming && status) return { label: status, live: false };
  if (streaming) return { label: "Live", live: true };
  return { label: newest ? "Paused" : "Earlier", live: false };
}
