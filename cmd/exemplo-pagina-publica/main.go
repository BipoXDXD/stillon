// Command exemplo-pagina-publica grava a status page pública com dados de
// exemplo, para as capturas do README (web/e2e/capturas.spec.ts). Usa o
// template de verdade, sem banco e sem servidor:
//
//	go run ./cmd/exemplo-pagina-publica <arquivo de destino>
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// nonceDoExemplo: a página sai de um arquivo, sem CSP; o nonce só precisa
// ter o formato do de verdade.
const nonceDoExemplo = "AAAAAAAAAAAAAAAAAAAAAA=="

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "exemplo-pagina-publica:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("uso: exemplo-pagina-publica <arquivo de destino>")
	}
	var buf bytes.Buffer
	if err := httpapi.RenderizarPaginaPublica(&buf, exemploDaPaginaPublica(), nonceDoExemplo); err != nil {
		return fmt.Errorf("renderizar o exemplo: %w", err)
	}
	// #nosec G703 -- ferramenta de desenvolvimento: o destino é o argumento de quem a roda, na própria máquina.
	if err := os.WriteFile(args[0], buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("gravar o exemplo: %w", err)
	}
	return nil
}

// exemploDaPaginaPublica: dois serviços no ar (um com um dia instável), um
// lento hoje, um fora do ar, um novo com só 10 dias de dados, e três
// incidentes resolvidos.
func exemploDaPaginaPublica() statuspage.View {
	agora := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	// ruins: o uptime dos dias com problema. Os dias 69, 78 e 87 (24/09, 03/10
	// e 12/10) são os dos incidentes abaixo, na medida da duração.
	servicos := []struct {
		nome     string
		estado   statuspage.State
		latencia int32
		ruins    map[int]float64
	}{
		{"API de pagamentos", statuspage.StateUp, 142, map[int]float64{41: 0.995, 69: 0.9375}},
		{"Site institucional", statuspage.StateUp, 318, nil},
		{"Painel do cliente", statuspage.StateDegraded, 2380, map[int]float64{78: 0.9549, 88: 0.996, 89: 0.993}},
		{"Banco de relatórios", statuspage.StateDown, 88, map[int]float64{87: 0.9722, 89: 0.61}},
	}
	v := statuspage.View{Title: "Status da Loja Exemplo", GeneratedAt: agora}
	for _, s := range servicos {
		lat := s.latencia
		it := statuspage.PublicItem{Name: s.nome, State: s.estado, LatencyMS: &lat}
		for d := range it.Days {
			it.Days[d] = statuspage.Day{Date: agora.AddDate(0, 0, d-statuspage.Days+1), Uptime: ptr(1)}
			if u, ruim := s.ruins[d]; ruim {
				it.Days[d].Uptime = ptr(u)
			}
		}
		it.Uptime90 = mediaDosDias(it.Days)
		v.Items = append(v.Items, it)
	}
	novo := statuspage.PublicItem{Name: "Webhook de pedidos", State: statuspage.StateUp}
	for d := range novo.Days {
		novo.Days[d].Date = agora.AddDate(0, 0, d-statuspage.Days+1)
		if d >= statuspage.Days-10 {
			novo.Days[d].Uptime = ptr(1)
		}
	}
	novo.Uptime90 = mediaDosDias(novo.Days)
	v.Items = append(v.Items, novo)
	for i, nome := range []string{"Banco de relatórios", "Painel do cliente", "API de pagamentos"} {
		inicio := agora.Add(-time.Duration(i*9+2) * 24 * time.Hour)
		v.Incidents = append(v.Incidents, statuspage.PublicIncident{
			Name: nome, StartedAt: inicio, ResolvedAt: inicio.Add(time.Duration(40+i*25) * time.Minute),
		})
	}
	return v
}

// mediaDosDias: o agregado da barra, para o percentual do serviço bater com
// os dias que ela mostra. Dia sem check não conta.
func mediaDosDias(dias [statuspage.Days]statuspage.Day) *float64 {
	var soma, n float64
	for _, d := range dias {
		if d.Uptime != nil {
			soma += *d.Uptime
			n++
		}
	}
	return ptr(soma / n)
}

func ptr(v float64) *float64 { return &v }
