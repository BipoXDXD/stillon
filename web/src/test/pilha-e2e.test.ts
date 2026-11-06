/// <reference types="node" />
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// A limpeza da pilha do E2E, com processos de verdade. A pasta de trabalho é
// própria (E2E_TMP): uma rodada do Playwright na mesma árvore não é tocada.

type Pilha = typeof import('../../e2e/pilha')

const sleepReal = execFileSync('sh', ['-c', 'command -v sleep'], { encoding: 'utf8' }).trim()

let tmp: string
let pilha: Pilha
let vivos: ChildProcess[]

beforeEach(async () => {
  tmp = mkdtempSync(path.join(tmpdir(), 'pilha-'))
  vivos = []
  vi.stubEnv('E2E_TMP', tmp)
  vi.stubEnv('PATH', `${path.join(tmp, 'bin')}:${process.env.PATH}`)
  vi.resetModules()
  pilha = await import('../../e2e/pilha')
})

afterEach(() => {
  for (const p of vivos) p.kill('SIGKILL')
  vi.unstubAllEnvs()
  rmSync(tmp, { recursive: true, force: true })
})

const registro = () => path.join(tmp, 'pilha.json')

function registrar(e: { pid?: number; container?: string }) {
  writeFileSync(registro(), JSON.stringify({ pid: 0, container: '', ...e }))
}

// Um processo que o `ps` mostra como <tmp>/<nome>, como o binário que o E2E
// compila. É o argv0 que o nomeia: o macOS mata a cópia de um binário do sistema.
function iniciar(nome: string) {
  const p = spawn(sleepReal, ['60'], { stdio: 'ignore', argv0: path.join(tmp, nome) })
  vivos.push(p)
  return p
}

// O docker da limpeza: registra os argumentos e faz o que o teste mandar.
function dockerFalso(comportamento: string) {
  const bin = path.join(tmp, 'bin')
  mkdirSync(bin, { recursive: true })
  const script = path.join(bin, 'docker')
  writeFileSync(script, `#!/bin/sh\necho "$*" >>"${tmp}/docker.log"\n${comportamento}\n`)
  chmodSync(script, 0o755)
}

const sinalDeSaida = (p: ChildProcess) =>
  new Promise<NodeJS.Signals | null>((ok) => p.once('exit', (_codigo, sinal) => ok(sinal)))

describe('uma rodada morta no meio', () => {
  it('derruba o binário dela', async () => {
    const binario = iniciar('stillon')
    const saida = sinalDeSaida(binario)
    registrar({ pid: binario.pid })

    pilha.limparPilhaVelha()

    expect(await saida).toBe('SIGTERM')
    expect(existsSync(registro())).toBe(false)
  })

  it('não derruba outro programa que herdou o pid', () => {
    const matar = vi.spyOn(process, 'kill')
    registrar({ pid: iniciar('sleep').pid })

    pilha.limparPilhaVelha()

    expect(matar).not.toHaveBeenCalled()
  })

  it('não confunde outro binário com nome parecido com o nosso', () => {
    const matar = vi.spyOn(process, 'kill')
    registrar({ pid: iniciar('stillon-velho').pid })

    pilha.limparPilhaVelha()

    expect(matar).not.toHaveBeenCalled()
  })

  it('aceita o pid que já não existe', async () => {
    const morto = iniciar('stillon')
    const saida = sinalDeSaida(morto)
    morto.kill('SIGKILL')
    await saida
    const matar = vi.spyOn(process, 'kill')
    registrar({ pid: morto.pid })

    pilha.limparPilhaVelha()

    expect(matar).not.toHaveBeenCalled()
    expect(existsSync(registro())).toBe(false)
  })
})

describe('o container da rodada', () => {
  const REGISTRO = { container: 'c-da-rodada' }

  it('sai e o registro vai junto', () => {
    dockerFalso('exit 0')
    registrar(REGISTRO)

    pilha.derrubar()

    expect(readFileSync(path.join(tmp, 'docker.log'), 'utf8')).toBe('rm -f c-da-rodada\n')
    expect(existsSync(registro())).toBe(false)
  })

  it('que já saiu não é erro', () => {
    const avisar = vi.spyOn(console, 'error')
    dockerFalso('echo "Error response from daemon: No such container: c-da-rodada" >&2; exit 1')
    registrar(REGISTRO)

    pilha.derrubar()

    expect(existsSync(registro())).toBe(false)
    expect(avisar).not.toHaveBeenCalled()
  })

  it('com o Docker parado fica no registro, e o aviso diz qual é', () => {
    const avisar = vi.spyOn(console, 'error').mockImplementation(() => {})
    dockerFalso('echo "Cannot connect to the Docker daemon" >&2; exit 1')
    registrar(REGISTRO)

    pilha.derrubar()

    expect(existsSync(registro())).toBe(true)
    expect(avisar).toHaveBeenCalledWith(expect.stringMatching(/c-da-rodada.*Cannot connect to the Docker daemon/s))
  })

  it('com o Docker pendurado o prazo vence e fica no registro', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    dockerFalso('exec sleep 30')
    registrar(REGISTRO)
    vi.stubEnv('E2E_PRAZO_DOCKER_MS', '300')
    vi.resetModules()
    const comPrazoCurto: Pilha = await import('../../e2e/pilha')

    comPrazoCurto.derrubar()

    expect(existsSync(registro())).toBe(true)
  })

  it('órfão de uma rodada velha impede subir outra por cima do registro', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    dockerFalso('echo "Cannot connect to the Docker daemon" >&2; exit 1')
    registrar(REGISTRO)

    expect(() => pilha.limparPilhaVelha()).toThrow(/rodada anterior/)
    expect(existsSync(registro())).toBe(true)
  })
})
