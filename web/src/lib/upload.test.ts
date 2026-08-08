import { afterEach, describe, expect, it, vi } from 'vitest';
import { MAX_FILE_BYTES, uploadModel } from './upload';

function ok(body: unknown) {
  return { ok: true, status: 201, json: async () => body } as Response;
}

afterEach(() => vi.unstubAllGlobals());

describe('uploadModel', () => {
  it('creates the model with the first file and adds the rest', async () => {
    const calls: Array<{ url: string; name: string }> = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const body = init?.body as FormData | undefined;
        const file = body?.get('file') as File | undefined;
        calls.push({ url, name: file?.name ?? '' });

        // No init means the trailing GET that re-reads the finished model.
        if (!init) return ok({ id: 7, name: 'Benchy', fileCount: 3, totalSize: 30 });
        if (url.startsWith('/api/models?')) return ok({ id: 7, name: 'Benchy', fileCount: 1 });
        return ok({ id: 99 });
      })
    );

    const states: string[] = [];
    const { model, failed } = await uploadModel(
      'Benchy',
      [file('a.stl'), file('b.stl'), file('c.stl')],
      (index, state) => states.push(`${index}:${state}`)
    );

    expect(calls.map((c) => c.url)).toEqual([
      '/api/models?name=Benchy',
      '/api/models/7/files',
      '/api/models/7/files',
      '/api/models/7'
    ]);
    expect(calls.slice(0, 3).map((c) => c.name)).toEqual(['a.stl', 'b.stl', 'c.stl']);
    expect(states).toEqual([
      '0:uploading',
      '0:done',
      '1:uploading',
      '1:done',
      '2:uploading',
      '2:done'
    ]);
    // The re-read is what gives the grid the real counts; the create response
    // only ever knows about its own file.
    expect(model.fileCount).toBe(3);
    expect(failed).toEqual([]);
  });

  it('encodes the name so a slash or an ampersand cannot break the URL', async () => {
    const seen: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        seen.push(url);
        return ok({ id: 1, name: 'x' });
      })
    );

    await uploadModel('Gears & bolts/v2', [file('a.stl')], () => {});

    expect(seen[0]).toBe('/api/models?name=Gears%20%26%20bolts%2Fv2');
  });

  // The failure that matters most: the model already exists by then. Throwing
  // it away would leave a row the user can neither see nor delete, and pressing
  // Upload again would make a second copy of it.
  it('returns the model when a later file fails, and keeps going', async () => {
    let call = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (!init) return ok({ id: 3, name: 'Half', fileCount: 2 });
        call += 1;
        if (call === 1) return ok({ id: 3, name: 'Half', fileCount: 1 });
        if (call === 2) {
          return {
            ok: false,
            status: 422,
            json: async () => ({ detail: 'library: invalid upload: bad name' })
          } as Response;
        }
        return ok({ id: 99 });
      })
    );

    const states: Array<[number, string, string | undefined]> = [];
    const { model, failed } = await uploadModel(
      'Half',
      [file('a.stl'), file('b.stl'), file('c.stl')],
      (i, s, e) => states.push([i, s, e])
    );

    expect(model.id).toBe(3);
    expect(failed).toEqual(['b.stl']);
    // One bad file says nothing about the next one, and this milestone has no
    // way to add it later, so c.stl still gets its turn.
    expect(states).toContainEqual([2, 'done', undefined]);
    expect(states).toContainEqual([1, 'failed', 'library: invalid upload: bad name']);
  });

  // The mirror image: nothing was created, so there is nothing to report and
  // retrying is the right thing for the caller to offer.
  it('throws when the very first file fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 422,
        json: async () => ({ detail: 'library: invalid upload: bad name' })
      }) as Response)
    );

    await expect(uploadModel('Nope', [file('a.stl'), file('b.stl')], () => {})).rejects.toThrow(
      'library: invalid upload: bad name'
    );
  });

  it('translates a 413 into something about the size limit', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: false, status: 413, json: async () => ({}) }) as Response)
    );

    await expect(uploadModel('Huge', [file('a.stl')], () => {})).rejects.toThrow(
      'over the 500 MB limit'
    );
  });

  it('survives a body that is not a problem document', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          ({
            ok: false,
            status: 502,
            json: async () => {
              throw new Error('not json');
            }
          }) as unknown as Response
      )
    );

    await expect(uploadModel('Gateway', [file('a.stl')], () => {})).rejects.toThrow(
      'Upload failed (502).'
    );
  });

  it('reports an unreachable server rather than throwing a fetch error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      })
    );

    await expect(uploadModel('Offline', [file('a.stl')], () => {})).rejects.toThrow(
      'Could not reach the server.'
    );
  });

  it('refuses an empty selection', async () => {
    await expect(uploadModel('Nothing', [], () => {})).rejects.toThrow('at least one file');
  });

  it('agrees with the server about the size cap', () => {
    expect(MAX_FILE_BYTES).toBe(500 * 1024 * 1024);
  });
});

function file(name: string): File {
  return new File(['solid'], name);
}
