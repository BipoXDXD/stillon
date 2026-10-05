package config

import (
	"log/slog"
	"strings"
	"testing"
)

// Toda chave obrigatória deve ser recusada no boot, sem devolver uma
// configuração parcial nem repetir no erro o material fornecido.
func TestLoadRecusaChavesAusentesOuBase64Invalido(t *testing.T) {
	for _, nome := range []string{"MASTER_KEY", "JWT_SECRET", "IP_HASH_KEY"} {
		for _, caso := range []struct {
			nome, valor, motivo string
		}{
			{"ausente", "", "variável obrigatória ausente"},
			{"só espaços", " \t ", "variável obrigatória ausente"},
			{"base64 inválido", "material-invalido@", "não é base64 válido"},
		} {
			t.Run(nome+"/"+caso.nome, func(t *testing.T) {
				env := validEnv()
				env[nome] = caso.valor
				cfg, err := Load(getenvFrom(env))
				if err == nil {
					t.Fatal("a chave inválida foi aceita")
				}
				if cfg != nil {
					t.Fatal("a recusa devolveu uma configuração parcial")
				}
				if !strings.Contains(err.Error(), nome) || !strings.Contains(err.Error(), caso.motivo) {
					t.Fatalf("erro = %v, queria identificar %s e %q", err, nome, caso.motivo)
				}
				if caso.nome == "base64 inválido" && strings.Contains(err.Error(), caso.valor) {
					t.Fatal("o erro repetiu o material da chave")
				}
			})
		}
	}
}

func TestLoadRecusaURLPublicaAusente(t *testing.T) {
	for _, valor := range []string{"", " \t "} {
		t.Run(valor, func(t *testing.T) {
			env := validEnv()
			env["PUBLIC_BASE_URL"] = valor
			cfg, err := Load(getenvFrom(env))
			if err == nil || !strings.Contains(err.Error(), "variável obrigatória ausente: PUBLIC_BASE_URL") {
				t.Fatalf("URL pública ausente: erro = %v, queria recusa da variável obrigatória", err)
			}
			if cfg != nil {
				t.Fatal("a recusa da URL pública devolveu uma configuração parcial")
			}
		})
	}
}

// Vírgulas sobrando não devem apagar proxies legítimos nem criar entradas
// confiáveis adicionais; uma lista só de separadores permanece vazia.
func TestLoadIgnoraSeparadoresVaziosNosProxies(t *testing.T) {
	for _, caso := range []struct {
		valor string
		quer  []string
	}{
		{" , 10.0.0.0/8, , 2001:db8::/32, ", []string{"10.0.0.0/8", "2001:db8::/32"}},
		{" , , \t, ", nil},
	} {
		t.Run(caso.valor, func(t *testing.T) {
			env := validEnv()
			env["TRUSTED_PROXIES"] = caso.valor
			cfg, err := Load(getenvFrom(env))
			if err != nil {
				t.Fatalf("separadores vazios recusados: %v", err)
			}
			if len(cfg.TrustedProxies) != len(caso.quer) {
				t.Fatalf("proxies = %v, queria %v", cfg.TrustedProxies, caso.quer)
			}
			for i, quer := range caso.quer {
				if cfg.TrustedProxies[i].String() != quer {
					t.Errorf("proxy %d = %s, queria %s", i, cfg.TrustedProxies[i], quer)
				}
			}
		})
	}
}

func TestLoadNivelDeLogConfigurado(t *testing.T) {
	for _, caso := range []struct {
		valor string
		quer  slog.Level
	}{
		{" DEBUG ", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"WaRn", slog.LevelWarn},
		{" ERROR ", slog.LevelError},
	} {
		t.Run(caso.valor, func(t *testing.T) {
			env := validEnv()
			env["LOG_LEVEL"] = caso.valor
			cfg, err := Load(getenvFrom(env))
			if err != nil {
				t.Fatalf("nível de log conhecido recusado: %v", err)
			}
			if cfg.LogLevel != caso.quer {
				t.Errorf("LogLevel = %v, queria %v", cfg.LogLevel, caso.quer)
			}
		})
	}
	for _, valor := range []string{"trace", "42"} {
		t.Run(valor, func(t *testing.T) {
			env := validEnv()
			env["LOG_LEVEL"] = valor
			cfg, err := Load(getenvFrom(env))
			if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL inválido") {
				t.Fatalf("nível desconhecido aceito: erro = %v", err)
			}
			if cfg != nil {
				t.Fatal("o nível desconhecido devolveu uma configuração parcial")
			}
		})
	}
}

// hostLocal também precisa falhar fechado se receber uma URL malformada,
// mesmo que o texto contenha localhost ou um endereço de loopback.
func TestHostLocalRecusaURLMalformada(t *testing.T) {
	for _, valor := range []string{"http://[::1", "http://localhost:abc", "http://localhost%zz"} {
		t.Run(valor, func(t *testing.T) {
			if hostLocal(valor) {
				t.Fatal("URL malformada foi considerada uma origem local")
			}
		})
	}
}

// Uma constante de demo malformada é defeito no código e deve interromper
// a inicialização; aceitar a falha silenciosamente retiraria sua validação.
func TestDemoAntigaInterrompeComBase64Invalido(t *testing.T) {
	defer func() {
		falha := recover()
		mensagem, ok := falha.(string)
		if !ok || !strings.Contains(mensagem, "chave de demo antiga com base64 inválido") {
			t.Fatalf("panic = %v, queria a recusa da constante inválida", falha)
		}
	}()
	demoAntiga("material-invalido@")
}
