package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// provasDaFamilia é o contador de provas erradas das rotas autenticadas (A3 do
// Plano 10) e o que fazer quando ele estoura. Uma instância só, dividida pela
// troca de senha e pelas cinco rotas do 2FA: com uma por rota, quem chuta a
// senha multiplicaria o orçamento alternando as rotas.
type provasDaFamilia struct {
	contador *auth.FamilyProofs
	contas   *account.Service
	proxies  middleware.Proxies
}

// reservar reserva uma prova para a família do token, antes de verificar
// qualquer coisa. Devolve nil depois de já ter respondido, quando a família
// estava esgotada: aí nada é verificado (nem o Argon2id roda), e a família é
// derrubada de novo, se ainda estiver de pé.
//
// Quem recebe a reserva chama Fail (por errou) ou Succeed ao julgar as provas,
// e sempre Release com defer.
func (p *provasDaFamilia) reservar(w http.ResponseWriter, r *http.Request, userID, familyID uuid.UUID) *auth.ProofReservation {
	res := p.contador.Reserve(familyID)
	if res.Exhausted() {
		res.Release()
		p.derrubar(w, r, userID, familyID)
		return nil
	}
	return res
}

// errou registra a prova errada. Até a penúltima, a resposta é a de sempre da
// rota (401 invalid_credentials; 400 validation_failed no confirm do 2FA). A
// última derruba a família e responde 401 unauthorized: o painel renova a
// sessão, a renovação falha, e quem estava chutando volta ao login.
func (p *provasDaFamilia) errou(w http.ResponseWriter, r *http.Request, res *auth.ProofReservation, userID uuid.UUID, codigo apierr.ErrorCode) {
	if res.Fail() {
		p.derrubar(w, r, userID, res.FamilyID())
		return
	}
	apierr.WriteError(w, r, codigo)
}

func (p *provasDaFamilia) derrubar(w http.ResponseWriter, r *http.Request, userID, familyID uuid.UUID) {
	if err := p.contas.RevokeFamilyForWrongProofs(r.Context(), userID, familyID,
		auth.Meta{IP: middleware.ClientIP(r, p.proxies), UserAgent: r.UserAgent()}); err != nil {
		slog.Error("derrubar família por provas erradas", "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
		return
	}
	apierr.WriteError(w, r, apierr.CodeUnauthorized)
}
