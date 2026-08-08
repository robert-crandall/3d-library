<script lang="ts">
  import { untrack } from 'svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { library } from '$lib/library.svelte';

  type Row = { id: number; name: string; color?: string; modelCount: number };

  let {
    title,
    singular,
    hint,
    path,
    rows,
    /** Categories carry a colour and the other two do not. That is the only
     *  difference between the three sections, which is why there is one of
     *  these rather than three pages of the same form. */
    colors,
    deleteBody
  }: {
    title: string;
    /** Spelled out rather than derived from the title, because "categories"
     *  minus an s is "categorie". */
    singular: string;
    hint: string;
    path: '/api/categories' | '/api/tags' | '/api/materials';
    rows: Row[];
    colors?: string[];
    deleteBody: (row: Row) => string;
  } = $props();

  let name = $state('');
  // The first swatch, chosen once. This is an initial value for the form, not
  // a mirror of the prop, so reading it untracked is the point.
  let color = $state(untrack(() => colors?.[0] ?? ''));
  let error = $state('');
  let busy = $state(false);

  let editing = $state<Row | null>(null);
  let editName = $state('');
  let editColor = $state('');

  let deleting = $state<Row | null>(null);

  // The casts exist because openapi-fetch resolves the body and response types
  // from the literal path, and a union of three literals is not one of them.
  // The three endpoints are the same shape apart from `color`, so the cast is
  // to the widest of them.
  type Any = '/api/categories';

  function body() {
    return colors ? { name: name.trim(), color } : { name: name.trim() };
  }

  // Each of the three wraps its request in try/finally. openapi-fetch reports a
  // 4xx by resolving with `error`, but it lets a fetch-level rejection through -
  // and without the finally that rejection would leave `busy` true, so every
  // control in the section stays disabled until the page is reloaded.
  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (busy || name.trim() === '') return;
    busy = true;
    error = '';
    try {
      const { error: failure } = await api.POST(path as Any, { body: body() as never });
      // The server's words, not ours: it is the only thing that knows the name
      // is already taken, and its message already says so.
      if (failure) {
        error = apiErrorMessage(failure, `Could not add the ${singular}.`);
        return;
      }
      name = '';
      await library.refresh();
    } catch {
      error = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }

  async function rename() {
    const row = editing;
    if (!row || busy || editName.trim() === '') return;
    busy = true;
    error = '';
    try {
      const { error: failure } = await api.PUT(`${path}/{id}` as `${Any}/{id}`, {
        params: { path: { id: row.id } },
        body: (colors
          ? { name: editName.trim(), color: editColor }
          : { name: editName.trim() }) as never
      });
      if (failure) {
        error = apiErrorMessage(failure, 'Could not save the change.');
        return;
      }
      editing = null;
      await library.refresh();
    } catch {
      error = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }

  async function remove() {
    const row = deleting;
    if (!row || busy) return;
    busy = true;
    error = '';
    try {
      const { error: failure } = await api.DELETE(`${path}/{id}` as `${Any}/{id}`, {
        params: { path: { id: row.id } }
      });
      if (failure) {
        error = apiErrorMessage(failure, 'Could not delete it.');
        return;
      }
      deleting = null;
      await library.refresh();
    } catch {
      error = 'Could not reach the server.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="rounded-tile border border-line bg-surface px-5 py-4">
  <h2 class="text-base font-semibold">{title}</h2>
  <p class="mt-1 text-sm text-muted">{hint}</p>

  {#if error}
    <p role="alert" class="mt-3 text-sm text-danger">{error}</p>
  {/if}

  <ul class="mt-4 divide-y divide-line">
    {#each rows as row (row.id)}
      <li class="flex items-center gap-3 py-2">
        {#if editing?.id === row.id}
          <!-- The row becomes the form. A separate dialog for a one-field
               rename is more chrome than the change deserves. -->
          <input
            class="min-w-0 flex-1 rounded border border-line-strong px-2 py-1 text-sm"
            aria-label="New name for {row.name}"
            bind:value={editName}
            disabled={busy}
          />
          {#if colors}
            <div class="flex gap-1">
              {#each colors as option (option)}
                <button
                  type="button"
                  class="h-5 w-5 rounded-xs border"
                  class:border-ink={editColor === option}
                  class:border-transparent={editColor !== option}
                  style="background-color: {option}"
                  aria-label="Colour {option}"
                  aria-pressed={editColor === option}
                  onclick={() => (editColor = option)}
                ></button>
              {/each}
            </div>
          {/if}
          <button
            type="button"
            class="rounded bg-accent px-3 py-1 text-sm font-medium text-accent-ink"
            onclick={rename}
            disabled={busy}
          >
            Save
          </button>
          <button
            type="button"
            class="rounded border border-line-strong px-3 py-1 text-sm"
            onclick={() => ((editing = null), (error = ''))}
            disabled={busy}
          >
            Cancel
          </button>
        {:else}
          {#if row.color}
            <span
              class="h-2.5 w-2.5 shrink-0 rounded-xs"
              style="background-color: {row.color}"
              aria-hidden="true"
            ></span>
          {/if}
          <span class="min-w-0 flex-1 truncate text-sm">{row.name}</span>
          <!-- The count is why the delete copy can be honest about what is
               about to happen to those models. -->
          <span class="shrink-0 text-xs text-faint">
            {row.modelCount}
            {row.modelCount === 1 ? 'model' : 'models'}
          </span>
          <button
            type="button"
            class="text-xs"
            onclick={() => (
              (editing = row), (editName = row.name), (editColor = row.color ?? ''), (error = '')
            )}
          >
            Rename
          </button>
          <button
            type="button"
            class="text-xs text-danger"
            aria-label="Delete {row.name}"
            onclick={() => ((deleting = row), (error = ''))}
          >
            Delete
          </button>
        {/if}
      </li>
    {/each}
  </ul>

  <form class="mt-4 flex items-center gap-2" onsubmit={create}>
    <input
      class="min-w-0 flex-1 rounded border border-line-strong px-2 py-1.5 text-sm"
      placeholder="Add {singular}"
      aria-label="New {singular} name"
      bind:value={name}
      disabled={busy}
    />
    {#if colors}
      <div class="flex gap-1">
        {#each colors as option (option)}
          <button
            type="button"
            class="h-5 w-5 rounded-xs border"
            class:border-ink={color === option}
            class:border-transparent={color !== option}
            style="background-color: {option}"
            aria-label="Colour {option}"
            aria-pressed={color === option}
            onclick={() => (color = option)}
          ></button>
        {/each}
      </div>
    {/if}
    <button
      type="submit"
      class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink disabled:opacity-50"
      disabled={busy || name.trim() === ''}
    >
      Add
    </button>
  </form>
</section>

{#if deleting}
  <ConfirmDialog
    title="Delete {deleting.name}?"
    body={deleteBody(deleting)}
    confirm="Delete"
    {busy}
    {error}
    onconfirm={remove}
    oncancel={() => ((deleting = null), (error = ''))}
  />
{/if}
