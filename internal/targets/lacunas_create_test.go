package targets_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// Lacunas de cobertura de Service.Create deixadas de fora antes da refatoração
// que o divide em passos. Só olham o que a API do pacote devolve e o que fica
// gravado, não as funções internas: valem antes e depois dela.

// Cada campo informado, longe do padrão, chega gravado e igual ao que o Create
// devolve: método, intervalo, timeout, status, limiar, pausa, aviso de
// certificado, tags (normalizadas e ordenadas) e regra de conteúdo.
func TestCreateGravaCadaCampoInformadoEDevolveOMesmoQueOGetLe(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := targets.Input{
		Name: "Pagamentos", URL: "https://pagamentos.exemplo.com/saude",
		Method: "POST", IntervalSec: 300, TimeoutMS: 5000, ExpectedStatus: 204,
		DegradedThresholdMS: ptr[int32](1500),
		Enabled:             false, TLSWarn: true,
		Headers:     map[string]string{"X-Chave": "valor-secreto"},
		Tags:        []string{"zeta", "alfa"},
		ContentRule: &targets.ContentRule{Mode: targets.ContentContains, Value: "tudo certo"},
	}

	criado, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}
	lido, err := a.svc.Get(ctx, dono, criado.ID)
	if err != nil {
		t.Fatal(err)
	}

	for nome, par := range map[string][2]any{
		"Name":           {criado.Name, lido.Name},
		"URL":            {criado.URL, lido.URL},
		"Method":         {criado.Method, lido.Method},
		"IntervalSec":    {criado.IntervalSec, lido.IntervalSec},
		"TimeoutMS":      {criado.TimeoutMS, lido.TimeoutMS},
		"ExpectedStatus": {criado.ExpectedStatus, lido.ExpectedStatus},
		"Enabled":        {criado.Enabled, lido.Enabled},
		"TLSWarn":        {criado.TLSWarn, lido.TLSWarn},
	} {
		if par[0] != par[1] {
			t.Errorf("%s: Create devolveu %v, Get leu %v", nome, par[0], par[1])
		}
	}
	if lido.Method != "POST" || lido.IntervalSec != 300 || lido.TimeoutMS != 5000 || lido.ExpectedStatus != 204 {
		t.Errorf("os valores informados não ficaram: %+v", lido)
	}
	if lido.Enabled || !lido.TLSWarn {
		t.Errorf("Enabled = %v e TLSWarn = %v, queria false e true", lido.Enabled, lido.TLSWarn)
	}
	for nome, alvo := range map[string]targets.Target{"Create": criado, "Get": lido} {
		if alvo.DegradedThresholdMS == nil || *alvo.DegradedThresholdMS != 1500 {
			t.Errorf("%s: DegradedThresholdMS = %v, queria 1500", nome, alvo.DegradedThresholdMS)
		}
		if !slices.Equal(alvo.Tags, []string{"alfa", "zeta"}) {
			t.Errorf("%s: Tags = %v, queria [alfa zeta]", nome, alvo.Tags)
		}
		if !slices.Equal(alvo.HeaderNames, []string{"X-Chave"}) {
			t.Errorf("%s: HeaderNames = %v, queria [X-Chave]", nome, alvo.HeaderNames)
		}
		if alvo.ContentRule == nil || alvo.ContentRule.Mode != targets.ContentContains || alvo.ContentRule.Value != "tudo certo" {
			t.Errorf("%s: ContentRule = %+v, queria contains \"tudo certo\"", nome, alvo.ContentRule)
		}
	}
	if !criado.CreatedAt.Equal(a.clk.Now()) || !lido.CreatedAt.Equal(a.clk.Now()) {
		t.Errorf("CreatedAt = %v (Create) e %v (Get), queria o relógio (%v)", criado.CreatedAt, lido.CreatedAt, a.clk.Now())
	}
}

// Sem limiar, tags nem regra, o alvo nasce sem nenhum dos três (as tags vêm
// como lista vazia, nunca nil) e com Enabled/TLSWarn como o cadastro mandou.
func TestCreateSemLimiarTagsNemRegraNasceSemEles(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)

	criado, err := a.svc.Create(ctx, dono, targets.Input{Name: "Simples", URL: "https://simples.exemplo.com/", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	lido, err := a.svc.Get(ctx, dono, criado.ID)
	if err != nil {
		t.Fatal(err)
	}

	for nome, alvo := range map[string]targets.Target{"Create": criado, "Get": lido} {
		if alvo.DegradedThresholdMS != nil || alvo.ContentRule != nil {
			t.Errorf("%s: limiar = %v e regra = %+v, queria os dois nil", nome, alvo.DegradedThresholdMS, alvo.ContentRule)
		}
		if alvo.Tags == nil || len(alvo.Tags) != 0 {
			t.Errorf("%s: Tags = %#v, queria lista vazia não nil", nome, alvo.Tags)
		}
		if !alvo.Enabled || alvo.TLSWarn {
			t.Errorf("%s: Enabled = %v e TLSWarn = %v, queria true e false", nome, alvo.Enabled, alvo.TLSWarn)
		}
	}
}

// O gancho depoisDeContar roda uma vez por Create, depois das contagens e
// ainda antes da decisão do teto: roda mesmo quando o teto recusa, e não roda
// quando a validação recusa antes de qualquer contagem.
func TestCreateChamaODepoisDeContarUmaVezPorChamadaQueChegaAContar(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	var chamadas int
	a.svc.SetDepoisDeContar(func() { chamadas++ })

	conta := func(nome string, in targets.Input, queria int) {
		t.Helper()
		chamadas = 0
		_, _ = a.svc.Create(ctx, dono, in)
		if chamadas != queria {
			t.Errorf("%s: depoisDeContar rodou %d vez(es), queria %d", nome, chamadas, queria)
		}
	}

	conta("sem regra", semRegra("https://a.exemplo.com/"), 1)
	conta("com regra", comRegra("https://b.exemplo.com/"), 1)
	invalida := entrada()
	invalida.URL = "http://127.0.0.1/"
	conta("validação recusa", invalida, 0)

	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-2)
	conta("teto recusa", entrada(), 1)
}

// O erro do teto sai como o próprio valor, sem o embrulho "gravar alvo", para
// a camada HTTP o reconhecer e para a mensagem não carregar ruído.
func TestCreateDevolveOErroDoTetoSemEmbrulho(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser))

	_, err := a.svc.Create(context.Background(), dono, entrada())

	if err != targets.ErrLimitReached {
		t.Fatalf("erro = %v, queria o próprio ErrLimitReached sem embrulho", err)
	}
}

// Quando os dois tetos estouram juntos, o do usuário vence: o alvo novo, com
// regra, cairia no teto por host, mas o dono já está no máximo de alvos e o
// erro devolvido é ErrLimitReached, nunca ErrContentHostLimit.
func TestCreateComOsDoisTetosEstouradosDevolveOTetoDoUsuario(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherComRegra(t, dono, "https://example.com", targets.DefaultContentRulesPerHost, true)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-targets.DefaultContentRulesPerHost)

	_, err := a.svc.Create(context.Background(), dono, comRegra("https://example.com/nova"))

	if err != targets.ErrLimitReached {
		t.Fatalf("erro = %v, queria ErrLimitReached (teto do usuário tem precedência sobre o do host)", err)
	}
}

// Falha de infraestrutura na transação vira "gravar alvo: ..." (com a causa
// preservada) e não grava linha nenhuma.
func TestCreateEmbrulhaFalhaDeGravacaoEPreservaACausa(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()

	_, err := a.svc.Create(ctx, dono, entrada())

	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("erro = %v, queria algo que embrulhe context.Canceled", err)
	}
	if !strings.HasPrefix(err.Error(), "gravar alvo: ") {
		t.Errorf("erro = %q, queria o prefixo \"gravar alvo: \"", err)
	}
	if errors.Is(err, targets.ErrLimitReached) || errors.Is(err, targets.ErrContentHostLimit) {
		t.Errorf("a falha de gravação foi confundida com um teto: %v", err)
	}
	var linhas int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&linhas); err != nil {
		t.Fatal(err)
	}
	if linhas != 0 {
		t.Errorf("%d linhas gravadas, queria nenhuma", linhas)
	}
}
