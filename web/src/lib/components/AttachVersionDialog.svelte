<script lang="ts">
  import Modal from './Modal.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { formatDate, formatFileCount } from '$lib/format';
  import type { Model } from '$lib/upload';

  let {
    parentId,
    busy = false,
    error = '',
    onattach,
    oncancel
  }: {
    /** The model the chosen one becomes a version of. Only used to keep it out
     *  of its own candidate list. */
    parentId: number;
    busy?: boolean;
    /** The server's refusal of the attach, shown under the list. Separate from
     *  the search's own failure, which is about a different request. */
    error?: string;
    onattach: (modelId: number) => void;
    oncancel: () => void;
  } = $props();

  let q = $state('');
  let results = $state<Model[]>([]);
  // True from the keystroke, not from the request: the debounce means a search
  // is pending for a quarter of a second before it is sent, and for that whole
  // time the rows on screen belong to a list that is about to be replaced.
  // Anything that acts on them has to be shut off for the pending window too,
  // not just the in-flight one.
  let searching = $state(true);
  // Separate, because an append invalidates nothing: the rows already on screen
  // stay exactly as they are, so picking one of them while the next page loads
  // is legitimate and must not be disabled.
  let loadingMore = $state(false);
  let searchError = $state('');
  let chosen = $state<number>();
  // What the last response said about the whole result set, so the picker can
  // tell whether there is a page after this one.
  let page = $state(1);
  let pageSize = $state(0);
  let total = $state(0);

  // The list endpoint returns roots only, one page at a time, so a library
  // bigger than a page is only reachable by searching or by paging. Both are
  // here: search narrows, Show more walks. Search alone is not enough, because
  // nothing stops a library holding thirty models whose names all contain the
  // word being searched for, and the thirtieth has to be attachable too.
  //
  // It deliberately does not hide models that already have versions. The server
  // refuses those, and that refusal has to be reachable from the UI - hiding
  // them here would make it unreachable and leave the rule untested by hand.
  let timer: ReturnType<typeof setTimeout> | undefined;
  const DEBOUNCE_MS = 250;

  // Out-of-order responses: type "brack", then "bracket", and the shorter
  // search can land second and overwrite the longer one's results. The counter
  // is the same pattern the library page uses.
  let generation = 0;

  // Whether the last response was one page of something longer. Derived from
  // the server's own numbers rather than `results.length`, which is shorter by
  // one whenever the parent itself was on the page.
  const truncated = $derived(pageSize > 0 && page * pageSize < total);

  /**
   * Read one page of candidates. `append` is what separates walking the list
   * from starting it over: a new search replaces the results, Show more adds to
   * them.
   */
  async function search(term: string, want: number, append: boolean) {
    const mine = ++generation;
    if (append) loadingMore = true;
    else searching = true;
    searchError = '';
    try {
      const { data, error: failure } = await api.GET('/api/models', {
        params: { query: { ...(term ? { q: term } : {}), ...(want > 1 ? { page: want } : {}) } }
      });
      if (mine !== generation) return;
      if (failure) {
        searchError = apiErrorMessage(failure, 'Could not load your models.');
        // A failed append keeps what is on screen. Clearing it would drop rows
        // that are still perfectly valid, and take the selection made against
        // them with it - leaving `chosen` naming a model the list no longer
        // shows, which is the one thing this picker must never allow.
        if (!append) results = [];
        return;
      }
      const found = data.items.filter((m) => m.id !== parentId);
      results = append ? [...results, ...found] : found;
      page = data.page;
      pageSize = data.pageSize;
      total = data.total;
    } catch {
      if (mine !== generation) return;
      searchError = 'Could not reach the server.';
      if (!append) results = [];
    } finally {
      if (mine === generation) {
        searching = false;
        loadingMore = false;
      }
    }
  }

  $effect(() => {
    const term = q.trim();
    clearTimeout(timer);
    // The results are about to be replaced, so a selection made against the
    // previous ones cannot stand: it names a model that need not be in the new
    // list, and leaving it set would let the form attach something the user can
    // no longer see.
    chosen = undefined;
    // Both of these belong on the keystroke rather than on the request. The
    // counter retires anything already in flight - without it a Show more sent
    // a moment ago still answers, and appends its page to a list this search is
    // about to throw away. The flag then keeps Show more and the radios off for
    // the pending window, so nothing new can be started or picked against rows
    // that are already known to be stale.
    generation++;
    searching = true;
    // The first run is the unsearched list, and it should not wait a quarter of
    // a second to appear.
    if (term === '') {
      search('', 1, false);
      return;
    }
    timer = setTimeout(() => search(term, 1, false), DEBOUNCE_MS);
    return () => clearTimeout(timer);
  });

  function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || searching || chosen === undefined) return;
    onattach(chosen);
  }
</script>

<Modal title="Add version" ondismiss={() => !busy && oncancel()}>
  <form onsubmit={submit}>
    <p class="mt-3 text-sm text-muted">
      Pick a model to file under this one. It leaves the library grid and its files stay with it.
    </p>

    <label class="mt-4 block text-sm font-medium" for="attach-search">Search</label>
    <input
      id="attach-search"
      type="search"
      class="mt-1 w-full rounded border border-line-strong px-2 py-1.5 text-sm"
      placeholder="Model name"
      bind:value={q}
      disabled={busy}
    />

    <div class="mt-3 max-h-64 overflow-y-auto rounded border border-line">
      {#if searching && results.length === 0}
        <p class="px-3 py-6 text-center text-sm text-muted">Loading…</p>
      {:else if searchError && results.length === 0}
        <p role="alert" class="px-3 py-6 text-center text-sm text-danger">{searchError}</p>
      {:else if results.length === 0}
        <p class="px-3 py-6 text-center text-sm text-muted">
          {q.trim() ? 'No models match that.' : 'You have no other models yet.'}
        </p>
      {:else}
        <!-- Radios rather than a click-to-attach list: attaching is a write, and
             a list where the first click commits it has no step at which you can
             see what you picked. -->
        {#each results as candidate (candidate.id)}
          <label class="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm">
            <input
              type="radio"
              name="attach-candidate"
              value={candidate.id}
              checked={chosen === candidate.id}
              disabled={busy || searching}
              onchange={() => (chosen = candidate.id)}
            />
            <span class="min-w-0 flex-1 truncate">{candidate.name}</span>
            <span class="shrink-0 text-xs text-muted">
              {formatFileCount(candidate.fileCount)} · {formatDate(candidate.createdAt)}
            </span>
          </label>
        {/each}
        {#if searchError}
          <!-- A page failed to load. The rows above it are still good, so the
               refusal goes under them rather than in place of them. -->
          <p role="alert" class="border-t border-line px-3 py-2 text-sm text-danger">
            {searchError}
          </p>
        {/if}
        {#if truncated}
          <!-- Inside the scroll box, under the last row, because that is where
               you are when you run out of results. -->
          <button
            type="button"
            class="w-full border-t border-line px-3 py-2 text-sm text-muted"
            onclick={() => search(q.trim(), page + 1, true)}
            disabled={busy || searching || loadingMore}
          >
            {loadingMore ? 'Loading…' : `Show more (${results.length} of ${total})`}
          </button>
        {/if}
      {/if}
    </div>

    {#if error}
      <p role="alert" class="mt-4 text-sm text-danger">{error}</p>
    {/if}

    <div class="mt-5 flex justify-end gap-2">
      <button
        type="button"
        class="rounded border border-line-strong px-3 py-1.5 text-sm"
        onclick={oncancel}
        disabled={busy}
      >
        Cancel
      </button>
      <button
        type="submit"
        class="rounded bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink"
        disabled={busy || searching || chosen === undefined}
      >
        {busy ? 'Adding…' : 'Add version'}
      </button>
    </div>
  </form>
</Modal>
