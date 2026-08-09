import { describe, expect, it } from 'vitest';
import {
  EMPTY_VIEW,
  modelsQuery,
  pageCount,
  parseView,
  viewHref,
  withFilter,
  withoutFilters,
  type View
} from './listing';

function parse(search: string): View {
  return parseView(new URL(`http://localhost/${search}`).searchParams);
}

describe('parseView', () => {
  it('reads every parameter the library page understands', () => {
    expect(parse('?categoryId=3&tagId=8&uncategorized=true&q=bin&sort=name&page=2')).toEqual({
      categoryId: '3',
      tagId: '8',
      collectionId: null,
      uncategorized: true,
      q: 'bin',
      sort: 'name',
      page: 2
    });
  });

  it('is the empty view when the URL is bare', () => {
    expect(parse('')).toEqual(EMPTY_VIEW);
  });

  // A URL can be hand-edited, bookmarked from an older version, or truncated by
  // a chat client. Every one of these would be a 422 from the server if the
  // client forwarded it, and a 422 on the library page is a blank screen with
  // an error where a library should be.
  it('replaces anything it does not recognise with the default', () => {
    expect(parse('?sort=bogus').sort).toBe('newest');
    expect(parse('?page=0').page).toBe(1);
    expect(parse('?page=-1').page).toBe(1);
    expect(parse('?page=abc').page).toBe(1);
    expect(parse('?page=1.5').page).toBe(1);
    expect(parse('?page= 2').page).toBe(1);
    expect(parse('?categoryId=abc').categoryId).toBeNull();
    expect(parse('?categoryId=0').categoryId).toBeNull();
    expect(parse('?tagId=-4').tagId).toBeNull();
    expect(parse('?uncategorized=1').uncategorized).toBe(false);
  });

  it('treats a whitespace-only search as no search', () => {
    expect(parse('?q=%20%20').q).toBe('');
  });

  it('truncates a search longer than the server accepts', () => {
    expect(parse(`?q=${'x'.repeat(200)}`).q).toHaveLength(100);
  });

  // The server counts characters and this counts UTF-16 units, so a naive
  // slice would cut the emoji at the boundary into a lone surrogate - which is
  // not text, and which the server would answer 422 or worse to.
  it('truncates on a character boundary', () => {
    const q = parse(`?q=${encodeURIComponent('a'.repeat(99) + '🧵🧵')}`).q;
    expect([...q]).toHaveLength(100);
    expect(q.endsWith('🧵')).toBe(true);
  });

  // A number long enough to become Infinity is still all digits. Forwarded, it
  // is the 422 that parseView exists to prevent.
  it('refuses a number too large to be one', () => {
    expect(parse(`?page=${'9'.repeat(400)}`).page).toBe(1);
    expect(parse(`?categoryId=${'9'.repeat(40)}`).categoryId).toBeNull();
  });

  it('ignores parameters it does not know', () => {
    expect(parse('?nonsense=1&q=bin').q).toBe('bin');
    expect(viewHref(parse('?nonsense=1&q=bin'))).toBe('/?q=bin');
  });
});

describe('viewHref and modelsQuery', () => {
  // A link people share should carry what was chosen and nothing else. Writing
  // the defaults out would make the unfiltered library `/?sort=newest&page=1`,
  // and then two URLs for one screen.
  it('omits every default', () => {
    expect(viewHref(EMPTY_VIEW)).toBe('/');
    expect(modelsQuery(EMPTY_VIEW)).toBe('/api/models');
    expect(viewHref({ ...EMPTY_VIEW, sort: 'newest', page: 1 })).toBe('/');
  });

  it('round-trips through the URL', () => {
    const view: View = {
      categoryId: '3',
      tagId: '8',
      collectionId: null,
      uncategorized: false,
      q: 'bin',
      sort: 'name-desc',
      page: 4
    };
    expect(parse(viewHref(view).slice(1))).toEqual(view);
  });

  // The two serializers have to agree, because the page fetches what the URL
  // says. If they could differ, Back would show a grid that does not match its
  // own address.
  it('sends the API the same parameters the URL carries', () => {
    const view = { ...EMPTY_VIEW, q: 'bin', sort: 'name' as const, page: 3 };
    expect(viewHref(view)).toBe('/?q=bin&sort=name&page=3');
    expect(modelsQuery(view)).toBe('/api/models?q=bin&sort=name&page=3');
  });
});

describe('withFilter', () => {
  const view: View = {
    categoryId: '3',
    tagId: '8',
    collectionId: null,
    uncategorized: false,
    q: 'bin',
    sort: 'name',
    page: 5
  };

  // Narrowing a search by category is one action, not a reason to start over.
  it('keeps the search and the ordering', () => {
    const next = withFilter(view, { categoryId: '4' });
    expect(next.q).toBe('bin');
    expect(next.sort).toBe('name');
  });

  // Page 5 of a search rarely exists once you narrow it, and landing on an
  // empty grid is a worse answer than the first page.
  it('drops the page', () => {
    expect(withFilter(view, { categoryId: '4' }).page).toBe(1);
    expect(withoutFilters(view).page).toBe(1);
  });

  it('clears every filter axis at once but nothing else', () => {
    expect(withoutFilters({ ...view, uncategorized: true })).toEqual({
      categoryId: null,
      tagId: null,
      collectionId: null,
      uncategorized: false,
      q: 'bin',
      sort: 'name',
      page: 1
    });
  });
});

describe('pageCount', () => {
  it('is never zero, so an empty result is page 1 of 1 and not page 1 of 0', () => {
    expect(pageCount(0, 24)).toBe(1);
  });

  it('counts a partial last page', () => {
    expect(pageCount(24, 24)).toBe(1);
    expect(pageCount(25, 24)).toBe(2);
    expect(pageCount(48, 24)).toBe(2);
  });
});

// A collection is the newest filter axis and the only one the server can refuse,
// so it is worth checking it survives the round trip like the others.
describe('collectionId', () => {
  it('reads, writes and refuses the same values as the other ids', () => {
    expect(parseView(new URLSearchParams('collectionId=12')).collectionId).toBe('12');
    // Same guard as the others: a 40-digit id would be forwarded to a server
    // that parses it as an int64 and answers 422.
    expect(parseView(new URLSearchParams('collectionId=0')).collectionId).toBeNull();
    expect(parseView(new URLSearchParams('collectionId=1e3')).collectionId).toBeNull();
    expect(
      parseView(new URLSearchParams('collectionId=99999999999999999999')).collectionId
    ).toBeNull();

    const view = parseView(new URLSearchParams('collectionId=12&categoryId=3&q=box'));
    // The URL and the request use the same names, so one serializer writes both.
    expect(viewHref(view)).toBe('/?categoryId=3&collectionId=12&q=box');
    expect(modelsQuery(view)).toBe('/api/models?categoryId=3&collectionId=12&q=box');
  });
});
