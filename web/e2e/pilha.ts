import { execFileSync, spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { createServer } from 'node:net'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'

// A pilha do E2E: Postgres num container e o binário real, compilado com o
// painel embutido. Tudo em e2e/.tmp, que o .gitignore ignora.

export const RAIZ = path.resolve(import.meta.dirname, '../..')
const TMP = path.join(import.meta.dirname, '.tmp')
const ESTADO = path.join(TMP, 'pilha.json')

export const EMAIL = 'e2e@vigilume.test'
// A política de senha nova do Plano 5: de 15 a 128 code points.
export const SENHA = 'uma senha de teste bem comprida'

type Estado = { container: string; pid: number }

const chave = () => randomBytes(32).toString('base64')

function portaLivre(): Promise<number> {
  return new Promise((ok, falha) => {
    const s = createServer()
    s.listen(0, '127.0.0.1', () => {
      const endereco = s.address()
      s.close(() => (typeof endereco === 'object' && endereco ? ok(endereco.port) : falha(new Error('sem porta'))))
    })
  })
}

async function esperar(o: { oque: string; ms: number; tentar: () => Promise<boolean> | boolean }) {
  const fim = Date.now() + o.ms
  while (Date.now() < fim) {
    if (await o.tentar()) return
    await new Promise((r) => setTimeout(r, 250))
  }
  throw new Error(`E2E: ${o.oque} não ficou pronto em ${o.ms} ms`)
}

export async function subir(): Promise<{ baseURL: string }> {
  mkdirSync(TMP, { recursive: true })
  const dist = path.join(RAIZ, 'internal/webui/dist/index.html')
  if (!existsSync(dist)) throw new Error('E2E: rode "npm run build" antes: o binário embute internal/webui/dist')

  const binario = path.join(TMP, 'vigilume')
  execFileSync('go', ['build', '-o', binario, './cmd/vigilume'], {
    cwd: RAIZ,
    stdio: 'inherit',
    env: { ...process.env, GOFLAGS: '-buildvcs=false' },
  })

  const container = execFileSync(
    'docker',
    [
      'run', '-d', '--rm', '-P',
      '-e', 'POSTGRES_PASSWORD=postgres',
      '-v', `${path.join(import.meta.dirname, 'init.sql')}:/docker-entrypoint-initdb.d/init.sql:ro`,
      'postgres:18-alpine',
    ],
    { encoding: 'utf8' },
  ).trim()
  const estado: Estado = { container, pid: 0 }
  writeFileSync(ESTADO, JSON.stringify(estado))
  // Daqui em diante há o que derrubar: qualquer falha antes de a pilha ficar
  // pronta (banco que não sobe, binário que não passa no /readyz, user
  // create recusado) derruba container e processo antes de propagar. O
  // globalTeardown não roda quando o globalSetup falha.
  try {
    return await terminarDeSubir(container, binario, estado)
  } catch (e) {
    derrubar()
    throw e
  }
}

async function terminarDeSubir(container: string, binario: string, estado: Estado): Promise<{ baseURL: string }> {
  // O entrypoint sobe o Postgres duas vezes (uma para o init.sql); a porta
  // só vale depois da segunda, e é o pg_isready pelo TCP que diz.
  await esperar({
    oque: 'o Postgres',
    ms: 60_000,
    tentar: () => {
      try {
        execFileSync('docker', ['exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'vigilume', '-d', 'vigilume'], {
          stdio: 'ignore',
        })
        return true
      } catch {
        return false
      }
    },
  })
  const portaDoBanco = execFileSync('docker', ['port', container, '5432/tcp'], { encoding: 'utf8' })
    .split('\n')[0]!
    .split(':')
    .pop()!
    .trim()

  const porta = await portaLivre()
  const baseURL = `http://127.0.0.1:${porta}`
  const env = {
    ...process.env,
    DATABASE_URL: `postgres://vigilume:vigilume@127.0.0.1:${portaDoBanco}/vigilume?sslmode=disable`,
    HTTP_ADDR: `127.0.0.1:${porta}`,
    PUBLIC_BASE_URL: baseURL,
    CORS_ORIGIN: baseURL,
    MASTER_KEY: chave(),
    JWT_SECRET: chave(),
    IP_HASH_KEY: chave(),
    METRICS_ADDR: 'off',
    ROLE: 'all',
    LOG_LEVEL: 'warn',
  }
  const proc = spawn(binario, [], { env, stdio: ['ignore', 'inherit', 'inherit'], detached: true })
  estado.pid = proc.pid ?? 0
  writeFileSync(ESTADO, JSON.stringify(estado))
  proc.unref()

  await esperar({
    oque: 'o binário (/readyz)',
    ms: 60_000,
    tentar: async () => (await fetch(`${baseURL}/readyz`).catch(() => null))?.ok ?? false,
  })

  // O usuário pelo subcomando do contrato, com a senha pelo stdin (nunca em
  // argumento nem em variável de ambiente — D16 do Plano 5).
  execFileSync(binario, ['user', 'create', '--email', EMAIL], {
    env,
    input: `${SENHA}\n`,
    stdio: ['pipe', 'inherit', 'inherit'],
  })
  return { baseURL }
}

// Quantas linhas de auditoria com esta ação a pilha própria tem (o
// Postgres do container, pelo superusuário, que a RLS não filtra). null com
// pilha externa (E2E_BASE_URL): lá o teste não alcança o banco.
export function contarAuditoria(acao: string): number | null {
  if (!process.env.E2E_PILHA_PROPRIA || !existsSync(ESTADO)) return null
  if (!/^[a-z_.]+$/.test(acao)) throw new Error(`E2E: ação inválida: ${acao}`)
  const e = JSON.parse(readFileSync(ESTADO, 'utf8')) as Estado
  const saida = execFileSync(
    'docker',
    ['exec', e.container, 'psql', '-U', 'postgres', '-d', 'vigilume', '-tAc',
      `SELECT count(*) FROM audit_log WHERE action = '${acao}'`],
    { encoding: 'utf8' },
  )
  return Number(saida.trim())
}

export function derrubar() {
  if (!existsSync(ESTADO)) return
  const e = JSON.parse(readFileSync(ESTADO, 'utf8')) as Estado
  if (e.pid) {
    try {
      process.kill(e.pid, 'SIGTERM')
    } catch {
      // já tinha saído
    }
  }
  if (e.container) execFileSync('docker', ['rm', '-f', e.container], { stdio: 'ignore' })
}
