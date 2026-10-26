// REST calls for the token library (routes in internal/server/server.go). The
// server pushes the updated library to every DM over the WebSocket, so these
// don't update app.library themselves.

import { app } from "./state.svelte";
import type { TokenTemplate } from "./types";

export type TemplateFields = Omit<TokenTemplate, "id">;

async function call(method: string, url: string, body?: unknown) {
  const r = await fetch(url, { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) app.toast(data.error ?? "request failed");
  return r.ok ? data : null;
}

/** Returns the new template's id, or null on failure. */
export async function createTemplate(t: TemplateFields): Promise<string | null> {
  return (await call("POST", "/api/library", t))?.id ?? null;
}

export async function updateTemplate(id: string, t: Partial<TemplateFields>) {
  return (await call("POST", `/api/library/${id}`, t)) !== null;
}

export async function deleteTemplate(id: string) {
  return (await call("DELETE", `/api/library/${id}`)) !== null;
}
