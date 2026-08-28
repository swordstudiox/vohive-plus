export function normalizeDisplayVersion(rawVersion: unknown, fallbackVersion: string): string {
  const version = String(rawVersion ?? '').trim()
  if (!version) return fallbackVersion
  if (version.toLowerCase() === 'unknown') return fallbackVersion
  return version
}
