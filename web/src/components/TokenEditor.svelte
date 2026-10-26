<script lang="ts">
  // Edit dialog for one token on the map or one library template. The two are
  // deliberately separate: saving a token sends update_token for that token
  // only; saving a template changes the defaults future spawns get.
  import { onMount } from "svelte";
  import { createTemplate, deleteTemplate, updateTemplate } from "../lib/library";
  import { app } from "../lib/state.svelte";
  import type { Command } from "../lib/types";
  import TokenChip from "./TokenChip.svelte";

  let { send }: { send: (c: Command) => Promise<unknown> } = $props();

  // App keys this component on app.editing, so it is fixed for our lifetime
  const editing = app.editing!;
  const isToken = editing.kind === "token";
  const isNew = !isToken && editing.id === "";

  const token = isToken ? app.scene?.tokens[editing.id] : undefined;
  const source = isToken ? token : app.template(editing.id);
  const original = {
    name: source?.name ?? "New creature",
    size: source?.size ?? 1,
    color: source?.color ?? "#c0392b",
    imageUrl: source?.imageUrl ?? "",
    maxHp: source?.maxHp ?? 10,
    hp: token?.hp ?? 0,
  };
  let draft = $state({ ...original });
  const fromTemplate = token?.templateId ? app.template(token.templateId) : undefined;

  let dialog: HTMLDialogElement;
  onMount(() => dialog.showModal());

  // close if the thing being edited disappears (deleted elsewhere, scene switched)
  $effect(() => {
    app.rev;
    if (isToken ? !app.scene?.tokens[editing.id] : !isNew && !app.template(editing.id)) close();
  });

  function close() {
    app.editing = null;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    const d = { ...draft, name: draft.name.trim(), size: Number(draft.size), maxHp: Math.max(0, Math.round(draft.maxHp || 0)), hp: Math.round(draft.hp || 0) };
    if (!d.name) return app.toast("a token needs a name");
    if (isToken) {
      const cmd: Command = { type: "update_token", id: editing.id };
      for (const k of ["name", "size", "color", "imageUrl", "maxHp", "hp"] as const) if (d[k] !== original[k]) cmd[k] = d[k];
      if (Object.keys(cmd).length > 2) await send(cmd);
      close();
      return;
    }
    const fields = { name: d.name, size: d.size, color: d.color, imageUrl: d.imageUrl, maxHp: d.maxHp };
    if (isNew) {
      const id = await createTemplate(fields);
      if (id) {
        app.templateId = id;
        close();
      }
    } else if (await updateTemplate(editing.id, fields)) close();
  }

  async function remove() {
    if (isToken) {
      send({ type: "remove_tokens", ids: [editing.id] });
      if (app.selection?.id === editing.id) app.selection = null;
      close();
    } else if (await deleteTemplate(editing.id)) {
      if (app.templateId === editing.id) app.templateId = "";
      close();
    }
  }
</script>

<dialog bind:this={dialog} onclose={close} onclick={(e) => e.target === dialog && close()}>
  <form onsubmit={save}>
    <header>
      <TokenChip name={draft.name} color={draft.color} imageUrl={draft.imageUrl} px={48} />
      <div>
        <h2>{isToken ? "Token" : isNew ? "New template" : "Template"}</h2>
        <p class="hint">
          {#if isToken}
            Changes apply to this token only{fromTemplate ? ` (spawned from ${fromTemplate.name})` : ""}.
          {:else}
            Defaults for new tokens. Tokens already on the map keep their values.
          {/if}
        </p>
      </div>
    </header>

    <label class="row">Name <input type="text" bind:value={draft.name} required /></label>
    <label class="row">HP
      {#if isToken}
        <input type="number" min="0" max={draft.maxHp} bind:value={draft.hp} title="current" />
        /
      {/if}
      <input type="number" min="0" max="100000" bind:value={draft.maxHp} title="maximum" />
      <span class="hint">{isToken ? "current / max" : "max (spawns at full)"}; 0 = untracked</span>
    </label>
    <label class="row">Size
      <select bind:value={draft.size}>
        <option value={1}>Medium (1)</option>
        <option value={2}>Large (2)</option>
        <option value={3}>Huge (3)</option>
        <option value={4}>Gargantuan (4)</option>
      </select>
    </label>
    <label class="row">Color <input type="color" bind:value={draft.color} /></label>
    <label class="row">Image URL <input type="text" bind:value={draft.imageUrl} placeholder="optional (uploads coming soon)" /></label>

    <footer>
      {#if !isNew}<button type="button" class="danger" onclick={remove}>Delete</button>{/if}
      <span class="spacer"></span>
      <button type="button" onclick={close}>Cancel</button>
      <button type="submit" class="primary">{isNew ? "Create" : "Save"}</button>
    </footer>
  </form>
</dialog>

<style>
  dialog {
    width: min(420px, calc(100vw - 32px));
    padding: 0;
    color: var(--text);
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  form {
    /* padding lives here so a click on the dialog element itself is a backdrop click */
    padding: 16px;
  }
  dialog::backdrop {
    background: #0008;
  }
  header {
    display: flex;
    gap: 12px;
    align-items: center;
    margin-bottom: 14px;
  }
  h2 {
    margin: 0 0 2px;
    font-size: 16px;
  }
  .hint {
    margin: 0;
  }
  footer {
    display: flex;
    gap: 8px;
    margin-top: 14px;
  }
  .spacer {
    flex: 1;
  }
  .primary {
    background: var(--accent);
    border-color: var(--accent);
    color: #1b1b1f;
  }
</style>
