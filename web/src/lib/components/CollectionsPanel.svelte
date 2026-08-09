<script lang="ts">
  import type { Collection } from '$lib/library.svelte';

  // A membership as the model detail sends it: no count, because the count is
  // of the whole collection and belongs to the sidebar, not to this model.
  type Membership = { id: number; name: string };

  let {
    memberships,
    all,
    loaded = false,
    error = '',
    mutating = false,
    onadd,
    onremove
  }: {
    /** The collections this model is in, as the detail response sends them. */
    memberships: Membership[];
    /** Every collection the user has, from the shared store. */
    all: Collection[];
    /** Whether the store has answered yet. The store starts empty, so without
     *  this the panel claims the user has no collections for the length of the
     *  first request. */
    loaded?: boolean;
    /** A refused add or remove. It lives here rather than in a dialog because
     *  neither write has one. */
    error?: string;
    mutating?: boolean;
    onadd: (collectionId: number) => void;
    onremove: (collection: Membership) => void;
  } = $props();

  // The ones it is not in yet. Offering a collection it already belongs to
  // would be a control whose only outcome is nothing happening.
  const available = $derived(
    all.filter((c) => !memberships.some((m) => m.id === c.id))
  );

  function add(event: Event) {
    const select = event.currentTarget as HTMLSelectElement;
    const value = select.value;
    // Put the picker back to its prompt, on the element rather than through a
    // piece of state: the state would already be '' from the last add, so
    // writing '' to it again is not a change and Svelte would leave the select
    // showing the collection just chosen - which reads as if the model is in
    // it. The picker is a verb, not a field.
    select.value = '';
    if (value) onadd(Number(value));
  }
</script>

<!--
  Which collections this model is filed in, and a way to file it in another.

  Always rendered, unlike the Versions panel: a model with no versions has
  nothing to say about versions, but a model in no collections still needs
  somewhere to be put into one. Collections are made in Settings, so with none
  made yet the panel says that rather than showing an empty picker.
-->
<section class="rounded-tile border border-line bg-surface">
  <h2 class="border-b border-line px-4 py-3 text-sm font-semibold">Collections</h2>
  {#if error}
    <p role="alert" class="border-b border-line px-4 py-2 text-sm text-danger">{error}</p>
  {/if}
  <div class="px-4 py-3">
    {#if memberships.length > 0}
      <ul class="flex flex-wrap gap-1.5">
        {#each memberships as collection (collection.id)}
          <li class="flex items-center gap-1 rounded bg-chip px-2 py-0.5 text-xs text-muted">
            <a href="/?collectionId={collection.id}">{collection.name}</a>
            <button
              type="button"
              class="text-faint disabled:opacity-50"
              disabled={mutating}
              aria-label="Remove from {collection.name}"
              onclick={() => onremove(collection)}
            >
              ×
            </button>
          </li>
        {/each}
      </ul>
    {/if}

    {#if loaded && all.length === 0}
      <p class="text-sm text-muted">
        No collections yet. Make one in <a class="underline" href="/settings">Settings</a>.
      </p>
    {:else if available.length > 0}
      <label class="mt-2 flex items-center gap-2 text-sm text-muted">
        <span>Add to</span>
        <select
          onchange={add}
          disabled={mutating}
          class="rounded border border-line px-2 py-1.5 text-sm disabled:opacity-50"
        >
          <option value="">Choose a collection</option>
          {#each available as collection (collection.id)}
            <option value={String(collection.id)}>{collection.name}</option>
          {/each}
        </select>
      </label>
    {/if}
  </div>
</section>
