package auth

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// As provas das rotas autenticadas (A3 do Plano 10): a troca de senha e o
// enroll, confirm, disable, rotate e rotate/confirm do 2FA. Antes elas
// consultavam e alimentavam o bloqueio da CONTA, e um anônimo que mandasse
// cinco senhas erradas ao login a cada 15 min desligava, junto com o login,
// tudo o que o dono logado podia fazer com a própria credencial.
//
// Agora cada família de sessão tem o seu contador, em memória. A quinta prova
// errada derruba a família (a rota responde 401 unauthorized): quem tem a
// sessão e chuta a senha é expulso, e o dono não é trancado por isso. A
// reserva vem ANTES da verificação (A2): N requisições simultâneas gastam N
// provas, e as que passam do limite nem chegam a verificar.
//
// Em memória de propósito: zera num restart, e um processo por vez atende a
// API. O preço está registrado nas decisões do Plano 10.

// FamilyProofLimit é quantas provas erradas uma família aguenta; a última a
// derruba. É o mesmo limiar do bloqueio da conta.
const FamilyProofLimit = LockThreshold

// familiaParada é quanto uma família sem nenhuma reserva fica no contador. O
// access token vive 15 min e o refresh o renova; uma família que ninguém usa
// há um dia não precisa da conta de ontem, e o mapa não cresce para sempre.
const familiaParada = 24 * time.Hour

// FamilyProofs é o contador de provas por família de sessão. O valor zero não
// serve: use NewFamilyProofs.
type FamilyProofs struct {
	clk    clock.Clock
	mu     sync.Mutex
	contas map[uuid.UUID]*contaDeProvas
}

type contaDeProvas struct {
	// reservadas conta as provas em voo e as erradas: uma reserva só sai
	// daqui por Release (não chegou a errar) ou Succeed (zera tudo).
	reservadas int
	visto      time.Time
}

func NewFamilyProofs(clk clock.Clock) *FamilyProofs {
	return &FamilyProofs{clk: clk, contas: map[uuid.UUID]*contaDeProvas{}}
}

// ProofReservation é uma prova reservada. Quem reserva chama exatamente um
// entre Fail e Succeed quando julga a prova, e sempre Release no fim (com
// defer): depois de Fail ou Succeed, Release não faz nada.
type ProofReservation struct {
	f       *FamilyProofs
	familia uuid.UUID
	n       int
	fechada bool
}

// Reserve reserva uma prova para a família. A reserva pode nascer esgotada
// (Exhausted): a família já errou o limite, e quem chama não verifica nada.
func (f *FamilyProofs) Reserve(familyID uuid.UUID) *ProofReservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	agora := f.clk.Now()
	for id, c := range f.contas {
		if agora.Sub(c.visto) > familiaParada {
			delete(f.contas, id)
		}
	}
	c, ok := f.contas[familyID]
	if !ok {
		c = &contaDeProvas{}
		f.contas[familyID] = c
	}
	c.reservadas++
	c.visto = agora
	return &ProofReservation{f: f, familia: familyID, n: c.reservadas}
}

// FamilyID é a família da reserva.
func (r *ProofReservation) FamilyID() uuid.UUID { return r.familia }

// Exhausted diz que a família já tinha errado o limite quando esta reserva
// foi feita: não verifique nada, derrube a família (de novo, se preciso).
func (r *ProofReservation) Exhausted() bool { return r.n > FamilyProofLimit }

// Fail registra a prova errada, que fica contada. Devolve true quando ela é
// a do limite: quem chama derruba a família.
func (r *ProofReservation) Fail() (derrubar bool) {
	r.fechada = true
	return r.n >= FamilyProofLimit
}

// Succeed zera a família: as provas pedidas conferiram.
func (r *ProofReservation) Succeed() {
	r.fechada = true
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	delete(r.f.contas, r.familia)
}

// Release devolve a reserva de quem não chegou a errar prova (corpo inválido
// depois da reserva, 403, 500, sessão caída). Depois de Fail ou Succeed, não
// faz nada.
func (r *ProofReservation) Release() {
	if r.fechada {
		return
	}
	r.fechada = true
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if c, ok := r.f.contas[r.familia]; ok && c.reservadas > 0 {
		c.reservadas--
	}
}
