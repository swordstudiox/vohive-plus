import { chmodSync, copyFileSync, existsSync, mkdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const desktopDir = resolve(scriptDir, '..')
const repoRoot = resolve(desktopDir, '..')

const source = resolve(
  process.env.VOHIVE_BACKEND_SOURCE || resolve(repoRoot, 'dist', 'vohive-open_linux_amd64')
)
const destination = resolve(
  process.env.VOHIVE_BACKEND_DEST ||
    resolve(desktopDir, 'src-tauri', 'resources', 'vohive', 'vohive-open_linux_amd64')
)

if (existsSync(source)) {
  mkdirSync(dirname(destination), { recursive: true })
  copyFileSync(source, destination)
  chmodSync(destination, 0o755)
  console.log(`Synced Linux backend resource from ${source}`)
} else if (existsSync(destination)) {
  console.log(`Using existing Linux backend resource at ${destination}`)
} else {
  console.error(
    [
      'Missing Linux backend resource for desktop build.',
      `Expected source: ${source}`,
      `Expected destination: ${destination}`,
      'Build the backend first, then rerun the desktop build.',
    ].join('\n')
  )
  process.exit(1)
}
