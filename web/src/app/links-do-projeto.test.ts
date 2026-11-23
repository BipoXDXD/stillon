import { describe, expect, it } from 'vitest'

// As telas de recurso moram em /projects/{id}/…. Um link absoluto para
// /targets, /heartbeats, /incidents, /channels ou /status-pages passa pelo
// redirecionamento dos links antigos e cai no Projeto padrão: dentro de outro
// projeto, ele leva a pessoa para fora dele sem aviso. Os caminhos vêm de
// useRotaDoProjeto (ou do prefixo do projeto, nas linhas do painel).
const SECAO = '(targets|heartbeats|incidents|channels|status-pages)'
const ABSOLUTO = new RegExp(
  [
    `to=["'{\`]*/${SECAO}`,
    `navegar\\(\\s*["'\`]/${SECAO}`,
    `href:\\s*["'\`]/${SECAO}`,
    `pathname:\\s*["'\`]/${SECAO}`,
  ].join('|'),
  // i: o incidenteHref das linhas do painel também conta.
  'i',
)

// O redirecionamento dos links antigos é o único lugar que os conhece.
const PERMITIDOS = new Set(['/src/app/RedirecionarAoProjeto.tsx'])

function achados(arquivo: string, texto: string): string[] {
  return texto
    .split('\n')
    .map((linha, i) => (ABSOLUTO.test(linha) ? `${arquivo}:${i + 1}: ${linha.trim()}` : null))
    .filter((a): a is string => a !== null)
}

describe('guarda de link absoluto para tela de projeto', () => {
  it('acusa os links absolutos', () => {
    expect(achados('x.tsx', '<Link to="/targets/new">')).toHaveLength(1)
    expect(achados('x.tsx', '<Link to={`/heartbeats/${id}`}>')).toHaveLength(1)
    expect(achados('x.tsx', "navegar('/incidents')")).toHaveLength(1)
    expect(achados('x.ts', "href: '/channels',")).toHaveLength(1)
    expect(achados('x.ts', 'href: `/status-pages`,')).toHaveLength(1)
    expect(achados('x.ts', "incidenteHref: '/incidents?status=open',")).toHaveLength(1)
    expect(achados('x.tsx', "to={{ pathname: '/targets/1', search }}")).toHaveLength(1)
  })

  it('aceita o caminho do projeto e as rotas da conta', () => {
    expect(achados('x.tsx', '<Link to={noProjeto(`/targets/${id}`)}>')).toEqual([])
    expect(achados('x.ts', 'href: `${prefixo}/targets/${it.id}`,')).toEqual([])
    expect(achados('x.tsx', '<Link to="/settings">')).toEqual([])
    expect(achados('x.tsx', "navegar('/login')")).toEqual([])
  })

  it('nenhum código de produção aponta para uma tela de projeto pela raiz', () => {
    const fontes = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })
    const producao = Object.entries(fontes).filter(
      ([caminho]) => !/\.test\.tsx?$/.test(caminho) && !caminho.startsWith('/src/test/') && !PERMITIDOS.has(caminho),
    )
    expect(producao.length).toBeGreaterThan(50) // o glob achou o código; caminho errado não passa em branco
    expect(producao.flatMap(([caminho, texto]) => achados(caminho, texto))).toEqual([])
  })
})
