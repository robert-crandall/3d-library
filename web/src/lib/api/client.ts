import createClient from 'openapi-fetch';
import type { paths } from './schema';

/**
 * The one API client. Deliberately bare: no baseUrl (the SPA is served by the
 * same binary as the API, so relative paths are correct), no headers, and no
 * interceptors.
 *
 * That last part is load-bearing. `fetch` sends same-origin cookies by default,
 * so the session cookie rides along on its own and there is nothing for a token
 * header to do. The integration tests exercise this same cookie-based boundary,
 * so this staying empty is checked, not just intended.
 */
export const api = createClient<paths>();

export type User = NonNullable<
  paths['/api/auth/me']['get']['responses'][200]['content']['application/json']
>;
