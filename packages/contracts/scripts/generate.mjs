// Regenerates the committed OpenAPI document and the TypeScript types over it.
//
//   node scripts/generate.mjs           rewrite openapi.json and src/schema.d.ts
//   node scripts/generate.mjs --check   regenerate into memory and fail on any difference
//
// `--check` is what CI runs. It is the whole point of committing the two files: a route
// whose shape changed without the types being regenerated is a compile error in `client`
// and `admin` the next time somebody regenerates, and a diff in CI today.

import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import openapiTS, { astToString } from 'openapi-typescript'

const here = dirname(fileURLToPath(import.meta.url))
const packageRoot = resolve(here, '..')
const repoRoot = resolve(packageRoot, '..', '..')

const documentPath = join(packageRoot, 'openapi.json')
const typesPath = join(packageRoot, 'src', 'schema.d.ts')

const check = process.argv.includes('--check')

// The document comes from the application, not from a running server.
//
// `go run ./apps/backend/cmd/api -openapi` builds the app and prints its OpenAPI
// description without opening a single connection, so this needs no database, no
// ClickHouse and no listening port — which is what lets the drift check run on every push
// rather than only in the job that starts the stack.
//
// Two settings are pinned rather than read from whoever's `.env` is on disk. `servers[0]`
// and `info.title` are the only parts of the document that come from configuration, and
// leaving them free would mean a developer with a different API_PUBLIC_URL regenerating a
// "changed" document that had not changed. The committed contract describes the public
// API, so it names the public address.
function emitDocument() {
  return execFileSync('go', ['run', './apps/backend/cmd/api', '-openapi'], {
    cwd: repoRoot,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    env: {
      ...process.env,
      APP_NAME: 'PlayerSense API',
      API_PUBLIC_URL: 'https://api.playersense.io'
    },
    // The framework logs its start-up lines to stderr; only stdout is the document.
    stdio: ['ignore', 'pipe', 'inherit']
  })
}

function read(path) {
  try {
    return readFileSync(path, 'utf8')
  } catch {
    return null
  }
}

function report(path, expected, actual) {
  if (expected === actual) return true
  const label = path.replace(`${repoRoot}/`, '')
  if (expected === null) {
    console.error(`✗ ${label} does not exist yet — run \`pnpm --filter @playersense/contracts generate\``)
  } else {
    console.error(`✗ ${label} is out of date — run \`pnpm --filter @playersense/contracts generate\``)
  }
  return false
}

const document = emitDocument()

// openapi-typescript is given the parsed document rather than the file path, so `--check`
// never has to write the document out first to read it back.
const ast = await openapiTS(JSON.parse(document), {
  // The dashboard reads what the API sends; it does not construct responses. Readonly
  // properties make that the type system's problem rather than a review comment.
  immutable: true,
  // An endpoint with no response body should be `undefined`, not `unknown` — the latter
  // silently accepts anything at the call site.
  emptyObjectsUnknown: false
})

const banner = `/**
 * Generated from the PlayerSense API's own OpenAPI document. Do not edit.
 *
 * Regenerate with:  pnpm --filter @playersense/contracts generate
 * Verified in CI by: pnpm --filter @playersense/contracts check
 */

`
const types = banner + astToString(ast)

if (check) {
  const ok = [
    report(documentPath, read(documentPath), document),
    report(typesPath, read(typesPath), types)
  ].every(Boolean)

  if (!ok) {
    console.error(
      '\nThe API and the committed contract disagree. This is the drift check doing its job:\n' +
      'regenerate, read the diff, and commit it with the change that caused it.'
    )
    process.exit(1)
  }
  console.log('✓ the committed contract matches the API')
} else {
  mkdirSync(dirname(typesPath), { recursive: true })
  writeFileSync(documentPath, document)
  writeFileSync(typesPath, types)
  const paths = Object.keys(JSON.parse(document).paths ?? {}).length
  console.log(`✓ wrote openapi.json (${paths} paths) and src/schema.d.ts`)
}
