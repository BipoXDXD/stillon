import type { Page } from '@playwright/test'

// O painel só descobre o que mudou fora dele (o ping de uma tarefa, o
// avaliador abrindo ou fechando um incidente) pelo próprio poll: 15 s no
// resumo e no heartbeat, 30 s nos eventos e nos incidentes
// (web/src/lib/api/consultas.ts). Esperar esses timers em tempo real era a
// maior parte das jornadas do heartbeat no CI.
//
// noRitmoDoPoll acelera só o relógio do NAVEGADOR, e só enquanto dura uma
// espera pelo poll: os timers do próprio painel disparam antes, e a tela muda
// pelo mesmo caminho de sempre (o refetchInterval, sem recarregar e sem foco).
// Nada muda no servidor: o avaliador, os limitadores e a cadência do teste
// seguem no relógio real. Fora das esperas o relógio anda no ritmo normal, e
// timers curtos de interface (o "Copiado" de 2 s, os avisos) não encurtam.
//
// 4×: com a cadência de uma requisição por segundo (cadencia.ts), a tela da
// tarefa pede ~0,5 por segundo (heartbeat a cada 3,75 s, eventos e incidentes
// a cada 7,5 s) e o painel ~0,7; acima disso a fila da cadência cresceria.
export const ACELERACAO = 4
const TIQUE_MS = 250

// Precisa vir antes do primeiro goto: o relógio falso é instalado na página.
// O Date e os timers seguem andando sozinhos; noRitmoDoPoll só os adianta.
export async function instalarRelogio(page: Page): Promise<void> {
  await page.clock.install()
}

export async function noRitmoDoPoll<T>(page: Page, espera: () => Promise<T>): Promise<T> {
  const controle = { ativo: true }
  // A falha do relógio vira valor já aqui: rejeitada no meio da espera, sem
  // ninguém aguardando, ela seria uma rejeição não tratada.
  const falhaDoRelogio = adiantar(page, controle).then(
    () => null,
    (e: unknown) => e,
  )
  let resultado: T
  try {
    resultado = await espera()
  } finally {
    controle.ativo = false
  }
  // Um relógio que parou de avançar não pode passar por um poll que não
  // chegou: a falha dele volta aqui, depois da espera. Se a espera falhou, é a
  // falha dela que sobe (o finally acima não engole nada).
  const falha = await falhaDoRelogio
  if (falha) throw falha
  return resultado
}

async function adiantar(page: Page, controle: { ativo: boolean }): Promise<void> {
  while (controle.ativo) {
    await new Promise((ok) => setTimeout(ok, TIQUE_MS))
    if (controle.ativo) await page.clock.fastForward(TIQUE_MS * (ACELERACAO - 1))
  }
}
