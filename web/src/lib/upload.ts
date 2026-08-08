import type { paths } from './api/schema';

/** The list endpoint's element type. Taken from the generated schema rather
 *  than written out, so a migration that changes the API breaks the build here
 *  instead of at runtime. NonNullable because the generator types a JSON body
 *  as possibly null. */
export type Model = NonNullable<
  paths['/api/models']['get']['responses'][200]['content']['application/json']
>[number];

/** Mirrors the server's caps in `internal/library`. Duplicated rather than
 *  derived: the server is the guard, this is only so the UI can say no before
 *  spending five minutes uploading something that will be refused. */
export const MAX_FILES = 20;
export const MAX_FILE_BYTES = 500 * 1024 * 1024;

export type UploadState = 'queued' | 'uploading' | 'done' | 'failed';

/**
 * Upload `files` as one model.
 *
 * Sequential, one request per file, because that is the shape the API has: the
 * first file creates the model and the rest are added to it, so nothing after
 * the first can start until the first returns an id. Sequential is also what
 * keeps a phone from opening twenty concurrent 500 MB uploads.
 *
 * Raw `fetch` rather than the generated client. The upload endpoints describe
 * their body as opaque binary in the OpenAPI document - they have to, or huma
 * would buffer a 500 MB request into memory before the handler ran - and
 * `openapi-typescript` renders that as `string`. So the typed client cannot
 * express a FormData here. See the comment on `create-model` in
 * `internal/library/api.go`.
 *
 * `onState` is called for every file as it moves, so the caller can render
 * progress without this function knowing anything about the UI.
 */
export async function uploadModel(
  name: string,
  files: File[],
  onState: (index: number, state: UploadState, error?: string) => void
): Promise<Model> {
  if (files.length === 0) throw new Error('Pick at least one file.');

  let model: Model | undefined;

  for (const [index, file] of files.entries()) {
    onState(index, 'uploading');

    const body = new FormData();
    body.append('file', file);

    const url = model
      ? `/api/models/${model.id}/files`
      : `/api/models?name=${encodeURIComponent(name)}`;

    let response: Response;
    try {
      response = await fetch(url, { method: 'POST', body });
    } catch {
      onState(index, 'failed', 'Could not reach the server.');
      throw new Error('Could not reach the server.');
    }

    if (!response.ok) {
      const message = await failureMessage(response);
      onState(index, 'failed', message);
      throw new Error(message);
    }

    onState(index, 'done');

    if (!model) {
      model = (await response.json()) as Model;
    }
  }

  // Unreachable: the loop runs at least once and either assigns or throws.
  // Narrowing it for the type checker rather than asserting non-null, so a
  // future edit that breaks the invariant fails loudly instead of at a `.id`.
  if (!model) throw new Error('Upload produced no model.');

  // The create response only knows about its own file. Re-read so the caller
  // gets the real counts for the grid.
  const refreshed = await fetch(`/api/models/${model.id}`);
  return refreshed.ok ? ((await refreshed.json()) as Model) : model;
}

/** huma reports failures as RFC 7807 problem documents; `detail` is the part
 *  worth showing. Falls back to the status when the body is not one, which is
 *  what a proxy returning HTML would give us. */
async function failureMessage(response: Response): Promise<string> {
  if (response.status === 413) return 'That file is over the 500 MB limit.';
  try {
    const body = await response.json();
    if (typeof body?.detail === 'string' && body.detail) return body.detail;
  } catch {
    // Not JSON.
  }
  return `Upload failed (${response.status}).`;
}
