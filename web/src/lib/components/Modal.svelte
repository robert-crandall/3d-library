<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    title,
    ondismiss,
    children
  }: {
    /** Rendered as the dialog's heading and its accessible name. */
    title: string;
    /**
     * Escape, and only Escape. The overlay is deliberately not clickable: every
     * dialog in this app is either mid-upload or one confirm away from a
     * permanent delete, and a stray click outside is not consent to abandon
     * either. Pass a no-op to make the dialog undismissable while busy.
     */
    ondismiss: () => void;
    children: Snippet;
  } = $props();

  // The dialog claims aria-modal, so it has to behave like one: focus starts
  // inside it, Tab stays inside it, and Escape leaves. A native <dialog> would
  // give all three away for free, but jsdom does not implement showModal(), so
  // that version could not be tested. Everything focusable in these dialogs is
  // an <input>, a <textarea> or a <button>, so the query does not need to be
  // more clever than that.
  let box: HTMLElement | undefined = $state();

  $effect(() => {
    box?.querySelector<HTMLElement>('input, textarea, button')?.focus();
  });

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      ondismiss();
      return;
    }
    if (event.key !== 'Tab' || !box) return;

    const stops = [
      ...box.querySelectorAll<HTMLInputElement>('input, textarea, button, a[href]')
    ].filter((el) => !el.disabled);
    const edge = event.shiftKey ? stops[0] : stops[stops.length - 1];
    if (!edge || document.activeElement !== edge) return;
    event.preventDefault();
    (event.shiftKey ? stops[stops.length - 1] : stops[0]).focus();
  }

  // Unique per instance so two dialogs on one page cannot claim the same
  // aria-labelledby target.
  const titleId = $props.id();
</script>

<svelte:window onkeydown={keydown} />

<!--
  Extracted from UploadDialog once this milestone needed three of these. The
  overlay, the role, the initial focus and the Tab trap were the same in all
  three; only the contents differ, so the contents are the snippet.
-->
<div class="fixed inset-0 grid place-items-center bg-black/40 p-4">
  <div
    bind:this={box}
    class="w-full max-w-md rounded-tile border border-line bg-surface p-5"
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleId}
  >
    <h2 id={titleId} class="text-lg font-semibold">{title}</h2>
    {@render children()}
  </div>
</div>
