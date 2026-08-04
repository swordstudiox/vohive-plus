import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const script = fileURLToPath(new URL('../scripts/sync-backend-resource.mjs', import.meta.url))

function runSync(env) {
  return spawnSync(process.execPath, [script], {
    env: { ...process.env, ...env },
    encoding: 'utf8',
  })
}

test('sync backend resource copies built Linux runtime into desktop resources', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vohive-sync-resource-'))
  try {
    const source = join(dir, 'dist', 'vohive-open_linux_amd64')
    const destination = join(dir, 'desktop', 'resources', 'vohive-open_linux_amd64')
    mkdirSync(join(dir, 'dist'), { recursive: true })
    writeFileSync(source, 'linux-runtime')

    const result = runSync({
      VOHIVE_BACKEND_SOURCE: source,
      VOHIVE_BACKEND_DEST: destination,
    })

    assert.equal(result.status, 0, result.stderr)
    assert.equal(readFileSync(destination, 'utf8'), 'linux-runtime')
    assert.match(result.stdout, /Synced Linux backend resource/)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('sync backend resource accepts an existing copied runtime', () => {
  const dir = mkdtempSync(join(tmpdir(), 'vohive-existing-resource-'))
  try {
    const source = join(dir, 'missing', 'vohive-open_linux_amd64')
    const destination = join(dir, 'resources', 'vohive-open_linux_amd64')
    mkdirSync(join(dir, 'resources'), { recursive: true })
    writeFileSync(destination, 'existing-runtime')

    const result = runSync({
      VOHIVE_BACKEND_SOURCE: source,
      VOHIVE_BACKEND_DEST: destination,
    })

    assert.equal(result.status, 0, result.stderr)
    assert.equal(readFileSync(destination, 'utf8'), 'existing-runtime')
    assert.match(result.stdout, /Using existing Linux backend resource/)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('sync backend resource fails clearly when no runtime exists', () => {
  const dir = mkdtempSync(join(tmpdir(), 'vohive-missing-resource-'))
  try {
    const result = runSync({
      VOHIVE_BACKEND_SOURCE: join(dir, 'dist', 'vohive-open_linux_amd64'),
      VOHIVE_BACKEND_DEST: join(dir, 'resources', 'vohive-open_linux_amd64'),
    })

    assert.equal(result.status, 1)
    assert.match(result.stderr, /Missing Linux backend resource/)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
