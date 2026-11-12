# Mise panel

React 18 + Vite + TypeScript panel for the Mise API. It uses the approved
Hallmark design tokens, in English, with no mock data and no state library.

## Development

The API must be running on the same origin. For local work, build the panel and
serve it from the Go binary:

```sh
cd ../api
make build-all
MISE_DB=/tmp/mise.db MISE_PUBLIC_ORIGIN=http://127.0.0.1:8787 ./build/mise
```

For a fast Vite loop, run the binary on its default port (8787) and let Vite
forward `/api` to it — `vite.config.ts` already has the proxy:

```sh
cd ../api
MISE_DB=/tmp/mise.db MISE_PUBLIC_ORIGIN=http://127.0.0.1:5173 ./build/mise
cd ../web && npm ci && npm run dev
```

## Build

```sh
npm ci
npm run build
```

`npm run build` type-checks with `tsc -b` and writes `dist/`. The Go build stages
`dist/` into `api/internal/panel/dist` and embeds it in the binary.

## Screens

- Sign in with a human token; the server sets the 30-day cookie.
- Request access, keep the one-time signup secret, check status and claim a token
  once approved.
- Board with six columns, the attention block, search, project filter and the
  mobile state selector.
- Create a task with an idempotency key that survives retries of the same form.
- Move a task from its card with `If-Match`; `Approve and close` is the primary
  action for `done`.
- A lease badge for in-progress cards, including the expired state.
