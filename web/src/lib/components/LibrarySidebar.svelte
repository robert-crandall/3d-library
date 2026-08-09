<script lang="ts">
  import LibraryError from './LibraryError.svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import SignOutButton from '$lib/components/SignOutButton.svelte';
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';
  import { library } from '$lib/library.svelte';
  import { EMPTY_VIEW, parseView, viewHref, withFilter, withoutFilters } from '$lib/listing';

  // The selection lives in the URL, not in a variable here. A filtered library
  // is a place: it survives a reload, it can be linked, and Back leaves it.
  // Keeping it in component state would make all three false and would also
  // need a second copy on the grid page, which reads the same parameters to
  // decide what to fetch.
  const onLibrary = $derived(page.url.pathname === '/');
  const view = $derived(onLibrary ? parseView(page.url.searchParams) : EMPTY_VIEW);
  const unfiltered = $derived(
    onLibrary && !view.categoryId && !view.tagId && !view.uncategorized
  );

  /** A link that keeps the other axis, and keeps the search and the ordering.
   *  Clicking a tag while a category is selected narrows to both, which is what
   *  the server's AND does; clicking the selected one clears just that axis. */
  function href(key: 'categoryId' | 'tagId' | 'uncategorized', value: string) {
    if (key === 'uncategorized') {
      // Uncategorized and a category are mutually exclusive: together they
      // match nothing, and offering the user an always-empty grid is not a
      // filter.
      return viewHref(
        withFilter(view, { uncategorized: !view.uncategorized, categoryId: null })
      );
    }
    const cleared = view[key] === value;
    return viewHref(
      withFilter(view, {
        [key]: cleared ? null : value,
        ...(key === 'categoryId' ? { uncategorized: false } : {})
      })
    );
  }

  // The box is local state so typing is not gated on a round trip, and the URL
  // is only written after a pause. One navigation per pause, not per keystroke.
  let draft = $state('');
  let timer: ReturnType<typeof setTimeout> | undefined;
  const DEBOUNCE_MS = 250;

  $effect(() => {
    // Two things are load-bearing here. It reads the URL, so it re-runs on
    // every navigation - including one where the term does not change - and the
    // teardown then cancels a pending search: this sidebar lives in the layout
    // and does not unmount when you open a model, so a search typed 100ms
    // before clicking a tile would otherwise navigate you back to the library a
    // moment after you got there.
    //
    // And it never reads `draft`, so a keystroke cannot cancel its own timer.
    const here = page.url.pathname === '/';
    draft = here ? parseView(page.url.searchParams).q : '';
    return () => clearTimeout(timer);
  });

  function search() {
    clearTimeout(timer);
    timer = setTimeout(() => {
      const q = draft.trim();
      if (q === view.q && onLibrary) return;
      // replaceState because one history entry per pause makes Back walk
      // backwards through a half-typed word instead of leaving the search.
      goto(viewHref(withFilter(view, { q })), {
        replaceState: true,
        keepFocus: true,
        noScroll: true
      });
    }, DEBOUNCE_MS);
  }
</script>

<!--
  The signed-in sidebar: search, navigation, the category list, the tag list,
  and a way into Settings.

  Collections from screen 1a are absent - they are a later milestone - because a
  control that does nothing is worse than one that is not there yet.
-->
<aside class="flex w-62 shrink-0 flex-col border-r border-line bg-sidebar">
  <div class="flex items-center gap-2 px-5 py-5">
    <span
      class="grid h-7 w-7 place-items-center rounded bg-accent text-sm font-bold text-accent-ink"
      aria-hidden="true">3D</span
    >
    <span class="font-semibold">Library</span>
  </div>

  <div class="px-5 pb-4">
    <input
      type="search"
      aria-label="Search models"
      placeholder="Search models"
      maxlength="100"
      bind:value={draft}
      oninput={search}
      class="w-full rounded border border-line px-2.5 py-1.5 text-sm"
    />
  </div>

  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-2 pb-4">
    <nav aria-label="Library">
      <h2 class="px-3 pb-1.5 text-xs font-medium tracking-wide text-faint uppercase">Library</h2>
      <a
        href={viewHref(withoutFilters(view))}
        aria-current={unfiltered ? 'page' : undefined}
        class="flex items-center justify-between rounded px-3 py-1.5 text-sm"
        class:bg-selected={unfiltered}
        class:font-medium={unfiltered}
      >
        All models<span class="text-faint">{library.counts.models}</span>
      </a>
      <a
        href={href('uncategorized', 'true')}
        aria-current={view.uncategorized ? 'page' : undefined}
        class="flex items-center justify-between rounded px-3 py-1.5 text-sm"
        class:bg-selected={view.uncategorized}
        class:font-medium={view.uncategorized}
      >
        Uncategorized<span class="text-faint">{library.counts.uncategorized}</span>
      </a>
    </nav>

    <!-- Settings shows this failure itself, right above the lists it makes
         wrong, and two copies of one alert is two announcements and two
         identical Try again buttons. The screen the user is acting on wins. -->
    {#if page.url.pathname !== '/settings'}
      <LibraryError class="px-3" />
    {/if}

    {#if library.categories.length > 0}
      <nav aria-label="Categories">
        <h2 class="px-3 pb-1.5 text-xs font-medium tracking-wide text-faint uppercase">
          Categories
        </h2>
        {#each library.categories as category (category.id)}
          {@const selected = view.categoryId === String(category.id)}
          <a
            href={href('categoryId', String(category.id))}
            aria-current={selected ? 'page' : undefined}
            class="flex items-center gap-2 rounded px-3 py-1.5 text-sm"
            class:bg-selected={selected}
            class:font-medium={selected}
          >
            <span
              class="h-2 w-2 shrink-0 rounded-xs"
              style="background-color: {category.color}"
              aria-hidden="true"
            ></span>
            <span class="truncate">{category.name}</span>
            <span class="ml-auto shrink-0 text-faint">{category.modelCount}</span>
          </a>
        {/each}
      </nav>
    {/if}

    {#if library.tags.length > 0}
      <nav aria-label="Tags">
        <h2 class="px-3 pb-1.5 text-xs font-medium tracking-wide text-faint uppercase">Tags</h2>
        <div class="flex flex-wrap gap-1.5 px-3">
          {#each library.tags as tag (tag.id)}
            {@const selected = view.tagId === String(tag.id)}
            <a
              href={href('tagId', String(tag.id))}
              aria-current={selected ? 'page' : undefined}
              class="rounded px-2 py-0.5 text-xs"
              class:bg-selected={selected}
              class:font-medium={selected}
              class:bg-chip={!selected}
              class:text-muted={!selected}
            >
              {tag.name}
            </a>
          {/each}
        </div>
      </nav>
    {/if}
  </div>

  <div class="flex flex-col gap-3 border-t border-line px-5 py-4">
    <a
      href="/settings"
      aria-current={page.url.pathname === '/settings' ? 'page' : undefined}
      class="text-sm">Settings</a
    >
    <ThemeToggle />
    <SignOutButton />
  </div>
</aside>
