<script lang="ts">
  import { app, type Tool } from "../lib/state.svelte";

  const tools: { id: Tool; icon: string; label: string; key: string }[] = [
    { id: "select", icon: "➚", label: "Select / move", key: "v" },
    { id: "terrain", icon: "▦", label: "Paint terrain", key: "b" },
    { id: "erase", icon: "⌫", label: "Erase terrain", key: "e" },
    { id: "object", icon: "❦", label: "Place objects", key: "o" },
    { id: "wall", icon: "▭", label: "Draw walls (click points, double-click or Enter to finish)", key: "w" },
    { id: "token", icon: "●", label: "Add token", key: "t" },
  ];

  function onKey(e: KeyboardEvent) {
    const t = e.target as HTMLElement;
    if (e.ctrlKey || e.metaKey || e.altKey || t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || app.editing) return;
    const tool = tools.find((x) => x.key === e.key.toLowerCase());
    if (tool && !(app.selection?.kind === "object" && e.key.toLowerCase() === "r")) app.tool = tool.id;
  }
</script>

<svelte:window onkeydown={onKey} />

<nav>
  {#each tools as t}
    <button class:active={app.tool === t.id} title="{t.label} ({t.key.toUpperCase()})" onclick={() => (app.tool = t.id)}>
      {t.icon}
    </button>
  {/each}
</nav>

<style>
  nav {
    grid-area: tools;
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 6px;
    background: var(--panel);
    border-right: 1px solid var(--border);
  }
  button {
    width: 40px;
    height: 40px;
    font-size: 18px;
    padding: 0;
  }
  button.active {
    background: var(--accent);
    color: #1b1b1f;
    border-color: var(--accent);
  }
</style>
