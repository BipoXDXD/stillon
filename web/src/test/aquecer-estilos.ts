// O jsdom só analisa a folha de estilos padrão no primeiro getComputedStyle, e
// esse custo cairia no primeiro clique do arquivo, dentro do prazo do teste.
// Quem importa este módulo paga o custo na coleta, que não tem prazo.
getComputedStyle(document.documentElement)
