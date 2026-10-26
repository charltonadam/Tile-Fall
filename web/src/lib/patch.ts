import type { Patch, Scene } from "./types";

/** Which parts of the scene a patch touched, so the renderer can redraw only those. */
export interface Changes {
  all: boolean; // snapshot or meta change: redraw everything
  terrainKeys: string[];
  objects: boolean;
  walls: boolean;
  tokens: boolean;
}

export const everything = (): Changes => ({ all: true, terrainKeys: [], objects: true, walls: true, tokens: true });

function applyMap<T>(dst: Record<string, T>, src: Record<string, T | null> | undefined): boolean {
  if (!src) return false;
  for (const [k, v] of Object.entries(src)) {
    if (v === null) delete dst[k];
    else dst[k] = v;
  }
  return true;
}

/** Applies a server patch in place. Mirrors Scene.apply in internal/scene/scene.go. */
export function applyPatch(s: Scene, p: Patch): Changes {
  const ch: Changes = { all: false, terrainKeys: [], objects: false, walls: false, tokens: false };
  if (p.meta) {
    Object.assign(s, p.meta);
    ch.all = true;
  }
  if (p.terrain) {
    for (const [k, v] of Object.entries(p.terrain)) {
      if (v === "") delete s.terrain[k];
      else s.terrain[k] = v;
      ch.terrainKeys.push(k);
    }
  }
  ch.objects = applyMap(s.objects, p.objects);
  ch.walls = applyMap(s.walls, p.walls);
  ch.tokens = applyMap(s.tokens, p.tokens);
  if (p.version) s.version = p.version;
  return ch;
}
