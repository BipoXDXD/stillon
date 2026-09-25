// Aplica o tema salvo antes da primeira pintura, para o painel não piscar
// claro antes de ficar escuro. Mesma chave e mesma regra de src/lib/theme.tsx.
;(function () {
  var tema = 'sistema'
  try {
    tema = localStorage.getItem('stillon.tema') || 'sistema'
  } catch {}
  var escuro =
    tema === 'escuro' || (tema === 'sistema' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', escuro)
})()
