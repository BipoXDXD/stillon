package httpapi_test

import (
	"context"
	"maps"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// As cotas são do titular, somando os projetos dele (spec §Cotas): com A, o
// Projeto padrão, cheio, o POST no projeto B do mesmo titular dá 409, e B
// continua vazio. O de canais (e-mail) prova também a pré-conferência: ela já
// conta A, e o e-mail de verificação nem sai.
func TestQuotaCountsSeeEveryProjectOfTheUser(t *testing.T) {
	comRegra := maps.Clone(alvoValido)
	comRegra["url"] = "https://example.com/z"
	comRegra["content_rule"] = map[string]any{"mode": "contains", "value": "ok"}

	casos := []struct {
		nome, recurso, tabela, codigo string
		corpo                         any
		// encher grava em A, por fora da RLS, o teto inteiro do recurso.
		encher string
		teto   int64
	}{
		{"alvos", "targets", "targets", "target_limit_reached", alvoValido, `
			INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
			                     expected_status, next_run_at)
			SELECT gen_random_uuid(), $1, $2, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
			  FROM generate_series(1, $3::int) i`, targets.MaxTargetsPerUser},
		{"heartbeats", "heartbeats", "heartbeats", "heartbeat_limit_reached", corpoDePeriodo, `
			INSERT INTO heartbeats (id, user_id, project_id, name, ping_token_hash, period_sec, grace_sec,
			                        enabled, created_at, deadline_at, description, ping_resumes)
			SELECT gen_random_uuid(), $1, $2, 'hb ' || i, sha256(convert_to(gen_random_uuid()::text, 'UTF8')),
			       3600, 300, true, now(), now() + interval '1 hour', '', false
			  FROM generate_series(1, $3::int) i`, heartbeat.MaxPerUser},
		{"canais", "channels", "notif_channels", "channel_limit_reached",
			map[string]string{"kind": "email", "email": "pessoa@exemplo.com"}, `
			INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, created_at)
			SELECT gen_random_uuid(), $1, $2, 'discord', '\x01', now(), now()
			  FROM generate_series(1, $3::int) i`, notify.MaxChannels},
		{"regras_por_host", "targets", "targets", "content_rule_host_limit_reached", comRegra, `
			INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
			                     expected_status, next_run_at, content_mode, content_value)
			SELECT gen_random_uuid(), $1, $2, 'regra ' || i, 'https://example.com/' || i, 'GET', 60, 10000, 200,
			       now(), 'contains', 'ok'
			  FROM generate_series(1, $3::int) i`, targets.DefaultContentRulesPerHost},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := newAPI(t)
			ctx := context.Background()
			admin := testdb.Admin(t)
			tok := tokenDe(t, a, a.userID)
			projetoA := testdb.ProjetoPadrao(t, a.userID)
			projetoB := testdb.NovoProjeto(t, a.userID, "B")
			if _, err := admin.Exec(ctx, c.encher, a.userID, projetoA, c.teto); err != nil {
				t.Fatalf("encher A: %v", err)
			}

			caminho := "/api/v1/projects/" + projetoB.String() + "/" + c.recurso
			resp := chamar(t, a, http.MethodPost, caminho, tok, c.corpo)
			defer resp.Body.Close()
			if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusConflict || e.Error.Code != c.codigo {
				t.Errorf("POST %s com A no teto deu %d/%s, queria 409/%s", caminho, resp.StatusCode, e.Error.Code, c.codigo)
			}
			if n := contarNoProjetoComoAdmin(t, c.tabela, projetoB); n != 0 {
				t.Errorf("a recusa deixou %d linha(s) de %s em B", n, c.tabela)
			}
			a.email.mu.Lock()
			enviados := len(a.email.links)
			a.email.mu.Unlock()
			if enviados != 0 {
				t.Errorf("com A no teto de canais, %d e-mail(s) de verificação saíram; a pré-conferência "+
					"tem de contar os projetos do titular", enviados)
			}
		})
	}
}

// contarNoProjetoComoAdmin conta, por fora da RLS, as linhas da tabela no projeto.
func contarNoProjetoComoAdmin(t *testing.T, tabela string, projeto uuid.UUID) int64 {
	t.Helper()
	var n int64
	// tabela é sempre um literal do próprio teste.
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM `+tabela+` WHERE project_id = $1`, projeto).Scan(&n); err != nil {
		t.Fatalf("contar %s no projeto %s: %v", tabela, projeto, err)
	}
	return n
}
