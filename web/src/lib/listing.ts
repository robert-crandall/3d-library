// The library's view state: which filters, which search, which ordering, which
// page. It lives entirely in the URL, so reload, Back, and opening a model and
// coming back all restore it without a line of code, and a link to a search is
// a link other people can follow.
//
// The URL and the API use the same parameter names on purpose, so one
// serializer writes both and there is no second place for them to disagree.

export const SORTS = ['newest', 'oldest', 'name', 'name-desc'] as const;
export type Sort = (typeof SORTS)[number];

export const PAGE_SIZE_FALLBACK = 24;
const MAX_QUERY = 100;

export type View = {
	q: string;
	sort: Sort;
	page: number;
	categoryId: string | null;
	tagId: string | null;
	uncategorized: boolean;
};

export const EMPTY_VIEW: View = {
	q: '',
	sort: 'newest',
	page: 1,
	categoryId: null,
	tagId: null,
	uncategorized: false
};

/**
 * Reads a View out of a URL's query string, normalising rather than trusting
 * it. A hand-mangled or stale URL renders the library instead of a 422, because
 * every value is either recognised or replaced with its default here, and the
 * client never forwards garbage the server would then have to reject.
 */
export function parseView(params: URLSearchParams): View {
	const sort = params.get('sort');
	const page = params.get('page');
	return {
		// Spread rather than slice: the server counts characters, and slicing a
		// JS string counts UTF-16 units, so an emoji at the boundary would be
		// cut in half into a lone surrogate.
		q: [...(params.get('q') ?? '').trim()].slice(0, MAX_QUERY).join(''),
		sort: SORTS.includes(sort as Sort) ? (sort as Sort) : 'newest',
		// A page has to be a positive integer. /^\d+$/ rejects '1.5', '-1',
		// '1e3' and ' 1', all of which Number() would happily accept, and the
		// safe-integer test rejects the 400-digit one that becomes Infinity.
		page: page && /^\d+$/.test(page) && Number(page) >= 1 && Number.isSafeInteger(Number(page))
			? Number(page)
			: 1,
		categoryId: id(params.get('categoryId')),
		tagId: id(params.get('tagId')),
		uncategorized: params.get('uncategorized') === 'true'
	};
}

// Kept as a string, because it is only ever put back into a URL. The
// safe-integer test is what stops a 40-digit id being forwarded to a server
// that parses it as an int64 and answers 422.
function id(raw: string | null): string | null {
	return raw && /^[1-9]\d*$/.test(raw) && Number.isSafeInteger(Number(raw)) ? raw : null;
}

/**
 * Serializes a View into a query string, omitting every default so the
 * unfiltered library is `/` and a shared link carries only what was chosen.
 */
function search(view: View): string {
	const params = new URLSearchParams();
	if (view.categoryId) params.set('categoryId', view.categoryId);
	if (view.tagId) params.set('tagId', view.tagId);
	if (view.uncategorized) params.set('uncategorized', 'true');
	if (view.q) params.set('q', view.q);
	if (view.sort !== 'newest') params.set('sort', view.sort);
	if (view.page > 1) params.set('page', String(view.page));
	return params.toString();
}

/** The library page's address for this view. */
export function viewHref(view: View): string {
	const qs = search(view);
	return qs ? `/?${qs}` : '/';
}

/** The list request for this view. */
export function modelsQuery(view: View): string {
	const qs = search(view);
	return qs ? `/api/models?${qs}` : '/api/models';
}

/**
 * A view with a filter changed. Every filter change drops the page, because
 * page 3 of a search rarely exists after you narrow it to one category, and
 * being dropped onto an empty grid is a worse answer than the first page. The
 * search term and the ordering survive: you narrow a search by category, you do
 * not start over.
 */
export function withFilter(view: View, change: Partial<View>): View {
	return { ...view, page: 1, ...change };
}

/** Clears every filter axis, keeping the search and the ordering. */
export function withoutFilters(view: View): View {
	return withFilter(view, { categoryId: null, tagId: null, uncategorized: false });
}

/** How many pages a result set of this size fills. Never zero. */
export function pageCount(total: number, pageSize: number): number {
	return Math.max(1, Math.ceil(total / Math.max(1, pageSize)));
}
