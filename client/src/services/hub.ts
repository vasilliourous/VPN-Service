/**
 * The Locus hub. Everything the client talks to — activation, heartbeat,
 * updates — is served from here.
 *
 * This is the ONE place the base URL is written. It is matched against the
 * server's `tier_configs` `server` field, which the hub also owns, so a hub
 * move is a server-side change plus this constant, never a string sweep.
 *
 * The URL is deliberately the bare duckdns host rather than a custom domain:
 * that is what the live hub serves today (`docs/DEPLOY.md`), and a client that
 * cannot resolve its hub cannot activate, heartbeat, or update.
 */
export const HUB_URL = 'https://networkingguides.duckdns.org'

/** Build an absolute hub URL from a path fragment. */
export const hubUrl = (path: string): string =>
  `${HUB_URL}/${path.replace(/^\/+/, '')}`
