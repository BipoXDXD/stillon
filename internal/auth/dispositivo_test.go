package auth_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
)

var chaveMestra = []byte("chave-mestra-de-teste-32-bytes!!")

// hashDoDono é um hash de senha qualquer: o carimbo só o compara.
const hashDoDono = "$argon2id$hash-do-dono"

// A3 (Plano 10): o cookie de dispositivo vale para a conta que o recebeu, até
// o prazo, e só com a chave que o assinou. Qualquer outra coisa é "sem
// cookie", e o login segue pelo bloqueio da conta.
func TestDeviceCookieChecks(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	d := auth.NewDevices(chaveMestra, clk)
	dono, outro := uuid.New(), uuid.New()
	carimbo := auth.NewDeviceStamp(hashDoDono, nil)

	valor, prazo, err := d.Issue(dono, carimbo)
	if err != nil {
		t.Fatal(err)
	}
	if quer := clk.Now().Add(auth.DeviceTTL); !prazo.Equal(quer) {
		t.Errorf("prazo = %v, queria %v (180 dias)", prazo, quer)
	}
	id, ok := d.Check(valor, dono, carimbo)
	if !ok {
		t.Fatal("o cookie recém-emitido não vale para a conta dele")
	}
	outroValor, _, err := d.Issue(dono, carimbo)
	if err != nil {
		t.Fatal(err)
	}
	if outroID, _ := d.Check(outroValor, dono, carimbo); outroID == id {
		t.Error("dois cookies emitidos com o mesmo identificador")
	}

	bruto, err := base64.RawURLEncoding.DecodeString(valor)
	if err != nil {
		t.Fatal(err)
	}
	adulterado := append([]byte(nil), bruto...)
	adulterado[0] ^= 1
	ativado := clk.Now().Add(-time.Hour)
	reativado := ativado.Add(time.Microsecond)
	casos := map[string]struct {
		valor   string
		conta   uuid.UUID
		devs    *auth.Devices
		carimbo auth.DeviceStamp
	}{
		"de outra conta":        {valor, outro, d, carimbo},
		"adulterado":            {base64.RawURLEncoding.EncodeToString(adulterado), dono, d, carimbo},
		"cortado":               {valor[:len(valor)-2], dono, d, carimbo},
		"lixo":                  {"nao-e-um-cookie", dono, d, carimbo},
		"vazio":                 {"", dono, d, carimbo},
		"de outra chave mestra": {valor, dono, auth.NewDevices([]byte("outra-chave-mestra-com-32-bytes!"), clk), carimbo},
		// I1 da revisão do Plano 10: a senha ou o 2FA mudaram depois do
		// cookie.
		"de antes da troca de senha": {valor, dono, d, auth.NewDeviceStamp("$argon2id$hash-novo", nil)},
		"de antes de ligar o 2FA":    {valor, dono, d, auth.NewDeviceStamp(hashDoDono, &ativado)},
	}
	for nome, c := range casos {
		if _, ok := c.devs.Check(c.valor, c.conta, c.carimbo); ok {
			t.Errorf("cookie %s foi aceito", nome)
		}
	}
	// Com o 2FA ligado, o reset-2fa e uma nova ativação mudam a data: o
	// cookie da ativação anterior não vale na nova.
	comFator, _, err := d.Issue(dono, auth.NewDeviceStamp(hashDoDono, &ativado))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Check(comFator, dono, auth.NewDeviceStamp(hashDoDono, &ativado)); !ok {
		t.Error("o cookie cunhado com o 2FA ligado não vale com o mesmo carimbo")
	}
	for nome, depois := range map[string]auth.DeviceStamp{
		"depois do reset-2fa":              auth.NewDeviceStamp(hashDoDono, nil),
		"depois de reativar (1 µs depois)": auth.NewDeviceStamp(hashDoDono, &reativado),
	} {
		if _, ok := d.Check(comFator, dono, depois); ok {
			t.Errorf("o cookie com o 2FA ligado valeu %s", nome)
		}
	}

	clk.Advance(auth.DeviceTTL - time.Second)
	if _, ok := d.Check(valor, dono, carimbo); !ok {
		t.Error("o cookie deixou de valer antes do prazo")
	}
	clk.Advance(time.Second)
	if _, ok := d.Check(valor, dono, carimbo); ok {
		t.Error("o cookie vencido continuou valendo")
	}
}

// As falhas do dispositivo contam só nele: a quinta reserva ainda é
// concedida (é verificada) e faz o cookie ser ignorado por LockDuration; o
// Refund da senha certa com 2FA pela frente devolve a reserva; o Reset do
// login completo zera; e cada dispositivo tem a sua conta.
func TestDeviceAttempts(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	d := auth.NewDevices(chaveMestra, clk)
	dono := uuid.New()
	carimbo := auth.NewDeviceStamp(hashDoDono, nil)
	emitir := func() auth.DeviceID {
		t.Helper()
		v, _, err := d.Issue(dono, carimbo)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := d.Check(v, dono, carimbo)
		return id
	}
	reservar := func(a auth.Attempts) bool {
		t.Helper()
		ok, err := a.Reserve(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	a, b := emitir(), emitir()

	for i := 1; i <= auth.LockThreshold; i++ {
		if !reservar(d.Attempts(a)) {
			t.Fatalf("reserva %d recusada", i)
		}
		if ignorado := d.Ignored(a); ignorado != (i == auth.LockThreshold) {
			t.Fatalf("depois de %d reservas, Ignored = %v", i, ignorado)
		}
	}
	if reservar(d.Attempts(a)) {
		t.Error("o dispositivo ignorado concedeu reserva")
	}
	if d.Ignored(b) || !reservar(d.Attempts(b)) {
		t.Error("as falhas de um dispositivo alcançaram outro")
	}

	if err := d.Attempts(a).Refund(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Ignored(a) {
		t.Error("devolver a quinta reserva não tirou o dispositivo do castigo")
	}
	if err := d.Attempts(a).Reset(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < auth.LockThreshold; i++ {
		reservar(d.Attempts(a))
	}
	if d.Ignored(a) {
		t.Error("Reset não zerou: quatro reservas depois dele já ignoram o dispositivo")
	}

	reservar(d.Attempts(a))
	if !d.Ignored(a) {
		t.Fatal("a quinta reserva depois do Reset não ignorou o dispositivo")
	}
	clk.Advance(auth.LockDuration)
	if d.Ignored(a) {
		t.Error("o dispositivo continuou ignorado depois de LockDuration")
	}
	for i := 1; i < auth.LockThreshold; i++ {
		if !reservar(d.Attempts(a)) || d.Ignored(a) {
			t.Fatalf("depois do castigo, a reserva %d já ignorou o dispositivo: a contagem não recomeçou", i)
		}
	}
}
