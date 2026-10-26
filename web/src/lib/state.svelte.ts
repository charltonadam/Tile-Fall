import type { Status } from "./connection";
import type { Catalog, Role, Scene, TokenTemplate } from "./types";

export type Tool = "select" | "terrain" | "erase" | "object" | "wall" | "token";

export type Selection = { kind: "object" | "token" | "wall"; id: string } | null;

/** The token edit dialog: a token on the map, or a library template ("" id = new template). */
export type Editing = { kind: "token" | "template"; id: string } | null;

export interface Toast {
  id: number;
  text: string;
}

/**
 * UI state shared by components. The scene itself is mutated in place by
 * patches (it can be large, so it is not deeply reactive); anything that
 * displays scene data should read `app.rev` so it re-runs when it changes.
 */
class AppState {
  role = $state<Role>("player");
  status = $state<Status>("connecting");
  scene = $state.raw<Scene | null>(null);
  rev = $state(0);
  catalog = $state.raw<Catalog | null>(null);
  library = $state.raw<TokenTemplate[]>([]);

  tool = $state<Tool>("select");
  /** sidebar shows scene settings/list instead of the tool and selection panels */
  scenePanel = $state(false);
  terrain = $state("grass");
  brush = $state(1);
  assetId = $state("tree_oak");
  wallStyle = $state("stone");
  selection = $state<Selection>(null);
  /** library template the token tool places (and the one being dragged from the tray) */
  templateId = $state("");
  editing = $state<Editing>(null);

  toasts = $state<Toast[]>([]);
  private toastId = 0;

  toast(text: string) {
    const id = ++this.toastId;
    this.toasts = [...this.toasts, { id, text }];
    setTimeout(() => (this.toasts = this.toasts.filter((t) => t.id !== id)), 4000);
  }

  template(id: string) {
    return this.library.find((t) => t.id === id);
  }

  get isDM() {
    return this.role === "dm";
  }
}

export const app = new AppState();
