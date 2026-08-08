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

/** What an upload left behind.
 *
 *  `failed` is the names of the files that did not make it. It is usually
 *  empty, and when it is not the model still exists with the rest - which is
 *  why this is a return value and not an exception. Throwing away the model on
 *  a partial failure would hide a row the user cannot see, cannot delete (there
 *  is no delete until a later milestone), and would duplicate the moment they
 *  pressed Upload again. */
export type UploadOutcome = { model: Model; failed: string[] };

/**
 * A failure that created nothing, or one that might have.
 *
 * `certain` is true only when the server *answered* with a refusal - a 4xx.
 * Those are generated before anything is committed, so nothing exists and
 * offering the user a retry is right.
 *
 * A dropped connection or a 5xx is not that. The request may well have landed:
 * the server itself treats a COMMIT that returns an error as possibly
 * committed, and a response can be lost on the way back regardless. Retrying
 * then is how you get two copies of a model that this milestone cannot delete,
 * so the caller has to offer a reload instead.
 */
export class UploadFailed extends Error {
  readonly certain: boolean;

  constructor(message: string, certain: boolean) {
    super(message);
    this.name = 'UploadFailed';
    this.certain = certain;
  }
}

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
 *
 * A failure part-way does not stop the rest: one file being too large says
 * nothing about the next, and until a later milestone adds "add files to an
 * existing model" there is no second chance for anything skipped here. Only a
 * failure on the *first* file throws, because then no model was created and
 * there is nothing to report.
 */
export async function uploadModel(
  name: string,
  files: File[],
  onState: (index: number, state: UploadState, error?: string) => void
): Promise<UploadOutcome> {
  if (files.length === 0) throw new Error('Pick at least one file.');

  let model: Model | undefined;
  const failed: string[] = [];

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
      const message = 'Could not reach the server.';
      onState(index, 'failed', message);
      if (!model) throw new UploadFailed(`${message} The model may still have been created.`, false);
      failed.push(file.name);
      continue;
    }

    if (!response.ok) {
      const message = await failureMessage(response);
      onState(index, 'failed', message);
      if (!model) {
        const certain = response.status < 500;
        throw new UploadFailed(
          certain ? message : `${message} The model may still have been created.`,
          certain
        );
      }
      failed.push(file.name);
      continue;
    }

    onState(index, 'done');

    if (!model) {
      try {
        model = (await response.json()) as Model;
      } catch {
        // The model was created - the server said 201 - but the body did not
        // arrive, so we do not know its id and cannot even show it. This is the
        // one success that has to be reported as a failure, and it is emphatically
        // not a safe one to retry.
        const message = 'The upload finished but the reply did not arrive. The model was created.';
        onState(index, 'failed', message);
        throw new UploadFailed(message, false);
      }
    }
  }

  // Unreachable: the loop runs at least once, and its first pass either assigns
  // `model` or throws. Narrowed rather than asserted non-null, so a future edit
  // that breaks the invariant fails here instead of at a `.id`.
  if (!model) throw new Error('Upload produced no model.');

  // The create response only knows about its own file. Re-read so the caller
  // gets the real counts for the grid.
  let refreshed: Model | undefined;
  try {
    const response = await fetch(`/api/models/${model.id}`);
    if (response.ok) refreshed = (await response.json()) as Model;
  } catch {
    // Handled below.
  }

  if (refreshed) {
    model = refreshed;
  } else if (model.fileCount !== files.length - failed.length) {
    // The re-read is the only thing that knows how many files the model ended
    // up with - the create response predates every file after the first. When
    // its count happens to match what we watched land (a single-file upload, or
    // one where everything after the first failed) it is still true and we keep
    // it. Otherwise showing it would tell the user files are missing when they
    // are not, and the obvious response to that is to upload the model again -
    // which this milestone cannot undo. So report it as unresolved instead.
    throw new UploadFailed(
      'The files uploaded, but the library could not be read back. The model was created.',
      false
    );
  }

  // The re-read is the arbiter, not the individual responses. A file whose
  // response was lost still landed, and telling the user it is missing when it
  // is sitting right there is its own kind of wrong.
  if (failed.length > 0 && model.fileCount === files.length) return { model, failed: [] };

  return { model, failed };
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
