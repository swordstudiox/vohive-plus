import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const webPackage = JSON.parse(readFileSync(new URL('../web/package.json', import.meta.url), 'utf8'))

test('web test script uses a shell-expanded test file pattern on Linux', () => {
  assert.equal(webPackage.scripts.test, 'tsx --test tests/*.test.ts')
})

test('web build separates fast bundling from full release checks', () => {
  assert.equal(webPackage.scripts.build, 'vite build')
  assert.equal(webPackage.scripts['build:check'], 'npm run typecheck && npm run build')
})
