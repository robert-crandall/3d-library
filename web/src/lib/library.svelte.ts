import { api } from '$lib/api/client';
import { apiErrorMessage } from '$lib/api/errors';
import type { paths } from '$lib/api/schema';

type Json<P extends keyof paths, M extends 'get'> = paths[P][M] extends {
  responses: { 200: { content: { 'application/json': infer T } } };
}
  ? T
  : never;

export type Category = NonNullable<Json<'/api/categories', 'get'>>[number];
export type Label = NonNullable<Json<'/api/tags', 'get'>>[number];
export type Counts = NonNullable<Json<'/api/library/counts', 'get'>>;

/**
 * The taxonomy, read once and shared.
 *
 * There is one of these for the whole app rather than one per page, because
 * the sidebar and the edit dialog show the same categories and they must not
 * disagree: renaming a category in Settings has to change the sidebar behind
 * the dialog, and a second copy would leave it stale until a reload.
 *
 * Everything is re-read from the server after a change rather than patched in
 * place. The counts move for reasons the client cannot compute - deleting a
 * category uncategorizes its models, assigning one moves two numbers at once -
 * so the only cheap way to be right is to ask. It is four small GETs.
 */
class Library {
  categories = $state<Category[]>([]);
  tags = $state<Label[]>([]);
  materials = $state<Label[]>([]);
  counts = $state<Counts>({ models: 0, uncategorized: 0 });
  /** Empty until something fails. The sidebar renders it instead of a stale
   *  list, because a sidebar that quietly shows yesterday's categories is
   *  worse than one that says it could not load them. */
  error = $state('');
  loaded = $state(false);

  /** Bumped by every refresh and every reset, so a response can tell whether
   *  it is still the answer to the question being asked. */
  #generation = 0;

  /**
   * Forget everything.
   *
   * This is a module singleton, so it outlives sign-out: `goto('/login')` is a
   * client-side navigation and nothing reloads the page. Without this, the next
   * person to sign in on the same tab would see the previous one's categories
   * until the first GET landed.
   */
  reset() {
    this.#generation++;
    this.categories = [];
    this.tags = [];
    this.materials = [];
    this.counts = { models: 0, uncategorized: 0 };
    this.error = '';
    this.loaded = false;
  }

  async refresh() {
    const generation = ++this.#generation;
    try {
      const [categories, tags, materials, counts] = await Promise.all([
        api.GET('/api/categories'),
        api.GET('/api/tags'),
        api.GET('/api/materials'),
        api.GET('/api/library/counts')
      ]);
      // Two refreshes can be in flight at once - delete a tag, then delete
      // another before the first reply arrives - and they can land in either
      // order. Only the newest one is still the truth.
      if (generation !== this.#generation) return;
      const failure =
        categories.error ?? tags.error ?? materials.error ?? counts.error;
      if (failure) {
        this.#fail(apiErrorMessage(failure, 'Could not load categories and tags.'));
        return;
      }
      this.categories = categories.data ?? [];
      this.tags = tags.data ?? [];
      this.materials = materials.data ?? [];
      this.counts = counts.data ?? { models: 0, uncategorized: 0 };
      this.error = '';
      this.loaded = true;
    } catch {
      if (generation !== this.#generation) return;
      this.#fail('Could not reach the server.');
    }
  }

  /**
   * Say what went wrong, and keep a list that was read successfully.
   *
   * The two failures are not the same. Nothing has been read yet: there is
   * nothing to keep, and the sidebar showing categories it never loaded would
   * be an invention. Something has: the lists were right one action ago, and
   * emptying them because a refresh blipped would take a working sidebar away
   * over a request the user did not make and cannot retry - the error stays on
   * screen either way, so nothing is being hidden by keeping them.
   */
  #fail(message: string) {
    if (!this.loaded) {
      this.categories = [];
      this.tags = [];
      this.materials = [];
      this.counts = { models: 0, uncategorized: 0 };
    }
    this.error = message;
  }
}

export const library = new Library();
