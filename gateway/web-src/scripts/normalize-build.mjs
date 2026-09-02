import { readdir, readFile, writeFile } from 'node:fs/promises'
import { extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const buildRoot = fileURLToPath(new URL('../../web', import.meta.url))
const textExtensions = new Set(['.css', '.html', '.js'])

async function normalize(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      await normalize(path)
      continue
    }
    if (!entry.isFile() || !textExtensions.has(extname(entry.name))) continue

    const source = await readFile(path, 'utf8')
    const normalized = source.replace(/\n[\t ]+\n/g, '\n\n')
    if (normalized !== source) await writeFile(path, normalized)
  }
}

await normalize(buildRoot)
