<script lang="ts">
  import ConfirmDialog from './ConfirmDialog.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { library, type Collection } from '$lib/library.svelte';

  // Its own section rather than a fourth TaxonomySection: a collection has a
  // description, which makes both forms two fields and a row two lines, and
  // threading that through as another optional prop would leave one component
  // rendering two different shapes on every branch.

  let name = $state('');
  let description = $state('');
  let error = $state('');
  let busy = $state(false);

  let editing = $state<Collection | null>(null);
  let editName = $state('');
  let editDescription = $state('');

  let deleting = $state<Collection | null>(null);

  // Every call wraps in try/finally: openapi-fetch reports a 4xx by resolving
  // with `error`, but lets a fetch-level rejection through, and that would
  // leave `busy` true and the whole section disabled until a reload.
  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (busy || name.trim() === '') return;
    busy = true;
    error = '';
    try {
      const { error: failure } = await api.POST('/api/collections', {
        body: { name: name.trim(), description: description.trim() }
      });
      // The server's words: it is the only thing that knows the name is taken.
      if (failure) {
        error = apiErrorMessage(failure, 'Could not add the collection.');
        return;
      }
      name = '';
      description = '';
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
      const { error: failure } = await api.PUT('/api/collections/{id}', {
        params: { path: { id: row.id } },
        body: { name: editName.trim(), description: editDescription.trim() }
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
      const { error: failure } = await api.DELETE('/api/collections/{id}', {
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
  <h2 class="text-base font-semibold">Collections</h2>
  <p class="mt-1 text-sm text-muted">
    Group models that go together, like the parts of one build. A model can be in
    any number of them.
  </p>

  {#if error}
    <p role="alert" class="mt-3 text-sm text-danger">{error}</p>
  {/if}

  <ul class="mt-4 divide-y divide-line">
    {#each library.collections as row (row.id)}
      <li class="py-2">
        {#if editing?.id === row.id}
          <!-- The row becomes the form, as the other sections do. -->
          <div class="flex flex-col gap-2">
            <input
              class="rounded border border-line-strong px-2 py-1 text-sm"
              aria-label="New name for {row.name}"
              bind:value={editName}
              disabled={busy}
            />
            <textarea
              class="rounded border border-line-strong px-2 py-1 text-sm"
              rows="2"
              aria-label="New description for {row.name}"
              bind:value={editDescription}
              disabled={busy}
            ></textarea>
            <div class="flex gap-2">
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
            </div>
          </div>
        {:else}
          <div class="flex items-center gap-3">
            <span class="min-w-0 flex-1 truncate text-sm">{row.name}</span>
            <!-- The count is why the delete copy can say what happens to the
                 models: nothing. -->
            <span class="shrink-0 text-xs text-faint">
              {row.modelCount}
              {row.modelCount === 1 ? 'model' : 'models'}
            </span>
            <button
              type="button"
              class="text-xs"
              onclick={() => (
                (editing = row),
                (editName = row.name),
                (editDescription = row.description),
                (error = '')
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
          </div>
          {#if row.description}
            <p class="mt-0.5 text-xs text-muted">{row.description}</p>
          {/if}
        {/if}
      </li>
    {/each}
  </ul>

  <form class="mt-4 flex flex-col gap-2" onsubmit={create}>
    <input
      class="rounded border border-line-strong px-2 py-1.5 text-sm"
      placeholder="Add collection"
      aria-label="New collection name"
      bind:value={name}
      disabled={busy}
    />
    <textarea
      class="rounded border border-line-strong px-2 py-1.5 text-sm"
      rows="2"
      placeholder="Description (optional)"
      aria-label="New collection description"
      bind:value={description}
      disabled={busy}
    ></textarea>
    <button
      type="submit"
      class="self-start rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink disabled:opacity-50"
      disabled={busy || name.trim() === ''}
    >
      Add
    </button>
  </form>
</section>

{#if deleting}
  <ConfirmDialog
    title="Delete {deleting.name}?"
    body={`Shows ${deleting.modelCount} ${deleting.modelCount === 1 ? 'model' : 'models'}. Deleting the collection does not delete any of them.`}
    confirm="Delete"
    {busy}
    {error}
    onconfirm={remove}
    oncancel={() => ((deleting = null), (error = ''))}
  />
{/if}
