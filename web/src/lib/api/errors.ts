/**
 * Turn a failed API response into something to show the user.
 *
 * There is deliberately no status-code-to-message table here. The server
 * already writes the copy - "invalid email or password", "registration is
 * closed", "email already registered" - and re-stating it in the frontend
 * means two sources of truth that drift. So: render what the server said.
 *
 * huma returns RFC 7807 problem details. A 422 puts the per-field complaints in
 * `errors[]` and leaves `detail` generic ("validation failed"), so the field
 * messages win when they exist.
 */
export function apiErrorMessage(error: unknown, fallback = 'Something went wrong.'): string {
  if (!error || typeof error !== 'object') return fallback;
  const problem = error as { detail?: unknown; errors?: unknown };

  if (Array.isArray(problem.errors)) {
    const messages = problem.errors
      .map((e) => (e && typeof e === 'object' ? (e as { message?: unknown }).message : undefined))
      .filter((m): m is string => typeof m === 'string' && m.length > 0);
    if (messages.length > 0) return messages.join('. ');
  }

  if (typeof problem.detail === 'string' && problem.detail.length > 0) return problem.detail;
  return fallback;
}
