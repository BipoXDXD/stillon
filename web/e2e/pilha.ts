import { execFileSync, spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { createServer } from 'node:net'
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'

// A pilha do E2E: Postgres num container e o binário real, compilado com o
// painel embutido. Tudo em e2e/.tmp, que o .gitignore ignora.

export const RAIZ = path.resolve(import.meta.dirname, '../..')
const TMP = path.join(import.meta.dirname, '.tmp')
const ESTADO = path.join(TMP, 'pilha.json')

export const EMAIL = 'e2e@stillon.example'
// A política de senha nova do Plano 5: de 15 a 128 code points.
export const SENHA = 'uma senha de teste bem comprida'

// databaseURL: a do binário, para o desligar2FA rodar o CLI contra o mesmo
// banco (só credenciais do container desta rodada, em e2e/.tmp).
type Estado = { container: string; pid: number; databaseURL?: string }

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

// desistir: uma falha que não adianta esperar (o binário que já saiu); a
// espera termina com ela, em vez de gastar o prazo inteiro.
async function esperar(o: {
  oque: string
  ms: number
  tentar: () => Promise<boolean> | boolean
  desistir?: () => Error | null
}) {
  const fim = Date.now() + o.ms
  while (Date.now() < fim) {
    const motivo = o.desistir?.()
    if (motivo) throw motivo
    if (await o.tentar()) return
    await new Promise((r) => setTimeout(r, 250))
  }
  throw new Error(`E2E: ${o.oque} não ficou pronto em ${o.ms} ms`)
}

// Uma rodada morta no meio (Ctrl+C, kill) não roda o globalTeardown, e o
// container e o binário dela ficam de pé. A próxima rodada os derruba antes
// de subir os seus — só pelos ids que a rodada velha gravou em pilha.json.
export function limparPilhaVelha() {
  if (!existsSync(ESTADO)) return
  derrubar({ soSeForONosso: true })
}

export async function subir(): Promise<{ baseURL: string }> {
  mkdirSync(TMP, { recursive: true })
  limparPilhaVelha()
  const dist = path.join(RAIZ, 'internal/webui/dist/index.html')
  if (!existsSync(dist)) throw new Error('E2E: rode "npm run build" antes: o binário embute internal/webui/dist')

  const binario = path.join(TMP, 'stillon')
  execFileSync('go', ['build', '-o', binario, './cmd/stillon'], {
    cwd: RAIZ,
    stdio: 'inherit',
    env: { ...process.env, GOFLAGS: '-buildvcs=false' },
  })

  const container = execFileSync(
    'docker',
    [
      // A porta do banco só no loopback, numa porta livre que o Docker escolhe
      // ("-P" publicaria em 0.0.0.0, para a rede toda).
      'run', '-d', '--rm', '-p', '127.0.0.1::5432',
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
        execFileSync('docker', ['exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'stillon', '-d', 'stillon'], {
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
    DATABASE_URL: `postgres://stillon:stillon@127.0.0.1:${portaDoBanco}/stillon?sslmode=disable`,
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
  estado.databaseURL = env.DATABASE_URL
  writeFileSync(ESTADO, JSON.stringify(estado))
  // O binário que sai antes do /readyz (configuração recusada, porta ocupada)
  // falha a subida na hora, com o código de saída, e não depois de 60 s.
  let saida: Error | null = null
  proc.on('exit', (codigo, sinal) => {
    saida = new Error(`E2E: o binário saiu antes do /readyz (código ${codigo}, sinal ${sinal})`)
  })
  proc.unref()

  await esperar({
    oque: 'o binário (/readyz)',
    ms: 60_000,
    tentar: async () => (await fetch(`${baseURL}/readyz`).catch(() => null))?.ok ?? false,
    desistir: () => saida,
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
    ['exec', e.container, 'psql', '-U', 'postgres', '-d', 'stillon', '-tAc',
      `SELECT count(*) FROM audit_log WHERE action = '${acao}'`],
    { encoding: 'utf8' },
  )
  return Number(saida.trim())
}

// desligar2FA roda `stillon user reset-2fa` para o e-mail: o 2FA desligado e
// as sessões encerradas, pelo CLI, sem passar pelo painel. É a rede de
// segurança do troca.spec.ts (M2 da revisão do painel do Plano 10): o teste
// liga o 2FA do único usuário, e uma falha no meio o deixaria ligado para as
// specs seguintes (ou para a próxima rodada contra a mesma pilha). Idempotente:
// sem 2FA ligado, o comando diz "nada mudou" e sai 0.
//
// Na pilha própria, o binário desta rodada com a DATABASE_URL dela. Contra uma
// pilha externa, só o compose (E2E_COMPOSE_SERVICE, o serviço do app, como no
// CI): `docker compose exec` no binário do contêiner, que já tem a
// DATABASE_URL. Devolve false quando não há como (outra pilha externa): quem
// chama pula o teste.
export function podeDesligar2FA(): boolean {
  return Boolean((process.env.E2E_PILHA_PROPRIA && existsSync(ESTADO)) || process.env.E2E_COMPOSE_SERVICE)
}

export function desligar2FA(email: string) {
  if (process.env.E2E_PILHA_PROPRIA && existsSync(ESTADO)) {
    const e = JSON.parse(readFileSync(ESTADO, 'utf8')) as Estado
    if (!e.databaseURL) throw new Error('E2E: pilha.json sem a DATABASE_URL')
    execFileSync(path.join(TMP, 'stillon'), ['user', 'reset-2fa', '--email', email], {
      env: { ...process.env, DATABASE_URL: e.databaseURL },
      stdio: ['ignore', 'inherit', 'inherit'],
    })
    return
  }
  const servico = process.env.E2E_COMPOSE_SERVICE
  if (!servico) throw new Error('E2E: sem pilha própria nem E2E_COMPOSE_SERVICE, não há como desligar o 2FA')
  execFileSync('docker', ['compose', 'exec', '-T', servico, '/stillon', 'user', 'reset-2fa', '--email', email], {
    cwd: RAIZ,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
}

// É o nosso binário: o pid de uma rodada velha pode ter sido reusado por
// outro processo. Sem /proc (macOS), não se sabe, e não se mata.
function ehONossoBinario(pid: number): boolean {
  try {
    return readFileSync(`/proc/${pid}/cmdline`, 'utf8').split('\0')[0] === path.join(TMP, 'stillon')
  } catch {
    return false
  }
}

// Derruba só o que pilha.json registrou, e apaga o registro: uma rodada
// seguinte não tenta derrubar de novo ids que já não são nossos.
export function derrubar(o: { soSeForONosso?: boolean } = {}) {
  if (!existsSync(ESTADO)) return
  const e = JSON.parse(readFileSync(ESTADO, 'utf8')) as Estado
  if (e.pid && (!o.soSeForONosso || ehONossoBinario(e.pid))) {
    try {
      process.kill(e.pid, 'SIGTERM')
    } catch {
      // já tinha saído
    }
  }
  if (e.container) {
    try {
      execFileSync('docker', ['rm', '-f', e.container], { stdio: 'ignore' })
    } catch {
      // já tinha saído (o --rm o apaga quando para)
    }
  }
  rmSync(ESTADO, { force: true })
}
