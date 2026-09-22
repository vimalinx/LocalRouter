import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { agentUsage, analytics, channels, logs, policies, protocols, tokens } from './readme-demo-data'

const root = fileURLToPath(new URL('../', import.meta.url))
const port = 18327
const summary = {
  channels: channels.length, tokens: tokens.length, protocols: protocols.length, protocols_ready: protocols.length,
  listen: `127.0.0.1:${port}`, admin_auth_enabled: false, engine: 'localrouter-native', billing: 'disabled', oauth: 'external',
  api_token_file: '/demo/localrouter/api-token', admin_token_file: '/demo/localrouter/admin-token',
  database_path: '/demo/localrouter/local.db', config_dir: '/demo/config', state_dir: '/demo/state',
  cache_dir: '/demo/cache', protocol_dir: '/demo/protocols',
  update: { enabled: false, automatic: false, current_version: 'source-demo', channel: 'development', status: 'development' },
}
const page = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 100 })
const responses: Record<string, unknown> = {
  '/local/status': { success: true, mode: 'documentation-demo', ...summary },
  '/local/api/summary': summary, '/local/api/analytics': analytics, '/local/api/protocols': protocols,
  '/local/api/providers': [{ id: 1, key: 'openai', name: 'OpenAI 兼容协议', base_url: 'https://example.invalid/v1', requires_key: true }],
  '/local/api/channels': page(channels), '/local/api/tokens': page(tokens), '/local/api/logs': page(logs),
  '/local/api/token-policies': policies, '/local/api/agent-usage': agentUsage,
  '/local/api/identity-requests': [],
  '/local/api/service-allowance-settings': { enabled: false, revision: 0, has_saved_rules: false },
  '/local/api/service-allowances': [],
  '/local/api/maintenance-access': { agent_tokens_enabled: false, default_auth: 'admin', admin_header: 'X-Local-Admin',
    agent_auth: 'bearer', agent_capability: 'localrouter.maintain', service_tokens: 'call-only', maintenance_tokens: 'maintenance-only' },
  '/local/api/protocol-drafts': [], '/local/api/protocols/history': [],
  '/local/api/workflows/jobs': [], '/local/api/protocol-events': [],
}
const server = await createServer({
  root, configFile: `${root}vite.config.ts`,
  server: { host: '127.0.0.1', port, strictPort: true, open: false, proxy: {} },
  plugins: [{ name: 'readme-synthetic-api', configureServer(vite) {
    vite.middlewares.use((request, response, next) => {
      const pathname = new URL(request.url || '/', `http://127.0.0.1:${port}`).pathname
      if (!/^\/(local|manage|p|v1|v1beta|w|mcp|docs|\.well-known)(\/|$)/.test(pathname)) return next()
      response.setHeader('Content-Type', 'application/json; charset=utf-8')
      response.setHeader('Cache-Control', 'no-store')
      if (request.method !== 'GET') {
        response.statusCode = 405
        response.end(JSON.stringify({ success: false, message: '截图演示只读，所有操作均未执行。' }))
      } else if (Object.hasOwn(responses, pathname)) {
        const data = responses[pathname]
        response.end(JSON.stringify(pathname === '/local/status' ? data : { success: true, data }))
      } else {
        response.statusCode = 404
        response.end(JSON.stringify({ success: false, message: '此接口未包含在截图演示中。' }))
      }
    })
  } }],
})
await server.listen()
console.log(`README screenshot demo: http://127.0.0.1:${port}/ (synthetic data, read-only)`)
for (const signal of ['SIGINT', 'SIGTERM'] as const) process.once(signal, async () => { await server.close(); process.exit(0) })
