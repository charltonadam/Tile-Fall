// Mirrors the JSON shapes in internal/scene (scene.go, catalog.go, grid.go).

export type GridType = "square" | "hex";
export type HexOrientation = "pointy" | "flat";

export interface Grid {
  type: GridType;
  hexOrientation?: HexOrientation;
  cellSize: number;
  visible: boolean;
  color: string;
  opacity: number;
  /** distance one cell represents; absent means 5 (see gridScale) */
  distance?: number;
  units?: string;
}

export interface Cell {
  col: number;
  row: number;
}

export interface Point {
  x: number;
  y: number;
}

export interface Background {
  url: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Meta {
  name: string;
  width: number;
  height: number;
  grid: Grid;
  background: Background | null;
}

export type Layer = "floor" | "overhead";

export interface SceneObject {
  id: string;
  assetId: string;
  x: number;
  y: number;
  rotation: number;
  scale: number;
  layer: Layer;
  z: number;
}

export interface Wall {
  id: string;
  points: Point[];
  closed: boolean;
  style: string;
}

export interface Token {
  id: string;
  name: string;
  cell: Cell;
  size: number;
  color: string;
  imageUrl?: string;
  hp: number;
  /** 0 means hit points are not tracked */
  maxHp: number;
  /** library template it was spawned from; provenance only, the two are independent */
  templateId?: string;
}

/** A token library entry: the defaults new tokens are spawned with (internal/game/library.go). */
export interface TokenTemplate {
  id: string;
  name: string;
  size: number;
  color: string;
  imageUrl?: string;
  maxHp: number;
}

export interface Scene extends Meta {
  id: string;
  version: number;
  nextId: number;
  terrain: Record<string, string>;
  objects: Record<string, SceneObject>;
  walls: Record<string, Wall>;
  tokens: Record<string, Token>;
}

export interface Patch {
  version: number;
  meta?: Meta;
  terrain?: Record<string, string>;
  objects?: Record<string, SceneObject | null>;
  walls?: Record<string, Wall | null>;
  tokens?: Record<string, Token | null>;
}

export interface TerrainType {
  id: string;
  name: string;
  color: string;
  description: string;
  tags: string[];
}

export interface Asset {
  id: string;
  name: string;
  description: string;
  tags: string[];
  file: string;
  size: number;
  layer: Layer;
  color: string;
  shape: string;
}

export interface WallStyle {
  id: string;
  name: string;
  color: string;
  width: number;
}

export interface Catalog {
  terrains: TerrainType[];
  assets: Asset[];
  wallStyles: WallStyle[];
}

export type Role = "dm" | "player";

/** A command as sent to the server: {type, ...fields}. See internal/scene/commands.go. */
export type Command = { type: string; [k: string]: unknown };
