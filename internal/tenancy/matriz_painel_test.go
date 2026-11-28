package tenancy_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// nomesDoPainel liga cada permissão do domínio ao nome que o painel usa em
// web/src/lib/permissoes.json. Permissão nova no Go sem nome aqui faz o teste
// falhar: o painel precisa saber dela.
var nomesDoPainel = map[string]tenancy.Permission{
	"read":               tenancy.PermRead,
	"edit_monitors":      tenancy.PermEditMonitors,
	"manage_channels":    tenancy.PermManageChannels,
	"manage_status_page": tenancy.PermManageStatusPage,
	"invite_basic":       tenancy.PermInviteBasic,
	"manage_members":     tenancy.PermManageMembers,
	"manage_project":     tenancy.PermManageProject,
}

// TestPainelPermissionsMatchDomain: a matriz que o painel usa para esconder
// ações é a mesma de Role.Can. O painel não decide nada (o servidor confere),
// mas uma divergência mostraria botões que dão 403 ou esconderia o que o
// papel pode.
func TestPainelPermissionsMatchDomain(t *testing.T) {
	cru, err := os.ReadFile("../../web/src/lib/permissoes.json")
	if err != nil {
		t.Fatalf("ler permissoes.json: %v", err)
	}
	var matriz map[string][]string
	if err := json.Unmarshal(cru, &matriz); err != nil {
		t.Fatalf("permissoes.json inválido: %v", err)
	}

	papeis := []tenancy.Role{tenancy.RoleOwner, tenancy.RoleManager, tenancy.RoleMember, tenancy.RoleViewer}
	if len(matriz) != len(papeis) {
		t.Fatalf("permissoes.json tem %d papéis, o domínio tem %d", len(matriz), len(papeis))
	}
	for nome := range matriz {
		if _, err := tenancy.ParseRole(nome); err != nil {
			t.Errorf("papel %q do permissoes.json: %v", nome, err)
		}
	}

	for _, papel := range papeis {
		lista, ok := matriz[string(papel)]
		if !ok {
			t.Errorf("permissoes.json sem o papel %q", papel)
			continue
		}
		for _, nome := range lista {
			if _, conhecido := nomesDoPainel[nome]; !conhecido {
				t.Errorf("%s: permissão %q do permissoes.json não existe no domínio", papel, nome)
			}
		}
		for nome, p := range nomesDoPainel {
			if naLista, pode := slices.Contains(lista, nome), papel.Can(p); naLista != pode {
				t.Errorf("%s/%s: permissoes.json diz %v, Role.Can diz %v", papel, nome, naLista, pode)
			}
		}
	}

	nomeadas := make(map[tenancy.Permission]bool, len(nomesDoPainel))
	for _, p := range nomesDoPainel {
		nomeadas[p] = true
	}
	for p := 1; p <= 255; p++ {
		perm := tenancy.Permission(p)
		for _, papel := range papeis {
			if papel.Can(perm) && !nomeadas[perm] {
				t.Errorf("a permissão %d, de %s, não tem nome no painel", p, papel)
				break
			}
		}
	}
}
