// Package legal guarda a versão dos textos legais (Termos de Uso e Política
// de Privacidade) que o cadastro exige aceitar.
package legal

// VersaoAtual é a data da versão vigente dos termos. O painel lê a mesma
// versão de web/src/features/legal/versao.json e a envia no cadastro; um
// teste confere que as duas andam juntas. Mudar os textos é mudar as duas.
const VersaoAtual = "2026-11-28"
