package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/httpapi"
)

const rotaDeExportacao = "/api/v1/me/export"

// Pelo Routes de verdade: o anexo com o nome do dia, sem cache, o corpo é JSON
// inteiro e a exportação fica na auditoria.
func TestExportarPelaAPIAnexoSemCache(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodGet, rotaDeExportacao, token, nil)
	defer resp.Body.Close()
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, queria 200: %s", resp.StatusCode, corpo)
	}
	for nome, esperado := range map[string]string{
		"Content-Type":        "application/json",
		"Content-Disposition": `attachment; filename="stillon-export-` + a.clk.Now().UTC().Format("2006-01-02") + `.json"`,
		"Cache-Control":       "no-store",
	} {
		if got := resp.Header.Get(nome); got != esperado {
			t.Errorf("%s = %q, queria %q", nome, got, esperado)
		}
	}
	if !json.Valid(corpo) {
		t.Fatalf("corpo não é JSON válido: %s", corpo)
	}
	var topo struct {
		Account struct {
			ID uuid.UUID `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(corpo, &topo); err != nil || topo.Account.ID != a.userID {
		t.Errorf("account.id = %s (%v), queria a conta do token %s", topo.Account.ID, err, a.userID)
	}
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'account.exported'`, a.userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("auditoria account.exported = %d, queria 1", n)
	}
}

// Balde próprio por usuário, no banco: rajada de 2, e a 3ª na janela é 429. O
// balde é da conta: outro usuário, do mesmo IP, continua exportando.
func TestExportarTemLimitePorUsuario(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	for i := range 2 {
		status, corpo := lerCru(t, a, http.MethodGet, rotaDeExportacao, token, nil)
		if status != http.StatusOK {
			t.Fatalf("exportação %d da rajada: %d %s", i+1, status, corpo)
		}
	}
	status, corpo := lerCru(t, a, http.MethodGet, rotaDeExportacao, token, nil)
	exigirErro(t, "3ª exportação na janela", respostaCrua{status: status, corpo: corpo},
		http.StatusTooManyRequests, apierr.CodeRateLimited)

	outro := outroUsuario(t, a)
	status, corpo = lerCru(t, a, http.MethodGet, rotaDeExportacao, tokenDe(t, a, outro), nil)
	if status != http.StatusOK {
		t.Errorf("exportação de outro usuário: %d %s, queria 200", status, corpo)
	}
}

// exportadorQueCaiNoMeio escreve o começo do JSON e falha.
type exportadorQueCaiNoMeio struct{}

func (exportadorQueCaiNoMeio) Escrever(_ context.Context, _ uuid.UUID, w io.Writer) error {
	if _, err := io.WriteString(w, `{"format":"stillon-export","account":`); err != nil {
		return err
	}
	return errors.New("banco caiu no meio")
}

// A falha depois do primeiro byte derruba a conexão, pela cadeia inteira
// (Recover incluído): o cliente vê o download quebrar, e não um arquivo
// truncado com status 200 nem um envelope de erro colado no fim. Com poucos
// bytes ainda no buffer do servidor, a queda chega antes do status (o Do
// falha); com mais, no meio do corpo (o ReadAll falha). As duas servem.
func TestExportarFalhaNoMeioDerrubaODownload(t *testing.T) {
	a := newAPICom(t, nil, func(d *httpapi.Deps) { d.Exportacao = exportadorQueCaiNoMeio{} })
	req, err := http.NewRequest(http.MethodGet, a.srv.URL+rotaDeExportacao, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenDe(t, a, a.userID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // a conexão caiu antes do status
	}
	defer resp.Body.Close()
	corpo, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("o download terminou sem erro (status %d): %s", resp.StatusCode, corpo)
	}
}
