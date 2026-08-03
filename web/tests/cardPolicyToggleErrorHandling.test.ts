import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const toggles = await readFile(
  new URL('../src/composables/useCardPolicyToggles.ts', import.meta.url),
  'utf8'
)

const cardPolicyPanel = await readFile(
  new URL('../src/components/CardPolicyPanel.vue', import.meta.url),
  'utf8'
)

const esimCardPolicyInline = await readFile(
  new URL('../src/components/EsimCardPolicyInline.vue', import.meta.url),
  'utf8'
)

test('card policy toggles preserve backend error messages and clear pending in finally', () => {
  assert.match(toggles, /export type ToggleResult = \{ ok: boolean; message\?: string \}/)
  assert.match(toggles, /async function runToggle/)
  assert.match(toggles, /catch \(err\)/)
  assert.match(toggles, /finally\s*\{[\s\S]*pending\.value = false/)
})

test('card policy panels render concrete toggle error messages', () => {
  assert.match(cardPolicyPanel, /networkError/)
  assert.match(cardPolicyPanel, /\{\{ networkError \}\}/)
  assert.match(cardPolicyPanel, /roamingError/)
  assert.match(esimCardPolicyInline, /networkError/)
  assert.match(esimCardPolicyInline, /\{\{ roamingError \}\}/)
})
