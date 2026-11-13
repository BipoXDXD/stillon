import { describe, expect, it } from 'vitest'
import { caminho } from './caminho'

describe('caminho', () => {
  it('mantém o literal como está, sem codificar', () => {
    expect(caminho`/targets/summary?a=b#c d`).toBe('/targets/summary?a=b#c d')
  })

  it.each([
    ['barra', 'a/b', 'a%2Fb'],
    ['interrogação', 'a?b', 'a%3Fb'],
    ['jogo da velha', 'a#b', 'a%23b'],
    ['espaço', 'a b', 'a%20b'],
    ['porcentagem', '100%', '100%25'],
    ['acento', 'ação', 'a%C3%A7%C3%A3o'],
    ['ponto e ponto', '..', '..'],
    ['vazio', '', ''],
  ])('codifica a interpolação: %s', (_nome, id, esperado) => {
    expect(caminho`/targets/${id}/tags`).toBe(`/targets/${esperado}/tags`)
  })

  it('codifica cada interpolação e só elas', () => {
    const heartbeat = 'h/1'
    const incidente = 'i 2?'
    expect(caminho`/heartbeats/${heartbeat}/incidents/${incidente}/ack`).toBe('/heartbeats/h%2F1/incidents/i%202%3F/ack')
  })

  it('aceita interpolação no começo e no fim', () => {
    const id = 'x/y'
    expect(caminho`${id}`).toBe('x%2Fy')
    expect(caminho`/a/${id}`).toBe('/a/x%2Fy')
  })
})
