<script lang="ts">
  import { formatDate } from '$lib/format';
  import type { FamilyMember } from '$lib/upload';

  let {
    family,
    currentId,
    mutating = false,
    ondetach
  }: {
    /** The root first, then its versions newest first - the order the server
     *  sends, which is the order design 1c draws. Always includes the model the
     *  page is showing. */
    family: FamilyMember[];
    currentId: number;
    mutating?: boolean;
    ondetach: (member: FamilyMember) => void;
  } = $props();

  // The root is the first entry, by the server's ordering. Derived rather than
  // passed, so the panel cannot be handed a family and a rootId that disagree.
  const rootId = $derived(family[0]?.id);
</script>

<!--
  Design 1c's Versions panel: the root on a line of its own with a dot, its
  versions indented under a rule, each with its note and its date.

  The panel is rendered only when there is a family to show - the page decides
  that, because a model with no versions shows no panel at all. Two markers do
  two different jobs: the dot says which entry is the root, and the current
  entry is the one that is not a link, because you are already on it.
-->
<section class="rounded-tile border border-line bg-surface">
  <h2 class="border-b border-line px-4 py-3 text-sm font-semibold">Versions</h2>
  <ul class="px-4 py-2">
    {#each family as member (member.id)}
      {@const current = member.id === currentId}
      {@const root = member.id === rootId}
      <li
        class="flex items-center gap-2 py-1.5 text-sm"
        class:ml-1={!root}
        class:border-l={!root}
        class:border-line={!root}
        class:pl-4={!root}
      >
        {#if root}
          <span class="size-2 shrink-0 rounded-full bg-accent" aria-hidden="true"></span>
        {/if}

        <span class="min-w-0 flex-1 truncate">
          {#if current}
            <!-- Not a link: this is the page you are on. aria-current is what
                 says so to a screen reader, where the weight only says it to a
                 sighted one. -->
            <span class="font-semibold" aria-current="page">{member.name}</span>
          {:else}
            <a href="/models/{member.id}">{member.name}</a>
          {/if}
          {#if member.description}
            <span class="text-muted"> — {member.description}</span>
          {/if}
        </span>

        <span class="shrink-0 text-xs text-muted">{formatDate(member.createdAt)}</span>

        {#if !root}
          <!-- On every version, not just the current one, so the whole family
               can be taken apart from whichever page you happen to be on. The
               root has nothing to detach from. -->
          <button
            type="button"
            class="shrink-0 text-xs text-muted underline"
            disabled={mutating}
            onclick={() => ondetach(member)}
          >
            Detach
          </button>
        {/if}
      </li>
    {/each}
  </ul>
</section>
