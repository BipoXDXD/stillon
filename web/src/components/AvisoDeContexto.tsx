// O painel EXIGE contexto seguro (https, ou http em localhost). Fora dele o
// navegador não oferece navigator.locks: duas abas renovando juntas
// apresentam o mesmo refresh, a detecção de reúso do servidor derruba a
// família, e a sessão cai. Não há trava substituta (localStorage não é
// exclusivo entre abas); o painel avisa, e quem opera põe HTTPS na frente.
export function AvisoDeContexto() {
  if (typeof window === 'undefined' || window.isSecureContext) return null
  return (
    <div role="alert" className="border-b border-perigo bg-painel px-4 py-3 text-sm">
      <p className="mx-auto max-w-6xl">
        <strong>Este painel precisa de HTTPS (ou de localhost).</strong> Sem conexão segura, o navegador não coordena a
        sessão entre abas: com mais de uma aba aberta, a sessão cai. Peça a quem opera o StillOn para servir o painel
        por HTTPS.
      </p>
    </div>
  )
}
