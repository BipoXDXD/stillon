import { describe, expect, it } from 'vitest'
import type { Alvo } from '@/lib/api/schemas'
import {
  apagaOsHeaders,
  campoDoServidor,
  escolhaDoIntervalo,
  esquemaAlvo,
  esquemaDoAlvo,
  paraAlteracao,
  paraCriacao,
  valoresDe,
  type ValoresDoAlvo,
} from './esquema'

// Fixtures literais: não derivamos o esperado do algoritmo ou dos padrões
// exportados. Mutantes de bytes, fronteiras, normalização e PATCH são
// exercitados separadamente, mantendo o produto intacto na entrega.
function entrada(p: Partial<ValoresDoAlvo> = {}): ValoresDoAlvo {
  return {
    name: 'API', url: 'https://api.exemplo.com/health', method: 'GET',
    intervaloEscolhido: '60', interval_sec: 60, timeout_ms: 10000,
    expected_status: 200, lentidao: 'padrao', degraded_threshold_ms: 2000,
    enabled: true, modoHeaders: 'substituir', headers: [], tagsTexto: '',
    modoDaRegra: 'desligada', valorDaRegra: '', ponteiroDaRegra: '', ...p,
  }
}

function camposComErro(p: Partial<ValoresDoAlvo>) {
  const r = esquemaAlvo.safeParse(entrada(p))
  return r.success ? [] : r.error.issues.map((e) => e.path.join('.'))
}

describe('validação pública do formulário de alvo', () => {
  it('normaliza somente os espaços das pontas de nome, URL e nome do header', () => {
    const r = esquemaAlvo.parse(entrada({
      name: '  Minha API  ', url: '\u00a0https://api.exemplo.com/health  ',
      headers: [{ nome: '  X-Chave  ', valor: '  valor preservado  ' }],
    }))
    expect(r.name).toBe('Minha API')
    expect(r.url).toBe('https://api.exemplo.com/health')
    expect(r.headers).toEqual([{ nome: 'X-Chave', valor: '  valor preservado  ' }])
  })

  it.each([
    ['', false], ['   ', false], ['nome\u0000oculto', false],
    ['🙂'.repeat(100), true], ['🙂'.repeat(101), false],
  ])('nome com %s: aceitação %s', (name, aceito) => {
    expect(esquemaAlvo.safeParse(entrada({ name })).success).toBe(aceito)
  })

  // A regra inteira está em lib/rotulo.test.ts; aqui, que o campo name a usa
  // e diz o motivo (o servidor só devolveria "Valor inválido.").
  it.each([
    ['inversão bidi', 'API‮telas'],
    ['espaço de largura zero', 'API​pública'],
    ['BOM no meio', 'API﻿pública'],
    ['hífen suave', 'API­pública'],
    ['ZWJ solto', 'API‍ pública'],
  ])('nome com %s mostra o motivo no campo', (_caso, name) => {
    const r = esquemaAlvo.safeParse(entrada({ name }))
    expect(r.success).toBe(false)
    expect(r.error?.issues.map((e) => [e.path.join('.'), e.message])).toEqual([
      ['name', 'Sem caracteres invisíveis ou de controle (direção do texto, largura zero, hífen suave).'],
    ])
  })

  it.each([
    ['emoji com ZWJ', 'Equipe 👩‍💻'],
    ['persa com ZWNJ', 'می‌خواهم'],
  ])('nome com %s é aceito', (_caso, name) => {
    expect(esquemaAlvo.safeParse(entrada({ name })).success).toBe(true)
  })

  it.each([
    ['não é uma URL', false], ['https://', false], ['ftp://exemplo.com/', false],
    ['http://./', false],
    ['https://api.exemplo.com/', true], ['http://8.8.8.8/', true],
    ['http://Localhost./', false], ['http://api.localhost/', false],
    ['http://0.1.2.3/', false], ['http://10.1.2.3/', false], ['http://127.0.0.1/', false],
    ['http://224.0.0.1/', false], ['http://223.1.2.3/', true],
    ['http://169.254.1.1/', false], ['http://169.253.1.1/', true],
    ['http://172.15.1.1/', true], ['http://172.16.1.1/', false],
    ['http://172.31.1.1/', false], ['http://172.32.1.1/', true],
    ['http://192.168.1.1/', false], ['http://192.167.1.1/', true],
    ['http://100.63.1.1/', true], ['http://100.64.1.1/', false],
    ['http://100.127.1.1/', false], ['http://100.128.1.1/', true],
    ['http://[::]/', false], ['http://[::1]/', false],
    ['http://[fc00::1]/', false], ['http://[fdff::1]/', false],
    ['http://[fe80::1]/', false], ['http://[febf::1]/', false],
    ['http://[fc::1]/', true], ['http://[fe8::1]/', true],
    ['http://[fe7f::1]/', true], ['http://[fec0::1]/', true],
  ])('URL %s: aceitação pelo formulário %s', (url, aceito) => {
    // Só o subconjunto de rejeições antecipadas do formulário, sem afirmar
    // que o endereço passa pela autorização de rede ou resolve publicamente.
    expect(esquemaAlvo.safeParse(entrada({ url })).success).toBe(aceito)
  })

  // Host de rótulo único (sem ponto) é nome da rede interna, como o
  // servidor recusa (safedial.IsSingleLabel).
  it.each([
    ['http://db/', false], ['http://db:5432/', false], ['http://db./', false],
    ['http://db../', false], ['https://INTRANET/', false], ['http://db。/', false],
    ['http://a.b/', true], ['https://exemplo.com/', true], ['http://exemplo.com./', true],
    ['http://[2606:4700:4700::1111]/', true],
  ])('URL %s: aceitação pelo formulário %s', (url, aceito) => {
    // Só o subconjunto de rejeições antecipadas do formulário, sem afirmar
    // que o endereço passa pela autorização de rede ou resolve publicamente.
    expect(esquemaAlvo.safeParse(entrada({ url })).success).toBe(aceito)
  })

  it.each([
    ['ASCII com 2048 bytes', 'https://a.invalid/' + 'a'.repeat(2030), true],
    ['ASCII com 2049 bytes', 'https://a.invalid/' + 'a'.repeat(2031), false],
    ['multibyte com 2048 bytes', 'https://a.invalid/' + 'é'.repeat(1015), true],
    ['multibyte com 2050 bytes', 'https://a.invalid/' + 'é'.repeat(1016), false],
  ])('URL %s respeita o limite em bytes', (_, url, aceito) => {
    expect(esquemaAlvo.safeParse(entrada({ url })).success).toBe(aceito)
  })

  it('editar conserva a URL guardada; mudar a URL reaplica a validação', () => {
    const esquema = esquemaDoAlvo({ url: 'http://0x7f.1/' })
    expect(esquema.safeParse(entrada({ url: ' http://0x7f.1/ ', modoHeaders: 'manter' })).success).toBe(true)
    expect(esquema.safeParse(entrada({ url: 'http://127.0.0.1/', modoHeaders: 'manter' })).success).toBe(false)
    expect(esquemaAlvo.safeParse(entrada({ url: 'http://0x7f.1/' })).success).toBe(false)
  })

  it.each([
    [{ interval_sec: 29 }, 'interval_sec'], [{ interval_sec: 86401 }, 'interval_sec'],
    [{ interval_sec: 30.5 }, 'interval_sec'], [{ timeout_ms: 999 }, 'timeout_ms'],
    [{ timeout_ms: 30001 }, 'timeout_ms'], [{ expected_status: 99 }, 'expected_status'],
    [{ expected_status: 600 }, 'expected_status'],
  ])('limites de %j produzem erro no campo %s', (valores, campo) => {
    expect(camposComErro(valores)).toContain(campo)
  })

  it.each([
    { interval_sec: 30, timeout_ms: 1000, expected_status: 100 },
    { interval_sec: 86400, timeout_ms: 30000, expected_status: 599 },
  ])('aceita as fronteiras válidas %j', (valores) => {
    expect(esquemaAlvo.safeParse(entrada(valores)).success).toBe(true)
  })

  it('jitter exige timeout estritamente abaixo de 90%; fora das faixas só acusa o próprio campo', () => {
    expect(camposComErro({ interval_sec: 30, timeout_ms: 26999 })).toEqual([])
    expect(camposComErro({ interval_sec: 30, timeout_ms: 27000 })).toEqual(['timeout_ms'])
    expect(camposComErro({ interval_sec: 20, timeout_ms: 27000 })).toEqual(['interval_sec'])
    expect(camposComErro({ interval_sec: 86401, timeout_ms: 30000 })).toEqual(['interval_sec'])
    expect(camposComErro({ interval_sec: 30, timeout_ms: 999 })).toEqual(['timeout_ms'])
    expect(camposComErro({ interval_sec: 30, timeout_ms: 30001 })).toEqual(['timeout_ms'])
  })

  it.each([
    [1, true], [60000, true], [0, false], [60001, false], [1.5, false], [NaN, false],
  ])('limiar personalizado %s: aceitação %s', (degraded_threshold_ms, aceito) => {
    expect(esquemaAlvo.safeParse(entrada({ lentidao: 'personalizado', degraded_threshold_ms })).success).toBe(aceito)
  })

  it.each(['padrao', 'desativado'] as const)('limiar escondido inválido não trava modo %s', (lentidao) => {
    expect(esquemaAlvo.safeParse(entrada({ lentidao, degraded_threshold_ms: NaN })).success).toBe(true)
    expect(esquemaAlvo.safeParse(entrada({ lentidao, degraded_threshold_ms: 60001 })).success).toBe(true)
  })

  it('headers só são validados quando serão substituídos; na edição vazia é preciso escolher remover', () => {
    const headers = [{ nome: 'Host', valor: 'x' }]
    expect(camposComErro({ modoHeaders: 'manter', headers })).toEqual([])
    expect(camposComErro({ modoHeaders: 'remover', headers })).toEqual([])
    expect(camposComErro({ headers })).toEqual(['headers.0.nome'])
    const editar = esquemaDoAlvo({ url: 'https://api.exemplo.com/health' })
    const vazio = editar.safeParse(entrada())
    expect(vazio.success).toBe(false)
    expect(vazio.error?.issues.map((e) => e.path.join('.'))).toEqual(['headers'])
    expect(editar.safeParse(entrada({ modoHeaders: 'remover' })).success).toBe(true)
    expect(esquemaAlvo.safeParse(entrada()).success).toBe(true)
  })

  it('rejeita nomes inválidos e duplicados sem distinguir maiúsculas', () => {
    expect(camposComErro({ headers: [{ nome: 'com espaço', valor: 'a' }] })).toEqual(['headers.0.nome'])
    expect(camposComErro({ headers: [{ nome: 'X-A', valor: 'a' }, { nome: 'x-a', valor: 'b' }] })).toEqual(['headers.1.nome'])
    expect(camposComErro({ headers: [{ nome: 'X-A', valor: 'a' }, { nome: 'X-B', valor: 'b' }] })).toEqual([])
  })

  it.each(['Host', 'Content-Length', 'Transfer-Encoding', 'Connection', 'Keep-Alive', 'Upgrade', 'TE', 'Trailer', 'Expect', 'Proxy-Authorization', 'Proxy-Connection'])('recusa o header controlado %s', (nome) => {
    expect(camposComErro({ headers: [{ nome, valor: 'x' }] })).toEqual(['headers.0.nome'])
  })

  it('valor multibyte de header respeita 1 KiB, e quantidade respeita 20', () => {
    expect(camposComErro({ headers: [{ nome: 'X-A', valor: 'é'.repeat(512) }] })).toEqual([])
    expect(camposComErro({ headers: [{ nome: 'X-A', valor: 'é'.repeat(513) }] })).toEqual(['headers.0.valor'])
    const vinte = Array.from({ length: 20 }, (_, i) => ({ nome: `X-${i}`, valor: 'x' }))
    expect(camposComErro({ headers: vinte })).toEqual([])
    expect(camposComErro({ headers: [...vinte, { nome: 'X-20', valor: 'x' }] })).toEqual(['headers'])
  })

  it('soma dos nomes e valores respeita 8 KiB sem confundir com o limite de cada valor', () => {
    // Oito nomes de 8 bytes + valores de 1016 bytes = 8192 bytes.
    const headers = Array.from({ length: 8 }, (_, i) => ({ nome: `X-00000${i}`, valor: 'x'.repeat(1016) }))
    expect(camposComErro({ headers })).toEqual([])
    expect(camposComErro({ headers: [{ nome: 'X-000000', valor: 'x'.repeat(1017) }, ...headers.slice(1)] })).toEqual(['headers'])
  })
})

describe('corpos e valores públicos do formulário', () => {
  it.each([[30, '30'], [60, '60'], [300, '300'], [900, '900'], [3600, '3600'], [42, 'outro'], [86400, 'outro']])('intervalo %s mostra escolha %s', (segundos, escolha) => {
    expect(escolhaDoIntervalo(Number(segundos))).toBe(escolha)
  })

  it.each([[null, 'desativado', 2000], [2000, 'padrao', 2000], [800, 'personalizado', 800]] as const)('alvo com limiar %s prepara modo %s e valor %s sem expor headers', (limiar, modo, valor) => {
    const a: Alvo = {
      id: 'alvo-1', name: 'Site', url: 'https://site.exemplo.com/', method: 'HEAD',
      interval_sec: 42, timeout_ms: 2000, expected_status: 204, degraded_threshold_ms: limiar,
      enabled: false, header_names: ['Authorization'], tags: ['b', 'a'], tls_warn: true, tls: null,
      content_rule: null,
      created_at: '2026-10-05T00:00:00Z',
    }
    expect(valoresDe(a)).toEqual({
      name: 'Site', url: 'https://site.exemplo.com/', method: 'HEAD', intervaloEscolhido: 'outro',
      interval_sec: 42, timeout_ms: 2000, expected_status: 204, lentidao: modo,
      degraded_threshold_ms: valor, enabled: false, modoHeaders: 'manter', headers: [], tagsTexto: 'b, a',
      modoDaRegra: 'desligada', valorDaRegra: '', ponteiroDaRegra: '',
    })
  })

  it('POST manda números explícitos e headers, mas omite o limiar padrão', () => {
    expect(paraCriacao(entrada({ headers: [{ nome: 'X-A', valor: '1' }] }))).toEqual({
      name: 'API', url: 'https://api.exemplo.com/health', method: 'GET', interval_sec: 60,
      timeout_ms: 10000, expected_status: 200, enabled: true, headers: { 'X-A': '1' },
    })
    expect(paraCriacao(entrada())).not.toHaveProperty('headers')
    expect(paraCriacao(entrada({ lentidao: 'desativado' }))).toHaveProperty('degraded_threshold_ms', null)
    expect(paraCriacao(entrada({ lentidao: 'personalizado', degraded_threshold_ms: 800 }))).toHaveProperty('degraded_threshold_ms', 800)
  })

  it.each([
    [{ name: 'Outro' }, { name: 'Outro' }], [{ url: 'https://outro.exemplo.com/' }, { url: 'https://outro.exemplo.com/' }],
    [{ method: 'POST' }, { method: 'POST' }], [{ interval_sec: 300 }, { interval_sec: 300 }],
    [{ timeout_ms: 2000 }, { timeout_ms: 2000 }], [{ expected_status: 204 }, { expected_status: 204 }],
    [{ enabled: false }, { enabled: false }],
  ] satisfies [Partial<ValoresDoAlvo>, object][])('PATCH só envia o campo alterado %j', (mudanca, esperado) => {
    const original = entrada({ modoHeaders: 'manter' })
    expect(paraAlteracao(original, { ...original, ...mudanca })).toEqual(esperado)
  })

  it('PATCH compara o limiar efetivo e só toca nos headers quando solicitado', () => {
    const original = entrada({ modoHeaders: 'manter' })
    expect(paraAlteracao(original, original)).toEqual({})
    expect(paraAlteracao(original, { ...original, lentidao: 'personalizado', degraded_threshold_ms: 2000 })).toEqual({})
    expect(paraAlteracao(original, { ...original, lentidao: 'desativado' })).toEqual({ degraded_threshold_ms: null })
    expect(paraAlteracao(original, { ...original, lentidao: 'personalizado', degraded_threshold_ms: 800 })).toEqual({ degraded_threshold_ms: 800 })
    expect(paraAlteracao(original, { ...original, modoHeaders: 'substituir', headers: [{ nome: 'X-A', valor: '1' }] })).toEqual({ headers: { 'X-A': '1' } })
    expect(paraAlteracao(original, { ...original, modoHeaders: 'remover' })).toEqual({ headers: null })
  })

  it('aviso de headers compara a origem do navegador, e URL guardada ilegível sempre avisa', () => {
    expect(apagaOsHeaders(['Authorization'], 'não é URL', { url: 'também inválida' })).toBe(true)
    expect(apagaOsHeaders(['Authorization'], 'https://api.exemplo.com/', { url: 'https://API.exemplo.com:443/outro' })).toBe(false)
    expect(apagaOsHeaders(['Authorization'], 'https://api.exemplo.com/', { url: 'https://api.exemplo.com:8443/' })).toBe(true)
    expect(apagaOsHeaders(['Authorization'], 'https://api.exemplo.com/', { url: 'https://outro.exemplo.com/', headers: {} })).toBe(false)
    expect(apagaOsHeaders([], 'https://api.exemplo.com/', { url: 'https://outro.exemplo.com/' })).toBe(false)
    expect(apagaOsHeaders(['Authorization'], 'https://api.exemplo.com/', { name: 'Outro' })).toBe(false)
  })
})

describe('tags no formulário', () => {
  // Espelho de internal/targets/tags_test.go. Sabotagem: tirar erroNasTags.
  it.each([
    ['', null],
    ['prod, api', null],
    ['Produção, api-v2_br', null],
    ['a b', 'Use letras, números, hífen e sublinhado, separados por vírgula.'],
    ['a/b', 'Use letras, números, hífen e sublinhado, separados por vírgula.'],
    ['produc\u0327a\u0303o', 'Use letras, números, hífen e sublinhado, separados por vírgula.'],
    ['a'.repeat(25), 'Cada tag tem até 24 caracteres.'],
    ['a,b,c,d,e,f,g,h,i', 'Até 8 tags.'],
  ])('"%s"', (tagsTexto, mensagem) => {
    const r = esquemaAlvo.safeParse(entrada({ tagsTexto }))
    expect(r.success ? null : r.error.issues[0]?.message).toBe(mensagem)
  })

  it('repetir não conta para o teto', () => {
    expect(esquemaAlvo.safeParse(entrada({ tagsTexto: 'a,b,c,d,e,f,g,h,a,A' })).success).toBe(true)
  })

  it('a criação manda as tags normalizadas, e não manda o campo vazio', () => {
    expect(paraCriacao(entrada({ tagsTexto: ' Prod, api,prod,, ' })).tags).toEqual(['api', 'prod'])
    expect(paraCriacao(entrada())).not.toHaveProperty('tags')
  })

  // Sabotagem: mandar tags sempre → todo PATCH reescreveria as tags.
  it('o PATCH só leva as tags se o conjunto mudou, e limpar é mandar lista vazia', () => {
    const original = entrada({ tagsTexto: 'api, prod' })
    expect(paraAlteracao(original, entrada({ tagsTexto: 'PROD,api' }))).not.toHaveProperty('tags')
    expect(paraAlteracao(original, entrada({ tagsTexto: 'api' })).tags).toEqual(['api'])
    expect(paraAlteracao(original, entrada({ tagsTexto: '' })).tags).toEqual([])
  })
})

describe('regra de conteúdo', () => {
  const ligada = (p: Partial<ValoresDoAlvo> = {}) => entrada({ modoDaRegra: 'contains', valorDaRegra: 'ok', ...p })

  function erros(v: ValoresDoAlvo) {
    const r = esquemaAlvo.safeParse(v)
    return r.success ? [] : r.error.issues.map((e) => [e.path.join('.'), e.message])
  }

  it('desligada não manda content_rule na criação', () => {
    expect(paraCriacao(entrada())).not.toHaveProperty('content_rule')
  })

  it('ligada manda modo e valor; só json_equals manda o ponteiro', () => {
    expect(paraCriacao(ligada({ ponteiroDaRegra: '/sobra' })).content_rule).toEqual({ mode: 'contains', value: 'ok' })
    expect(paraCriacao(ligada({ modoDaRegra: 'not_contains', valorDaRegra: 'erro' })).content_rule).toEqual({
      mode: 'not_contains', value: 'erro',
    })
    expect(
      paraCriacao(ligada({ modoDaRegra: 'json_equals', valorDaRegra: 'up', ponteiroDaRegra: '/status' })).content_rule,
    ).toEqual({ mode: 'json_equals', value: 'up', json_pointer: '/status' })
  })

  // O servidor compara bytes: os espaços das pontas fazem parte do valor.
  it('o valor não perde os espaços das pontas', () => {
    expect(esquemaAlvo.parse(ligada({ valorDaRegra: '  ok  ' })).valorDaRegra).toBe('  ok  ')
    expect(paraCriacao(ligada({ valorDaRegra: '  ok  ' })).content_rule).toEqual({ mode: 'contains', value: '  ok  ' })
  })

  it('desligada não valida o valor nem o ponteiro escondidos', () => {
    expect(erros(entrada({ valorDaRegra: 'x'.repeat(300), ponteiroDaRegra: 'sem barra' }))).toEqual([])
  })

  it('ligada sem valor é "Obrigatório."', () => {
    expect(erros(ligada({ valorDaRegra: '' }))).toEqual([['valorDaRegra', 'Obrigatório.']])
  })

  // L9 do STRIDE: o servidor conta runas (char_length), não unidades UTF-16.
  it.each([
    ['256 letras', 'a'.repeat(256), []],
    ['257 letras', 'a'.repeat(257), [['valorDaRegra', 'Até 256 caracteres.']]],
    ['256 emojis', '🙂'.repeat(256), []],
    ['257 emojis', '🙂'.repeat(257), [['valorDaRegra', 'Até 256 caracteres.']]],
  ])('valor com %s', (_caso, valorDaRegra, esperado) => {
    expect(erros(ligada({ valorDaRegra }))).toEqual(esperado)
  })

  it.each([['tab', 'a\tb'], ['quebra de linha', 'a\nb'], ['NUL', 'a\u0000b'], ['DEL', 'a\u007fb']])(
    'valor com %s é recusado',
    (_caso, valorDaRegra) => {
      expect(erros(ligada({ valorDaRegra }))).toEqual([
        ['valorDaRegra', 'Sem tab, quebra de linha ou outro caractere de controle.'],
      ])
    },
  )

  it.each([
    ['/status', '/status', []],
    ['só a barra', '/', []],
    ['com índice', '/itens/0/nome', []],
    ['com escapes', '/a~0b~1c', []],
    ['de 256 emojis', '/' + '🙂'.repeat(255), []],
    ['vazio', '', [['ponteiroDaRegra', 'Obrigatório.']]],
    ['sem barra', 'status', [['ponteiroDaRegra', 'Comece com /, como /status.']]],
    ['com ~2', '/a~2', [['ponteiroDaRegra', 'Use ~0 para ~ e ~1 para /.']]],
    ['com ~ no fim', '/a~', [['ponteiroDaRegra', 'Use ~0 para ~ e ~1 para /.']]],
    ['de 257 emojis', '/' + '🙂'.repeat(256), [['ponteiroDaRegra', 'Até 256 caracteres.']]],
    ['com tab', '/a\tb', [['ponteiroDaRegra', 'Sem tab, quebra de linha ou outro caractere de controle.']]],
  ])('ponteiro %s', (_caso, ponteiroDaRegra, esperado) => {
    expect(erros(ligada({ modoDaRegra: 'json_equals', ponteiroDaRegra }))).toEqual(esperado)
  })

  it('com HEAD a regra é recusada no seletor', () => {
    expect(erros(ligada({ method: 'HEAD' }))).toEqual([
      ['modoDaRegra', 'Com HEAD a resposta não tem corpo. Escolha GET ou POST para verificar o conteúdo.'],
    ])
    expect(erros(entrada({ method: 'HEAD' }))).toEqual([])
  })

  it.each(['Accept-Encoding', 'ACCEPT-ENCODING', 'accept-encoding'])('com o header %s a regra é recusada', (nome) => {
    const headers = [{ nome, valor: 'gzip' }]
    expect(erros(ligada({ headers }))).toEqual([
      ['modoDaRegra', 'Remova o header Accept-Encoding para verificar o conteúdo: a resposta viria comprimida.'],
    ])
    expect(erros(entrada({ headers }))).toEqual([])
    // "Manter como está" não manda headers: quem confere é o servidor.
    expect(erros(ligada({ headers, modoHeaders: 'manter' }))).toEqual([])
  })

  it('valoresDe lê a regra do alvo', () => {
    const a: Alvo = {
      id: 'alvo-1', name: 'Site', url: 'https://site.exemplo.com/', method: 'GET',
      interval_sec: 60, timeout_ms: 2000, expected_status: 200, degraded_threshold_ms: 2000,
      enabled: true, header_names: [], tags: [], created_at: '2026-10-05T00:00:00Z',
      content_rule: { mode: 'json_equals', value: 'up', json_pointer: '/status' },
    }
    expect(valoresDe(a)).toMatchObject({ modoDaRegra: 'json_equals', valorDaRegra: 'up', ponteiroDaRegra: '/status' })
    expect(valoresDe({ ...a, content_rule: { mode: 'contains', value: 'ok', json_pointer: null } })).toMatchObject({
      modoDaRegra: 'contains', valorDaRegra: 'ok', ponteiroDaRegra: '',
    })
  })

  // Sabotagem: tirar o null de paraAlteracao → a regra nunca é limpa.
  it('PATCH de regra para desligada manda content_rule: null', () => {
    expect(paraAlteracao(ligada({ modoHeaders: 'manter' }), entrada({ modoHeaders: 'manter' }))).toEqual({ content_rule: null })
  })

  it('PATCH sem mudança na regra não manda o campo', () => {
    const original = ligada({ modoHeaders: 'manter' })
    expect(paraAlteracao(original, { ...original })).toEqual({})
    // Fora de json_equals o ponteiro não faz parte da regra.
    expect(paraAlteracao(original, { ...original, ponteiroDaRegra: '/outro' })).toEqual({})
    const desligada = entrada({ modoHeaders: 'manter' })
    expect(paraAlteracao(desligada, { ...desligada, valorDaRegra: 'rascunho' })).toEqual({})
  })

  it('PATCH com a regra mudada manda a regra inteira', () => {
    const original = ligada({ modoHeaders: 'manter' })
    expect(paraAlteracao(original, { ...original, valorDaRegra: 'novo' })).toEqual({
      content_rule: { mode: 'contains', value: 'novo' },
    })
    expect(paraAlteracao(original, { ...original, modoDaRegra: 'json_equals', ponteiroDaRegra: '/s' })).toEqual({
      content_rule: { mode: 'json_equals', value: 'ok', json_pointer: '/s' },
    })
    const desligada = entrada({ modoHeaders: 'manter' })
    expect(paraAlteracao(desligada, { ...desligada, modoDaRegra: 'not_contains', valorDaRegra: 'erro' })).toEqual({
      content_rule: { mode: 'not_contains', value: 'erro' },
    })
  })

  it.each([
    ['content_rule', 'modoDaRegra'],
    ['content_rule.mode', 'modoDaRegra'],
    ['content_rule.value', 'valorDaRegra'],
    ['content_rule.json_pointer', 'ponteiroDaRegra'],
  ])('o erro %s do servidor cai em %s', (campo, noFormulario) => {
    expect(campoDoServidor[campo]).toBe(noFormulario)
  })
})
