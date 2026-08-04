import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)))
const forbiddenRootModule = 'github.com/iniwex5/vohive'
const scanRoots = [
  '.github/workflows',
  'cmd',
  'internal',
  'pkg',
  'README.md',
  'Dockerfile',
  'Dockerfile.github',
  'Makefile',
  'go.mod',
  'go.work',
  'web/go.mod',
]

function collectFiles(path) {
  const fullPath = join(repoRoot, path)
  const stat = statSync(fullPath)
  if (stat.isFile()) {
    return [fullPath]
  }
  return readdirSync(fullPath).flatMap((entry) => collectFiles(join(path, entry)))
}

test('project module path uses swordstudiox fork instead of old upstream root', () => {
  const offenders = scanRoots
    .flatMap(collectFiles)
    .filter((file) => readFileSync(file, 'utf8').includes(forbiddenRootModule))

  assert.deepEqual(offenders, [])
})
