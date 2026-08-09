<script lang="ts">
  import ModelTile from '$lib/components/ModelTile.svelte';
  import UploadDialog from '$lib/components/UploadDialog.svelte';
  import { api } from '$lib/api/client';
  import { apiErrorMessage } from '$lib/api/errors';
  import { library } from '$lib/library.svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import {
    SORTS,
    PAGE_SIZE_FALLBACK,
    modelsQuery,
    pageCount,
    parseView,
    viewHref,
    withFilter,
    withoutFilters,
    type Sort
  } from '$lib/listing';
  import type { Model } from '$lib/upload';

  let models = $state<Model[]>([]);
  // Three states, not a boolean and a string. "We asked and you have nothing"
  // and "we could not ask" must not render the same, and with only `loading`
  // plus `error` a failed load falls through to the empty state - which tells
  // the user their library is empty when nobody knows whether it is.
  let status = $state<'loading' | 'ready' | 'failed'>('loading');
  let error = $state('');
  let uploading = $state(false);
  // What the last successful response said about itself. The count line and the
  // pager read these, never the URL: ask for ?page=99 of a two-page library and
  // the server serves page 2, so links built from the URL would offer 98 and
  // 100 and the count line would claim a page that does not exist.
  let total = $state(0);
  let servedPage = $state(1);
  let pageSize = $state(PAGE_SIZE_FALLBACK);

  // The view is the URL's, so it is the sidebar's and this page's at once
  // without either telling the other. Deriving the fetch from it means Back, a
  // reload and a shared link all work with no extra code.
  const view = $derived(parseView(page.url.searchParams));
  const request = $derived(modelsQuery(view));

  const category = $derived(library.categories.find((c) => String(c.id) === view.categoryId));
  const tag = $derived(library.tags.find((t) => String(t.id) === view.tagId));
  const filtered = $derived(Boolean(view.categoryId || view.tagId || view.uncategorized));
  const heading = $derived(
    category?.name ?? (view.uncategorized ? 'Uncategorized' : (tag ? `#${tag.name}` : 'All models'))
  );

  const pages = $derived(pageCount(total, pageSize));
  const countLine = $derived(
    `${total} ${total === 1 ? 'model' : 'models'}` +
      (pages > 1 ? ` · page ${servedPage} of ${pages}` : '')
  );

  const SORT_LABELS: Record<Sort, string> = {
    newest: 'Recently added',
    oldest: 'Oldest first',
    name: 'Name A–Z',
    'name-desc': 'Name Z–A'
  };

  // You can only add to a library you have actually seen. Uploading over a
  // library that failed to load, or one still arriving, means prepending the
  // new model to a list that is wrong or about to be replaced - and then either
  // the upload disappears when the load lands, or the load is thrown away and
  // everything already in the library disappears instead. Waiting costs one
  // fast GET and removes both.

  // Which load is the current one. Typing in the search box starts a GET per
  // pause and clicking two categories quickly starts two, and the second can
  // answer first: without this the grid would end up showing the first
  // response's models under the second's heading, because the heading comes
  // from the URL and the models come from whichever reply landed last. A
  // counter rather than an AbortController because the stale reply is harmless
  // once ignored, and this is four lines.
  let generation = 0;

  async function load(url: string) {
    const mine = ++generation;
    // Back to loading for the duration, not just on the first call. Without
    // this a reload keeps the page in `ready` while the GET is in flight, which
    // leaves Upload enabled over a library that is being re-read precisely
    // because nobody knows what is in it.
    status = 'loading';
    error = '';
    try {
      // A serialized view rather than openapi-fetch's `params.query`, because
      // the URL and the API take the same parameters and one serializer means
      // there is no second place for them to disagree.
      const { data, error: failure } = await api.GET(url as '/api/models');
      if (mine !== generation) return;
      if (failure) {
        error = apiErrorMessage(failure, 'Could not load the library.');
        status = 'failed';
        return;
      }
      models = data?.items ?? [];
      total = data?.total ?? 0;
      servedPage = data?.page ?? 1;
      pageSize = data?.pageSize ?? PAGE_SIZE_FALLBACK;
      status = 'ready';
    } catch {
      if (mine !== generation) return;
      // openapi-fetch lets a fetch-level rejection through, so without this the
      // page would sit on "Loading…" forever.
      error = 'Could not reach the server.';
      status = 'failed';
    }
  }

  // Re-runs whenever the URL changes, which is the whole of filtering, sorting,
  // searching and paging: every one of them is a navigation, and this notices.
  $effect(() => {
    load(request);
  });

  function sorted(event: Event) {
    const next = (event.currentTarget as HTMLSelectElement).value as Sort;
    goto(viewHref(withFilter(view, { sort: next })), { noScroll: true });
  }

  // Re-read rather than prepend. Prepending is right for an unfiltered library,
  // but under a filter the new model may not belong in the list at all - it has
  // no category and no tags yet - and putting it there would show the user a
  // grid that disagrees with its own heading.
  function uploaded(_model: Model, opts?: { keepOpen?: boolean }) {
    load(request);
    library.refresh();
    // A partial upload keeps the dialog open to say what is missing.
    if (!opts?.keepOpen) uploading = false;
  }
</script>


<div class="px-8 py-7">
  <header class="flex items-start justify-between gap-4">
    <div class="min-w-0">
      <p class="text-xs font-medium tracking-wide text-faint uppercase">Library</p>
      <h1 class="mt-1 flex items-center gap-2 text-2xl font-semibold">
        {#if category}
          <span
            class="h-2.5 w-2.5 shrink-0 rounded-xs"
            style="background-color: {category.color}"
            aria-hidden="true"
          ></span>
        {/if}
        <span class="truncate">{heading}</span>
      </h1>
      {#if filtered}
        <a class="mt-1 inline-block text-sm text-muted underline" href={viewHref(withoutFilters(view))}
          >Clear filter</a
        >
      {/if}
    </div>
    <div class="flex shrink-0 items-center gap-3">
      <label class="flex items-center gap-2 text-sm text-muted">
        <span>Sort</span>
        <select
          value={view.sort}
          onchange={sorted}
          class="rounded border border-line px-2 py-1.5 text-sm"
        >
          {#each SORTS as option (option)}
            <option value={option}>{SORT_LABELS[option]}</option>
          {/each}
        </select>
      </label>
      <button
        type="button"
        class="rounded bg-accent px-4 py-2 text-sm font-medium text-accent-ink disabled:opacity-50"
        onclick={() => (uploading = true)}
        disabled={status !== 'ready'}
      >
        Upload
      </button>
    </div>
  </header>

  {#if status === 'ready'}
    <!-- The count is of matches, not of tiles on screen: it is what tells you a
         search narrowed anything when the first page looks full either way. -->
    <p class="mt-3 text-right text-sm text-muted">{countLine}</p>
  {/if}

  {#if error}
    <p role="alert" class="mt-6 text-sm text-danger">{error}</p>
  {/if}

  {#if status === 'loading'}
    <p class="mt-8 text-sm text-muted">Loading…</p>
  {:else if status === 'failed'}
    <button
      type="button"
      class="mt-6 rounded border border-line-strong px-3 py-1.5 text-sm"
      onclick={() => load(request)}
    >
      Try again
    </button>
  {:else if models.length === 0 && view.q}
    <!--
      No results for a search is its own state. It names the term, because after
      a debounce you may not trust that the box holds what was searched, and it
      offers a way out that keeps the filters rather than dumping you at the
      whole library.
    -->
    <div class="mt-8 rounded-tile border border-dashed border-line-strong px-6 py-16 text-center">
      <h2 class="text-base font-medium">No models match “{view.q}”</h2>
      <p class="mx-auto mt-2 max-w-sm text-sm text-muted">
        Search looks at model names and descriptions. Try a shorter word.
      </p>
      <a
        class="mt-6 inline-block rounded border border-line-strong px-3 py-1.5 text-sm"
        href={viewHref(withFilter(view, { q: '' }))}
      >
        Clear search
      </a>
    </div>
  {:else if models.length === 0 && filtered}
    <!--
      A filter that matches nothing is not an empty library, and offering
      "Upload your first model" here would be wrong twice: the library is not
      empty, and an upload would not show up under this filter anyway.
    -->
    <div class="mt-8 rounded-tile border border-dashed border-line-strong px-6 py-16 text-center">
      <h2 class="text-base font-medium">Nothing matches this filter</h2>
      <p class="mx-auto mt-2 max-w-sm text-sm text-muted">
        No models are filed here yet. Open a model and use Edit to give it a category or a tag.
      </p>
      <a
        class="mt-6 inline-block rounded border border-line-strong px-3 py-1.5 text-sm"
        href={viewHref(withoutFilters(view))}
      >
        Show all models
      </a>
    </div>
  {:else if models.length === 0}
    <!--
      The empty state is a real screen, not an absence. It is the first thing a
      new user sees, and a blank area under a header reads as a failed load.
    -->
    <div class="mt-8 rounded-tile border border-dashed border-line-strong px-6 py-16 text-center">
      <h2 class="text-base font-medium">Nothing here yet</h2>
      <p class="mx-auto mt-2 max-w-sm text-sm text-muted">
        Upload an STL, a 3MF, a G-code file or a photo, and it shows up here as a model.
      </p>
      <button
        type="button"
        class="mt-6 rounded bg-accent px-4 py-2 text-sm font-medium text-accent-ink"
        onclick={() => (uploading = true)}
      >
        Upload your first model
      </button>
    </div>
  {:else}
    <ul class="mt-7 grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {#each models as model (model.id)}
        <li><ModelTile {model} /></li>
      {/each}
    </ul>
    {#if pages > 1}
      <!-- Real links, so a page is a place: middle-click opens it in a tab and
           Back leaves it. Both sides are always shown so the pair does not move
           under the cursor between pages; the unavailable one is text. -->
      <nav aria-label="Pagination" class="mt-8 flex items-center justify-center gap-6 text-sm">
        {#if servedPage > 1}
          <a href={viewHref({ ...view, page: servedPage - 1 })} rel="prev">Previous</a>
        {:else}
          <span class="text-faint">Previous</span>
        {/if}
        {#if servedPage < pages}
          <a href={viewHref({ ...view, page: servedPage + 1 })} rel="next">Next</a>
        {:else}
          <span class="text-faint">Next</span>
        {/if}
      </nav>
    {/if}
  {/if}
</div>

{#if uploading}
  <UploadDialog
    onclose={(opts) => {
      uploading = false;
      // The dialog could not tell whether the upload landed. Re-reading the
      // library is what settles it, and it has to happen before Upload is
      // available again or the user can make a second copy they then have to
      // find and delete by hand.
      if (opts?.reload) {
        load(request);
        library.refresh();
      }
    }}
    onuploaded={uploaded}
  />
{/if}
