<script lang="ts">
  import { onMount } from "svelte";
  import { Connection } from "./lib/connection";
  import { MapView } from "./lib/map/MapView";
  import { applyPatch } from "./lib/patch";
  import { app } from "./lib/state.svelte";
  import type { Command, Role } from "./lib/types";
  import Toolbar from "./components/Toolbar.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import TokenEditor from "./components/TokenEditor.svelte";

  const role: Role = new URLSearchParams(location.search).get("role") === "dm" ? "dm" : "player";
  app.role = role;

  let mapEl: HTMLElement;
  let view: MapView;
  let conn: Connection;

  async function send(cmd: Command) {
    const ack = await conn.command(cmd);
    if (!ack.ok) {
      app.toast(ack.error ?? "command failed");
      conn.sync(); // drop any optimistic change
    }
    return ack;
  }

  async function undo() {
    const ack = await conn.undo();
    if (!ack.ok) app.toast(ack.error ?? "undo failed");
  }

  onMount(() => {
    view = new MapView({
      get isDM() { return app.isDM; },
      get tool() { return app.tool; },
      get terrain() { return app.terrain; },
      get brush() { return app.brush; },
      get assetId() { return app.assetId; },
      get wallStyle() { return app.wallStyle; },
      get templateId() { return app.templateId; },
      template: (id) => app.template(id),
      editToken: (id) => (app.editing = { kind: "token", id }),
      get selection() { return app.selection; },
      set selection(s) { app.selection = s; },
      send,
      touched: () => app.rev++,
    });
    let destroyed = false;
    view.init(mapEl).then(() => {
      if (destroyed) return;
      fetch("/api/catalog")
        .then((r) => r.json())
        .then((cat) => {
          app.catalog = cat;
          view.setCatalog(cat);
          if (app.scene) view.update(app.scene, null);
        });
      conn = new Connection(role, {
        hello: (r) => (app.role = r),
        status: (s) => (app.status = s),
        snapshot: (scene) => {
          app.scene = scene;
          app.rev++;
          if (app.selection && !app.scene[`${app.selection.kind}s` as "objects" | "tokens" | "walls"][app.selection.id]) app.selection = null;
          view.update(scene, null);
        },
        library: (templates) => {
          app.library = templates;
          if (app.templateId && !app.template(app.templateId)) app.templateId = "";
        },
        patch: (p) => {
          if (!app.scene) return;
          const ch = applyPatch(app.scene, p);
          app.rev++;
          view.update(app.scene, ch);
        },
      });
    });

    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "z" && app.isDM) {
        const t = e.target as HTMLElement;
        if (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || app.editing) return;
        e.preventDefault();
        undo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      destroyed = true;
      window.removeEventListener("keydown", onKey);
      conn?.close();
      view.destroy();
    };
  });

  // keep the overlay in sync with UI-driven selection/tool changes
  $effect(() => {
    app.selection;
    app.tool;
    app.brush;
    view?.refreshOverlay();
  });
</script>

<div class="shell" class:dm={app.isDM}>
  <header>
    <span class="logo">Tile-Fall</span>
    <span class="scene-name">{app.scene?.name ?? ""}</span>
    <span class="spacer"></span>
    {#if app.isDM}
      <button onclick={undo} title="Undo (Ctrl+Z)">Undo</button>
      <a class="role" href="/" target="_blank" title="Open the player view">Player view ↗</a>
    {/if}
    <span class="badge">{app.isDM ? "DM" : "Player"}</span>
    <span class="status {app.status}" title={app.status}></span>
  </header>
  {#if app.isDM}
    <Toolbar />
  {/if}
  <main bind:this={mapEl} class:painting={app.isDM && app.tool !== "select"}></main>
  {#if app.isDM}
    <Sidebar {send} fit={() => view?.fit()} />
  {/if}
  {#if app.isDM && app.editing}
    {#key `${app.editing.kind}:${app.editing.id}`}
      <TokenEditor {send} />
    {/key}
  {/if}
  <div class="toasts">
    {#each app.toasts as t (t.id)}
      <div class="toast">{t.text}</div>
    {/each}
  </div>
</div>

<style>
  .shell {
    display: grid;
    grid-template-rows: 44px 1fr;
    grid-template-columns: 1fr;
    grid-template-areas: "header" "map";
    height: 100vh;
  }
  .shell.dm {
    grid-template-columns: 52px 1fr 300px;
    grid-template-areas: "header header header" "tools map side";
  }
  header {
    grid-area: header;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 12px;
    background: var(--panel);
    border-bottom: 1px solid var(--border);
  }
  .logo {
    font-weight: 700;
    letter-spacing: 0.04em;
    color: var(--accent);
  }
  .scene-name {
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .spacer {
    flex: 1;
  }
  .role {
    color: var(--muted);
    font-size: 13px;
  }
  .badge {
    font-size: 12px;
    padding: 2px 8px;
    border-radius: 10px;
    background: var(--border);
  }
  .status {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: #c0392b;
  }
  .status.open {
    background: #2ecc71;
  }
  .status.connecting {
    background: #f1c40f;
  }
  main {
    grid-area: map;
    position: relative;
    overflow: hidden;
    min-width: 0;
    min-height: 0;
  }
  main.painting {
    cursor: crosshair;
  }
  .toasts {
    position: fixed;
    bottom: 16px;
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    gap: 6px;
    pointer-events: none;
  }
  .toast {
    background: #5a1f1f;
    border: 1px solid #a33;
    padding: 8px 14px;
    border-radius: 6px;
  }
  @media (max-width: 760px) {
    .shell.dm {
      grid-template-columns: 52px 1fr;
      grid-template-rows: 44px 1fr 40vh;
      grid-template-areas: "header header" "tools map" "side side";
    }
  }
</style>
