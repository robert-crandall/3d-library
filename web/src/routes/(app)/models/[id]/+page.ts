import { error } from '@sveltejs/kit';

/**
 * Turn the URL segment into a model id, or 404 before the page renders.
 *
 * The check is here rather than in the page because `/models/nonsense` is not a
 * model that failed to load, it is not a model. Letting it through would send
 * the API a request that can only 422 and then show a retry button for
 * something no amount of retrying fixes.
 *
 * The API's ids are bigint, which does not fit a JS number - but Postgres
 * bigserial starts at 1 and this is a personal library, so a real id will not
 * reach 2^53 in any timeline. Number is fine and the parse below is the only
 * thing standing between the URL and it.
 */
export function load({ params }: { params: { id: string } }) {
  if (!/^[1-9][0-9]*$/.test(params.id)) error(404, 'No such model.');
  return { id: Number(params.id) };
}
