/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ProvedorDeTema, useTema } from './tema'

const bootstrap = readFileSync('public/tema.js', 'utf8')

// Executa o arquivo público inteiro no mesmo DOM/storage do provider. Nenhuma
// constante privada é extraída ou usada para calcular o resultado esperado.
function iniciarAntesDoReact() {
  runInNewContext(bootstrap, { window, document, localStorage })
}

// O jsdom não implementa a preferência do sistema; o DOM, storage, contexto
// e provider continuam reais. EventTarget entrega os eventos aos listeners.
function preferenciaDoSistema(inicial: boolean) {
  let escuro = inicial
  const eventos = new EventTarget()
  const media = '(prefers-color-scheme: dark)'
  const mq: MediaQueryList = {
    get matches() { return escuro },
    media,
    onchange: null,
    addEventListener: eventos.addEventListener.bind(eventos),
    removeEventListener: eventos.removeEventListener.bind(eventos),
    dispatchEvent: eventos.dispatchEvent.bind(eventos),
    addListener: (ouvir) => { if (ouvir) eventos.addEventListener('change', ouvir as EventListener) },
    removeListener: (ouvir) => { if (ouvir) eventos.removeEventListener('change', ouvir as EventListener) },
  }
  vi.spyOn(window, 'matchMedia').mockReturnValue(mq)
  return (novo: boolean) => {
    escuro = novo
    eventos.dispatchEvent(Object.assign(new Event('change'), { matches: escuro, media }))
  }
}

function ControlesDeTema() {
  const { tema, mudar } = useTema()
  return <>
    <output aria-label="Preferência de tema">{tema}</output>
    <button onClick={() => mudar('claro')}>Escolher claro</button>
    <button onClick={() => mudar('escuro')}>Escolher escuro</button>
  </>
}

function montar() {
  return render(<ProvedorDeTema><ControlesDeTema /></ProvedorDeTema>)
}

function conferirEscuro(esperado: boolean) {
  expect(document.documentElement.classList.contains('dark')).toBe(esperado)
}

beforeEach(() => { document.documentElement.classList.remove('dark') })
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  document.documentElement.classList.remove('dark')
})

describe('bootstrap e provider preservam a preferência da mesma carga', () => {
  // Uma chave divergente ou fallback incorreto deve mudar a classe antes/depois
  // da montagem. As expectativas são literais, independentes das duas regras.
  const casos = [
    { salvo: null, sistema: false, escuro: false, preferencia: 'sistema' },
    { salvo: null, sistema: true, escuro: true, preferencia: 'sistema' },
    { salvo: 'sistema', sistema: false, escuro: false, preferencia: 'sistema' },
    { salvo: 'sistema', sistema: true, escuro: true, preferencia: 'sistema' },
    { salvo: 'claro', sistema: false, escuro: false, preferencia: 'claro' },
    { salvo: 'claro', sistema: true, escuro: false, preferencia: 'claro' },
    { salvo: 'escuro', sistema: false, escuro: true, preferencia: 'escuro' },
    { salvo: 'escuro', sistema: true, escuro: true, preferencia: 'escuro' },
    { salvo: 'invalido', sistema: false, escuro: false, preferencia: 'sistema' },
    { salvo: 'invalido', sistema: true, escuro: true, preferencia: 'sistema' },
  ]
  for (const c of casos) {
    it(`${c.salvo ?? 'ausente'} com sistema ${c.sistema ? 'escuro' : 'claro'}`, () => {
      preferenciaDoSistema(c.sistema)
      if (c.salvo !== null) localStorage.setItem('stillon.tema', c.salvo)
      iniciarAntesDoReact()
      conferirEscuro(c.escuro)
      montar()
      conferirEscuro(c.escuro)
      expect(screen.getByLabelText('Preferência de tema')).toHaveTextContent(c.preferencia)
    })
  }

  for (const sistema of [false, true]) {
    it(`sem leitura do storage segue sistema ${sistema ? 'escuro' : 'claro'}`, () => {
      preferenciaDoSistema(sistema)
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('storage indisponível') })
      iniciarAntesDoReact()
      conferirEscuro(sistema)
      montar()
      conferirEscuro(sistema)
      expect(screen.getByLabelText('Preferência de tema')).toHaveTextContent('sistema')
    })
  }
})

for (const c of [
  { escolha: 'claro', sistema: true, escuro: false },
  { escolha: 'escuro', sistema: false, escuro: true },
] as const) {
  it(`escolha ${c.escolha} sobrevive à recarga com sistema oposto`, () => {
    preferenciaDoSistema(c.sistema)
    iniciarAntesDoReact()
    const primeira = montar()
    fireEvent.click(screen.getByRole('button', { name: `Escolher ${c.escolha}` }))
    conferirEscuro(c.escuro)
    primeira.unmount()
    // Nova carga: não conservar classe/provider; apenas o storage real.
    document.documentElement.classList.remove('dark')
    iniciarAntesDoReact()
    conferirEscuro(c.escuro)
    montar()
    conferirEscuro(c.escuro)
    expect(screen.getByLabelText('Preferência de tema')).toHaveTextContent(c.escolha)
  })

  it(`escolha explícita ${c.escolha} não muda junto com o sistema`, () => {
    const mudarSistema = preferenciaDoSistema(c.sistema)
    localStorage.setItem('stillon.tema', c.escolha)
    iniciarAntesDoReact()
    montar()
    act(() => mudarSistema(!c.sistema))
    conferirEscuro(c.escuro)
    act(() => mudarSistema(c.sistema))
    conferirEscuro(c.escuro)
    expect(screen.getByLabelText('Preferência de tema')).toHaveTextContent(c.escolha)
  })
}

it('sistema acompanha mudanças e deixa de alterar o DOM depois de desmontar', () => {
  const mudarSistema = preferenciaDoSistema(false)
  iniciarAntesDoReact()
  const tela = montar()
  act(() => mudarSistema(true))
  conferirEscuro(true)
  act(() => mudarSistema(false))
  conferirEscuro(false)
  tela.unmount()
  act(() => mudarSistema(true))
  expect(document.documentElement.classList.contains('dark')).toBe(false)
})

it('sem escrita no storage a escolha ainda vale na aba atual', () => {
  preferenciaDoSistema(false)
  iniciarAntesDoReact()
  montar()
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('storage indisponível') })
  fireEvent.click(screen.getByRole('button', { name: 'Escolher escuro' }))
  conferirEscuro(true)
  expect(screen.getByLabelText('Preferência de tema')).toHaveTextContent('escuro')
})
