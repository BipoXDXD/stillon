/// <reference types="node" />
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
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
