import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const repoRoot = join(fileURLToPath(new URL('..', import.meta.url)))
const workflowsDir = join(repoRoot, '.github', 'workflows')

function readRepoFile(path) {
  return readFileSync(join(repoRoot, path), 'utf8')
}

function workflowFiles() {
  return readdirSync(workflowsDir).filter((name) => /\.(ya?ml)$/i.test(name))
}

test('README keeps fork attribution and upstream project overview', () => {
  const readme = readRepoFile('README.md')

  assert.match(
    readme,
    /本项目已从\s+\[windloom\/vohive-open\]\(https:\/\/github\.com\/windloom\/vohive-open\)\s+fork\s+为\s+\[swordstudiox\/vohive-plus\]\(https:\/\/github\.com\/swordstudiox\/vohive-plus\)/
  )
  assert.match(readme, /## 上游能力概览/)
  assert.match(readme, /多模组并发管理/)
  assert.match(readme, /轻量级代理引擎/)
  assert.match(readme, /统一接码\/验证码中心/)
  assert.match(readme, /VoWiFi 零信号通信/)
})

test('GitHub Actions do not publish container images or require registry secrets', () => {
  const names = workflowFiles()
  assert.ok(!names.includes('docker' + '-publish.yml'))
  assert.ok(!names.includes('docker' + '-build.yml'))

  const combined = names.map((name) => readFileSync(join(workflowsDir, name), 'utf8')).join('\n')
  const legacyImage = ['skyhot' + 'spur', 'vo' + 'hive'].join('/')
  const forbidden = [
    'Login to Docker' + 'Hub',
    'DOCKER' + 'HUB_USERNAME',
    'DOCKER' + 'HUB_TOKEN',
    legacyImage,
    'docker' + '/login-action',
    'docker' + '/build-push-action',
  ]
  for (const marker of forbidden) {
    assert.ok(!combined.includes(marker), `workflow should not contain ${marker}`)
  }
})

test('desktop Linux backend binary is not tracked by source control', () => {
  const trackedPath = 'desktop/src-tauri/resources/vohive/vohive-open_linux_amd64'
  const result = spawnSync('git', ['ls-files', '--', trackedPath], {
    cwd: repoRoot,
    encoding: 'utf8',
  })

  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.stdout.trim(), '')
  const ignoreRules = readRepoFile('.gitignore')
  assert.match(ignoreRules, /desktop\/src-tauri\/resources\/vohive\/vohive-open_linux_amd64/)
})
