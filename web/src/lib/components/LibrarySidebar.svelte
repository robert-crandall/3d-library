<script lang="ts">
  import LibraryError from './LibraryError.svelte';
  import { page } from '$app/state';
  import SignOutButton from '$lib/components/SignOutButton.svelte';
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';
  import { library } from '$lib/library.svelte';

  // The selection lives in the URL, not in a variable here. A filtered library
  // is a place: it survives a reload, it can be linked, and Back leaves it.
  // Keeping it in component state would make all three false and would also
  // need a second copy on the grid page, which reads the same parameters to
  // decide what to fetch.
  const params = $derived(page.url.searchParams);
  const onLibrary = $derived(page.url.pathname === '/');
  const categoryId = $derived(onLibrary ? params.get('categoryId') : null);
  const tagId = $derived(onLibrary ? params.get('tagId') : null);
  const uncategorized = $derived(onLibrary && params.get('uncategorized') === 'true');
  const unfiltered = $derived(onLibrary && !categoryId && !tagId && !uncategorized);

  /** A link that keeps the other axis. Clicking a tag while a category is
   *  selected narrows to both, which is what the server's AND does; clicking
   *  the selected one clears just that axis. */
  function href(key: 'categoryId' | 'tagId' | 'uncategorized', value: string) {
    const next = new URLSearchParams(onLibrary ? params : undefined);
    if (next.get(key) === value) next.delete(key);
    else next.set(key, value);
    // Uncategorized and a category are mutually exclusive: together they match
    // nothing, and offering the user an always-empty grid is not a filter.
    if (key === 'categoryId') next.delete('uncategorized');
    if (key === 'uncategorized') next.delete('categoryId');
    const query = next.toString();
    return query ? `/?${query}` : '/';
  }
</script>

<!--
  The signed-in sidebar: navigation, the category list, the tag list, and a way
  into Settings.

  Collections and the search box from screen 1a are absent - collections are a
  later milestone and search is milestone 8 - because a control that does
  nothing is worse than one that is not there yet.
-->
<aside class="flex w-62 shrink-0 flex-col border-r border-line bg-sidebar">
  <div class="flex items-center gap-2 px-5 py-5">
    <span
      class="grid h-7 w-7 place-items-center rounded bg-accent text-sm font-bold text-accent-ink"
      aria-hidden="true">3D</span
    >
    <span class="font-semibold">Library</span>
  </div>

  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-2 pb-4">
    <nav aria-label="Library">
      <h2 class="px-3 pb-1.5 text-xs font-medium tracking-wide text-faint uppercase">Library</h2>
      <a
        href="/"
        aria-current={unfiltered ? 'page' : undefined}
        class="flex items-center justify-between rounded px-3 py-1.5 text-sm"
        class:bg-selected={unfiltered}
        class:font-medium={unfiltered}
      >
        All models<span class="text-faint">{library.counts.models}</span>
      </a>
      <a
        href={href('uncategorized', 'true')}
        aria-current={uncategorized ? 'page' : undefined}
        class="flex items-center justify-between rounded px-3 py-1.5 text-sm"
        class:bg-selected={uncategorized}
        class:font-medium={uncategorized}
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
          {@const selected = categoryId === String(category.id)}
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
            {@const selected = tagId === String(tag.id)}
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
