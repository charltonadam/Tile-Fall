// WebSocket client for the protocol documented in internal/server/ws.go.

import type { Command, Patch, Role, Scene, TokenTemplate } from "./types";

export interface Ack {
  ok: boolean;
  version?: number;
  result?: unknown;
  error?: string;
}

export type Status = "connecting" | "open" | "closed";

export interface Handlers {
  hello(role: Role): void;
  snapshot(scene: Scene): void;
  patch(patch: Patch): void;
  /** DM only: the whole token library, on connect and after each change */
  library(templates: TokenTemplate[]): void;
  status(s: Status): void;
}

export class Connection {
  private ws: WebSocket | null = null;
  private nextId = 1;
  private pending = new Map<number, (a: Ack) => void>();
  private retry = 500;
  private stopped = false;

  constructor(private role: Role, private h: Handlers) {
    this.open();
  }

  private open() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/ws?role=${this.role}`);
    this.ws = ws;
    this.h.status("connecting");
    ws.onopen = () => {
      this.retry = 500;
      this.h.status("open");
    };
    ws.onmessage = (ev) => this.receive(JSON.parse(ev.data));
    ws.onclose = () => {
      this.ws = null;
      for (const resolve of this.pending.values()) resolve({ ok: false, error: "disconnected" });
      this.pending.clear();
      this.h.status("closed");
      if (!this.stopped) {
        setTimeout(() => this.open(), this.retry);
        this.retry = Math.min(this.retry * 2, 5000);
      }
    };
  }

  private receive(m: any) {
    switch (m.t) {
      case "hello":
        this.h.hello(m.role);
        break;
      case "snapshot":
        this.h.snapshot(m.scene);
        break;
      case "patch":
        this.h.patch(m.patch);
        break;
      case "library":
        this.h.library(m.templates ?? []);
        break;
      case "ack": {
        const resolve = this.pending.get(m.id);
        this.pending.delete(m.id);
        resolve?.({ ok: m.ok, version: m.version, result: m.result, error: m.error });
        break;
      }
    }
  }

  private request(msg: Record<string, unknown>): Promise<Ack> {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN) return Promise.resolve({ ok: false, error: "not connected" });
    const id = this.nextId++;
    ws.send(JSON.stringify({ ...msg, id }));
    return new Promise((resolve) => this.pending.set(id, resolve));
  }

  command(cmd: Command) {
    return this.request({ t: "cmd", cmd });
  }

  batch(cmds: Command[]) {
    return this.request({ t: "batch", cmds });
  }

  undo() {
    return this.request({ t: "undo" });
  }

  /** Ask for a fresh snapshot, e.g. after an optimistic update was rejected. */
  sync() {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify({ t: "sync" }));
  }

  close() {
    this.stopped = true;
    this.ws?.close();
  }
}
