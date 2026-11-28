package exportacao_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/exportacao"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// agora é o relógio do cenário: generated_at tem de sair dele, nunca do
// relógio da máquina.
var agora = time.Date(2026, 11, 28, 12, 34, 56, 789000000, time.UTC)

// Os segredos semeados. Nenhum deles pode aparecer nos bytes da exportação,
// nem cru nem na forma em que o banco o guarda (hash, ciphertext).
const (
	urlDoWebhook        = "https://discord.com/api/webhooks/123/segredo"
	valorDoHeader       = "Bearer segredo-xyz"
	tokenDePing         = "ping-token-segredo-abc"
	segredoTOTP         = "JBSWY3DPEHPK3PXP-segredo-totp"
	codigoDeRecuperacao = "rec-code-segredo-9f8e"
	hashDaSenha         = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNlZ3JlZG8$hashsegredodasenha"
	nomeAlheio          = "Projeto-Alheio"
)

func novaCaixa(t *testing.T) *crypto.Box {
	t.Helper()
	box, err := crypto.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func novoID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func novoUsuario(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	id := novoID(t)
	if err := db.New(pool).InsertUser(context.Background(), db.InsertUserParams{
		ID: id, Email: email, PasswordHash: hashDaSenha, CreatedAt: agora.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// cenario guarda o que os testes conferem e os segredos em todas as formas
// em que o banco os guarda.
type cenario struct {
	pool      *pgxpool.Pool
	box       *crypto.Box
	dona      uuid.UUID
	padrao    uuid.UUID // Projeto padrão da dona (titular)
	coOwner   uuid.UUID // owner, mas não titular
	editor    uuid.UUID // member: só em memberships
	alheio    uuid.UUID // sem vínculo nenhum
	proibidos map[string]string
}

func montar(t *testing.T) cenario {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	box := novaCaixa(t)
	admin := testdb.Admin(t)

	dona := novoUsuario(t, pool, "dona@exemplo.com")
	outro := novoUsuario(t, pool, "outro@exemplo.com")
	c := cenario{pool: pool, box: box, dona: dona, proibidos: map[string]string{}}

	// Conta: segredo TOTP cifrado, aceite dos termos, código de recuperação.
	totpEnc, err := box.Seal([]byte(segredoTOTP), crypto.AAD("users", "totp_secret_enc", dona))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE users SET totp_secret_enc = $2, totp_confirmed_at = $3,
		terms_version = '2026-11-28', terms_accepted_at = $3 WHERE id = $1`, dona, totpEnc, agora.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	hashRec := crypto.HashToken(codigoDeRecuperacao)
	if _, err := admin.Exec(ctx, `INSERT INTO totp_recovery_codes (id, user_id, code_hash, created_at)
		VALUES ($1, $2, $3, now())`, novoID(t), dona, hashRec); err != nil {
		t.Fatal(err)
	}

	// Sessão com ip_hash e hash de token.
	ipHash := crypto.HashIP(bytes.Repeat([]byte{9}, 32), "203.0.113.7")
	tokenHash := crypto.HashToken("token-de-sessao-segredo")
	testdb.FamiliaSintetica(t, dona, agora.Add(-time.Minute), agora.Add(time.Hour), tokenHash, ipHash)

	// Auditoria com ip_hash.
	entidade := "target"
	if err := db.New(pool).InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		ID: novoID(t), UserID: &dona, Action: "target.create", Entity: &entidade, EntityID: &dona,
		IpHash: ipHash, At: agora.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// Projetos.
	c.padrao = testdb.ProjetoPadrao(t, dona)
	c.coOwner = testdb.NovoProjeto(t, outro, "Projeto-CoOwner")
	testdb.Membro(t, c.coOwner, dona, "owner")
	c.editor = testdb.NovoProjeto(t, outro, "Projeto-Editor")
	testdb.Membro(t, c.editor, dona, "member")
	c.alheio = testdb.NovoProjeto(t, outro, nomeAlheio)

	// Recursos do Projeto padrão: alvo com header, heartbeat com token de
	// ping, canal Discord com a URL do webhook e canal de e-mail.
	alvo := novoID(t)
	headersEnc, err := targets.SealHeaders(box, alvo, map[string]string{"Authorization": valorDoHeader})
	if err != nil {
		t.Fatal(err)
	}
	pingHash := crypto.HashToken(tokenDePing)
	discord := novoID(t)
	discordEnc, err := box.Seal([]byte(urlDoWebhook), crypto.AAD("notif_channels", "config_enc", discord))
	if err != nil {
		t.Fatal(err)
	}
	email := novoID(t)
	emailEnc, err := box.Seal([]byte("avisos@exemplo.com"), crypto.AAD("notif_channels", "config_enc", email))
	if err != nil {
		t.Fatal(err)
	}
	verificado := agora.Add(-time.Hour)
	err = storage.InProjectTx(ctx, pool, dona, c.padrao, func(q *db.Queries, _ tenancy.ProjectScope) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dona, Name: "API", URL: "https://api.exemplo.com/health", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, HeadersEnc: headersEnc, Enabled: true,
			NextRunAt: agora, CreatedAt: agora.Add(-time.Hour), ProjectID: c.padrao,
		}); err != nil {
			return err
		}
		periodo := int32(300)
		if err := q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: novoID(t), UserID: dona, Name: "Backup", PingTokenHash: pingHash, PeriodSec: &periodo,
			GraceSec: 60, Enabled: true, CreatedAt: agora.Add(-time.Hour), DeadlineAt: agora.Add(time.Hour),
			ProjectID: c.padrao,
		}); err != nil {
			return err
		}
		if err := q.InsertChannel(ctx, db.InsertChannelParams{
			ID: discord, UserID: dona, Kind: "discord", ConfigEnc: discordEnc, VerifiedAt: &verificado,
			CreatedAt: agora.Add(-time.Hour), ProjectID: c.padrao,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: email, UserID: dona, Kind: "email", ConfigEnc: emailEnc, VerifiedAt: &verificado,
			CreatedAt: agora.Add(-time.Hour), ProjectID: c.padrao,
		})
	})
	if err != nil {
		t.Fatalf("semear o Projeto padrão: %v", err)
	}

	for nome, b := range map[string][]byte{
		"segredo TOTP cifrado": totpEnc, "hash do código de recuperação": hashRec, "ip_hash": ipHash,
		"hash do token de sessão": tokenHash, "hash do token de ping": pingHash,
		"URL do webhook cifrada": discordEnc, "headers cifrados": headersEnc,
	} {
		c.proibidos[nome+" (hex)"] = hex.EncodeToString(b)
		c.proibidos[nome+" (base64)"] = base64.StdEncoding.EncodeToString(b)
	}
	for nome, s := range map[string]string{
		"URL do webhook": urlDoWebhook, "token do webhook": "segredo", "valor do header": valorDoHeader,
		"token de ping": tokenDePing, "segredo TOTP": segredoTOTP, "código de recuperação": codigoDeRecuperacao,
		"hash da senha": hashDaSenha, "projeto alheio": nomeAlheio,
	} {
		c.proibidos[nome] = s
	}
	return c
}

func exportar(t *testing.T, c cenario) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := exportacao.Novo(c.pool, c.box, clock.NewFake(agora)).Escrever(context.Background(), c.dona, &buf); err != nil {
		t.Fatalf("exportar: %v", err)
	}
	return buf.Bytes()
}

func decodificar(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var topo map[string]json.RawMessage
	if err := json.Unmarshal(b, &topo); err != nil {
		t.Fatalf("a exportação não é um JSON válido: %v\n%s", err, b)
	}
	return topo
}

func TestExportacaoTemFormatoVersionado(t *testing.T) {
	topo := decodificar(t, exportar(t, montar(t)))
	var cab struct {
		Format      string    `json:"format"`
		Version     *int      `json:"version"`
		GeneratedAt time.Time `json:"generated_at"`
	}
	b, _ := json.Marshal(topo)
	if err := json.Unmarshal(b, &cab); err != nil {
		t.Fatal(err)
	}
	if cab.Format != "stillon-export" {
		t.Errorf("format = %q, esperado stillon-export", cab.Format)
	}
	if cab.Version == nil || *cab.Version != 1 {
		t.Errorf("version = %v, esperado 1", cab.Version)
	}
	if !cab.GeneratedAt.Equal(agora) {
		t.Errorf("generated_at = %v, esperado o relógio do cenário %v", cab.GeneratedAt, agora)
	}
}

func chaves(m map[string]json.RawMessage) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestExportacaoChavesDaVersao1(t *testing.T) {
	topo := decodificar(t, exportar(t, montar(t)))
	esperadas := []string{"account", "audit", "format", "generated_at", "memberships", "projects", "sessions", "version"}
	if got := chaves(topo); !slices.Equal(got, esperadas) {
		t.Errorf("chaves de topo = %v, esperado %v", got, esperadas)
	}
	var projetos []map[string]json.RawMessage
	if err := json.Unmarshal(topo["projects"], &projetos); err != nil || len(projetos) == 0 {
		t.Fatalf("projects = %s (%v): esperado ao menos um projeto", topo["projects"], err)
	}
	esperadasProj := []string{"channels", "created_at", "heartbeats", "id", "incidents", "invitations",
		"is_default", "members", "name", "rollups", "status_pages", "targets"}
	if got := chaves(projetos[0]); !slices.Equal(got, esperadasProj) {
		t.Errorf("chaves de projects[0] = %v, esperado %v", got, esperadasProj)
	}
}

func TestExportacaoIncluiProjetosOndeEOwner(t *testing.T) {
	c := montar(t)
	topo := decodificar(t, exportar(t, c))
	var projetos []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(topo["projects"], &projetos); err != nil {
		t.Fatal(err)
	}
	var pertencas []struct {
		ProjectID uuid.UUID `json:"project_id"`
		Role      string    `json:"role"`
		IsTitular bool      `json:"is_titular"`
	}
	if err := json.Unmarshal(topo["memberships"], &pertencas); err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, len(projetos))
	for _, p := range projetos {
		ids = append(ids, p.ID)
	}
	if !slices.Contains(ids, c.padrao) || !slices.Contains(ids, c.coOwner) {
		t.Errorf("projects = %v: esperado o Projeto padrão %s e o de owner não titular %s", ids, c.padrao, c.coOwner)
	}
	if slices.Contains(ids, c.editor) {
		t.Errorf("projects traz o projeto %s em que ela é member: só owner entra em projects", c.editor)
	}
	papeis := map[uuid.UUID]string{}
	for _, m := range pertencas {
		papeis[m.ProjectID] = m.Role
	}
	if papeis[c.editor] != "member" || papeis[c.coOwner] != "owner" || papeis[c.padrao] != "owner" {
		t.Errorf("memberships = %+v: esperado o padrão e o co-owner como owner e o editor como member", pertencas)
	}
	if _, ok := papeis[c.alheio]; ok {
		t.Errorf("memberships traz o projeto alheio %s", c.alheio)
	}
}

func TestExportacaoNaoVazaSegredoNemProjetoAlheio(t *testing.T) {
	c := montar(t)
	saida := exportar(t, c)
	decodificar(t, saida) // JSON inteiro e válido
	for nome, s := range c.proibidos {
		if strings.Contains(string(saida), s) {
			t.Errorf("a exportação contém %s (%q)", nome, s)
		}
	}
	// Prova de que a exportação leu os recursos semeados: sem o hint do
	// webhook e o nome do header, a ausência dos segredos não provaria nada.
	for _, esperado := range []string{"webhook …123", "Authorization", "[omitido]", "avisos@exemplo.com", "Backup"} {
		if !strings.Contains(string(saida), esperado) {
			t.Errorf("a exportação não contém %q: o recurso semeado não foi lido", esperado)
		}
	}
}

// Um alvo cujos headers não decifram (chave trocada, linha adulterada) sai com
// headers vazios, como os canais, em vez de derrubar a exportação inteira.
func TestExportacaoDegradaHeaderQueNaoDecifra(t *testing.T) {
	c := montar(t)
	ctx := context.Background()
	quebrado := novoID(t)
	// Selado para outro alvo: a AAD não bate e o Open recusa.
	alheio, err := targets.SealHeaders(c.box, novoID(t), map[string]string{"X-Token": "segredo-do-outro"})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InProjectTx(ctx, c.pool, c.dona, c.padrao, func(q *db.Queries, _ tenancy.ProjectScope) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: quebrado, UserID: c.dona, Name: "Quebrado", URL: "https://quebrado.exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, HeadersEnc: alheio, Enabled: true,
			NextRunAt: agora, CreatedAt: agora.Add(-time.Hour), ProjectID: c.padrao,
		})
	})
	if err != nil {
		t.Fatalf("semear o alvo quebrado: %v", err)
	}

	topo := decodificar(t, exportar(t, c))
	var projetos []struct {
		ID      uuid.UUID `json:"id"`
		Targets []struct {
			ID      uuid.UUID          `json:"id"`
			Headers *map[string]string `json:"headers"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(topo["projects"], &projetos); err != nil {
		t.Fatal(err)
	}
	achou := false
	for _, p := range projetos {
		for _, a := range p.Targets {
			if a.ID != quebrado {
				continue
			}
			achou = true
			if a.Headers == nil || len(*a.Headers) != 0 {
				t.Errorf("headers do alvo quebrado = %v, esperado {}", a.Headers)
			}
		}
	}
	if !achou {
		t.Errorf("o alvo quebrado %s não saiu na exportação", quebrado)
	}
}
