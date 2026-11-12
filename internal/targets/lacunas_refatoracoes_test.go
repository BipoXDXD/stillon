package targets_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// Lacunas de cobertura de Service.Update e Service.Summary deixadas de fora
// dos testes das refatorações (#306 e #310). Todos os testes aqui olham só o
// que a API do pacote devolve e o que fica gravado, não as funções internas:
// valem antes e depois de as refatorações entrarem.

func ligarTLSWarn(t *testing.T, a ambiente, dono uuid.UUID, ligado bool) targets.Target {
	t.Helper()
	in := entrada()
	in.TLSWarn = ligado
	alvo, err := a.svc.Create(context.Background(), dono, in)
	if err != nil {
		t.Fatal(err)
	}
	return alvo
}

// O PATCH com tls_warn grava o valor enviado, nos dois sentidos, e a leitura
// seguinte o devolve.
func TestUpdateTLSWarnGravaOValorEnviado(t *testing.T) {
	casos := []struct {
		nome          string
		antes, enviar bool
	}{
		{"liga o aviso que estava desligado", false, true},
		{"desliga o aviso que estava ligado", true, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			ctx := context.Background()
			dono := a.usuario(t)
			alvo := ligarTLSWarn(t, a, dono, c.antes)

			devolvido, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{TLSWarn: &c.enviar})
			if err != nil {
				t.Fatal(err)
			}
			if devolvido.TLSWarn != c.enviar {
				t.Errorf("Update devolveu TLSWarn = %v, queria %v", devolvido.TLSWarn, c.enviar)
			}
			lido, err := a.svc.Get(ctx, dono, alvo.ID)
			if err != nil {
				t.Fatal(err)
			}
			if lido.TLSWarn != c.enviar {
				t.Errorf("Get devolveu TLSWarn = %v, queria %v", lido.TLSWarn, c.enviar)
			}
		})
	}
}

// Um PATCH que não cita tls_warn mantém o que estava gravado.
func TestUpdateSemTLSWarnMantemOAvisoGravado(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo := ligarTLSWarn(t, a, dono, true)

	devolvido, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{Name: ptr("Outro nome")})
	if err != nil {
		t.Fatal(err)
	}
	if !devolvido.TLSWarn {
		t.Error("Update devolveu TLSWarn = false depois de um PATCH que não o citou")
	}
	lido, err := a.svc.Get(ctx, dono, alvo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !lido.TLSWarn {
		t.Error("Get devolveu TLSWarn = false depois de um PATCH que não o citou")
	}
}

// exigirAlvoIntacto relê o alvo e exige que nada do PATCH recusado tenha
// ficado gravado: o erro da transação desfaz também o UPDATE que já rodou.
func exigirAlvoIntacto(t *testing.T, a ambiente, dono, id uuid.UUID, nome, url string, tags []string) {
	t.Helper()
	lido, err := a.svc.Get(context.Background(), dono, id)
	if err != nil {
		t.Fatal(err)
	}
	if lido.Name != nome || lido.URL != url || !slices.Equal(lido.Tags, tags) {
		t.Errorf("alvo depois do PATCH recusado = {Name:%q URL:%q Tags:%v}; queria {%q %q %v}",
			lido.Name, lido.URL, lido.Tags, nome, url, tags)
	}
}

const (
	renomearTags = `ALTER TABLE target_tags RENAME TO target_tags_indisponivel_lacuna`
	voltarTags   = `ALTER TABLE target_tags_indisponivel_lacuna RENAME TO target_tags`
	renomearTLS  = `ALTER TABLE target_tls RENAME TO target_tls_indisponivel_lacuna`
	voltarTLS    = `ALTER TABLE target_tls_indisponivel_lacuna RENAME TO target_tls`
)

// Sem conseguir ler as tags do alvo, o PATCH falha com a causa do banco. O
// lerTags roda antes de qualquer escrita do Update: não há UPDATE a desfazer, e
// por isso o teste não afirma "nada fica gravado", só a causa e o prefixo.
func TestUpdateFalhaAoLerAsTagsComACausaDoBanco(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	alterarDDLComplementar(t, a, renomearTags, voltarTags)

	_, err := a.svc.Update(context.Background(), dono, alvo, targets.Patch{Name: ptr("Novo nome")})

	exigirPGComplementar(t, err, "42P01", "ler tags do alvo: ")
}

// O PATCH que troca o host precisa apagar a validade do certificado antigo. Se
// o apagar falha, o UPDATE da URL, que já rodou, é desfeito junto: nunca fica
// a URL nova com a validade do host antigo.
func TestUpdateFalhaAoApagarAValidadeDesfazOUpdateDaURL(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	a.gravarValidade(t, alvo, a.clk.Now().Add(30*24*time.Hour), a.clk.Now())
	restaurar := alterarDDLComplementar(t, a, renomearTLS, voltarTLS)

	_, err := a.svc.Update(context.Background(), dono, alvo, targets.Patch{URL: ptr("https://outro.exemplo.com/health")})

	exigirPGComplementar(t, err, "42P01", "apagar validade do certificado: ")
	restaurar()
	exigirAlvoIntacto(t, a, dono, alvo, "API", entrada().URL, []string{})
	if !a.temValidade(t, alvo) {
		t.Error("a validade do certificado sumiu apesar do PATCH recusado")
	}
}

// Sem conseguir ler a validade depois de gravar o alvo, o PATCH falha e o
// UPDATE que já rodou é desfeito.
func TestUpdateFalhaAoLerAValidadeDesfazOUpdate(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	alvo := a.alvo(t, dono)
	restaurar := alterarDDLComplementar(t, a, renomearTLS, voltarTLS)

	_, err := a.svc.Update(context.Background(), dono, alvo, targets.Patch{Name: ptr("Novo nome")})

	exigirPGComplementar(t, err, "42P01", "ler validade do certificado: ")
	restaurar()
	exigirAlvoIntacto(t, a, dono, alvo, "API", entrada().URL, []string{})
}

// A troca de tags é parte do PATCH: se apagar as antigas ou gravar as novas
// falha, o erro vem com a causa do banco e o PATCH inteiro é desfeito, nome e
// tags antigas inclusive.
func TestUpdateFalhaAoTrocarTagsDesfazOPatchInteiro(t *testing.T) {
	casos := []struct {
		nome, operacao, prefixo string
	}{
		{"apagar as tags antigas", "DELETE", "apagar tags do alvo: "},
		{"gravar as tags novas", "INSERT", "gravar tags do alvo: "},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			dono := a.usuario(t)
			in := entrada()
			in.Tags = []string{"prod"}
			criado, err := a.svc.Create(context.Background(), dono, in)
			if err != nil {
				t.Fatal(err)
			}
			restaurar := alterarDDLComplementar(t, a,
				`CREATE FUNCTION recusar_tags_lacuna() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					RAISE EXCEPTION 'tags recusadas pelo teste' USING ERRCODE = '23514';
				END $$;
				CREATE TRIGGER recusar_tags_lacuna BEFORE `+c.operacao+` ON target_tags
					FOR EACH ROW EXECUTE FUNCTION recusar_tags_lacuna();`,
				`DROP TRIGGER recusar_tags_lacuna ON target_tags; DROP FUNCTION recusar_tags_lacuna();`)

			_, err = a.svc.Update(context.Background(), dono, criado.ID,
				targets.Patch{Name: ptr("Novo nome"), SetTags: true, Tags: []string{"a", "b"}})

			exigirPGComplementar(t, err, "23514", c.prefixo)
			restaurar()
			exigirAlvoIntacto(t, a, dono, criado.ID, "API", entrada().URL, []string{"prod"})
		})
	}
}

// idsDoResumo devolve os ids do resumo em ordem fixa, para comparar conjuntos
// sem depender da ordem da lista.
func idsDoResumo(itens []targets.Summary) []string {
	ids := make([]string, 0, len(itens))
	for _, s := range itens {
		ids = append(ids, s.ID.String())
	}
	slices.Sort(ids)
	return ids
}

func idsEsperados(ids ...uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	slices.Sort(out)
	return out
}

func resumoFiltrado(t *testing.T, a ambiente, dono uuid.UUID, tags, estados []string) []targets.Summary {
	t.Helper()
	filtro, verr := targets.ParseSummaryFilter(tags, estados)
	if verr != nil {
		t.Fatalf("filtro recusado: %v", verr)
	}
	itens, err := a.svc.Summary(context.Background(), dono, filtro)
	if err != nil {
		t.Fatal(err)
	}
	return itens
}

func alvoComTags(t *testing.T, a ambiente, dono uuid.UUID, tags ...string) uuid.UUID {
	t.Helper()
	in := entrada()
	in.Tags = tags
	alvo, err := a.svc.Create(context.Background(), dono, in)
	if err != nil {
		t.Fatal(err)
	}
	return alvo.ID
}

// O filtro de tags do resumo é E: o alvo precisa ter todas. Quem tem só uma
// delas, ou nenhuma, fica de fora.
func TestSummaryFiltroDeTagsExigeTodasAsTags(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	comAmbas := alvoComTags(t, a, dono, "prod", "api")
	alvoComTags(t, a, dono, "prod")
	alvoComTags(t, a, dono)

	itens := resumoFiltrado(t, a, dono, []string{"prod", "api"}, nil)

	if got, want := idsDoResumo(itens), idsEsperados(comAmbas); !slices.Equal(got, want) {
		t.Errorf("resumo com tags prod+api = %v, queria só %v", got, want)
	}
}

// O filtro de estados do resumo é OU: serve qualquer um dos pedidos, e os
// demais estados ficam de fora.
func TestSummaryFiltroDeEstadosAceitaQualquerUmDosPedidos(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	no := a.alvo(t, dono)
	fora := a.alvo(t, dono)
	semCheck := a.alvo(t, dono)
	a.gravarCheck(t, no, a.clk.Now().Add(-time.Second), "up", 50)
	a.gravarCheck(t, fora, a.clk.Now().Add(-time.Second), "down", 5000)

	itens := resumoFiltrado(t, a, dono, nil, []string{"up", "down"})

	if got, want := idsDoResumo(itens), idsEsperados(no, fora); !slices.Equal(got, want) {
		t.Errorf("resumo com estados up ou down = %v, queria %v (sem o alvo %s, que está unknown)", got, want, semCheck)
	}
}

// O estado "paused" do filtro é o do alvo desativado, mesmo que o último
// check dele tenha sido up: o resumo o pede pelo enabled, não pelo check.
func TestSummaryFiltroPausadoDevolveSoOAlvoDesativado(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	ativo := a.alvo(t, dono)
	in := entrada()
	in.Enabled = false
	pausado, err := a.svc.Create(context.Background(), dono, in)
	if err != nil {
		t.Fatal(err)
	}
	a.gravarCheck(t, ativo, a.clk.Now().Add(-time.Second), "up", 50)
	a.gravarCheck(t, pausado.ID, a.clk.Now().Add(-time.Second), "up", 50)

	itens := resumoFiltrado(t, a, dono, nil, []string{targets.EstadoPausado})

	if got, want := idsDoResumo(itens), idsEsperados(pausado.ID); !slices.Equal(got, want) {
		t.Errorf("resumo com estado paused = %v, queria só %v", got, want)
	}
}
