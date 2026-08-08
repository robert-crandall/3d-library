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
    const model = await uploadModel(
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

  // Sequential matters: file 2 cannot start before file 1 returns the id it
  // needs. A failure therefore has to stop the run rather than press on.
  it('stops at the first failure and reports which file failed', async () => {
    let call = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        call += 1;
        if (call === 1) return ok({ id: 3, name: 'Half' });
        return {
          ok: false,
          status: 422,
          json: async () => ({ detail: 'library: invalid upload: bad name' })
        } as Response;
      })
    );

    const states: Array<[number, string, string | undefined]> = [];
    await expect(
      uploadModel('Half', [file('a.stl'), file('b.stl'), file('c.stl')], (i, s, e) =>
        states.push([i, s, e])
      )
    ).rejects.toThrow('library: invalid upload: bad name');

    expect(call).toBe(2);
    expect(states.at(-1)).toEqual([1, 'failed', 'library: invalid upload: bad name']);
    expect(states.some(([i]) => i === 2)).toBe(false);
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
