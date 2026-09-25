package auth

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// O cookie de dispositivo (A3 do Plano 10; OWASP, "Slow down online guessing
// attacks with device cookies"). Antes, qualquer anônimo que soubesse o e-mail
// do dono o trancava fora com cinco senhas erradas a cada 15 min: o bloqueio
// era da conta, e valia para todo mundo.
//
// Depois de um login COMPLETO (senha e, se ligado, 2FA), o navegador recebe um
// identificador aleatório com prazo, assinado por um MAC do par (usuário,
// identificador). Um login que o apresenta, válido para aquela conta, não é
// barrado pelo bloqueio da conta: as falhas dele contam num contador só dele,
// em memória, e cinco falhas fazem o cookie ser ignorado por 15 min (o login
// volta ao bloqueio da conta). Nada novo no banco: o MAC é a prova, e a chave
// é derivada da MASTER_KEY com um rótulo próprio.
//
// O MAC cobre também um carimbo das credenciais da conta (DeviceStamp; I1 da
// revisão do Plano 10): o hash da senha e a data de ativação do 2FA. Sem ele,
// os cookies de quem já teve acesso completo sobreviviam à remediação do dono
// (troca de senha, set-password, reset-2fa, que encerram as sessões), cada um
// com o seu contador fora do bloqueio da conta. Com ele, esses eventos
// invalidam todos os cookies da conta, os do dono inclusive: ele volta ao
// bloqueio da conta até o próximo login completo.

// DeviceTTL é a validade do cookie de dispositivo. O prazo vai dentro do
// cookie, sob o MAC: o servidor o confere, e não só o navegador.
const DeviceTTL = 180 * 24 * time.Hour

// rotuloDispositivo separa a chave do MAC de todo outro uso da MASTER_KEY.
const rotuloDispositivo = "vigilume.dispositivo.v1"

const (
	tamIDDispositivo  = 16
	tamPrazo          = 8
	tamMAC            = sha256.Size
	tamValorDecodific = tamIDDispositivo + tamPrazo + tamMAC
)

// DeviceID identifica um dispositivo que completou um login.
type DeviceID [tamIDDispositivo]byte

// Devices emite e confere cookies de dispositivo, e conta as falhas de cada um.
type Devices struct {
	chave  []byte
	clk    clock.Clock
	mu     sync.Mutex
	contas map[DeviceID]*contaDeDispositivo
}

type contaDeDispositivo struct {
	reservadas int
	ignorarAte time.Time
	visto      time.Time
}

// NewDevices deriva a chave do MAC da chave mestra por HKDF-SHA256, com um
// rótulo próprio: a chave do MAC não é a MASTER_KEY nem serve para outra
// coisa. O hkdf.Key só falha com uma saída maior que 255 blocos, o que 32
// bytes nunca são; o panic aqui é erro de programação.
func NewDevices(masterKey []byte, clk clock.Clock) *Devices {
	chave, err := hkdf.Key(sha256.New, masterKey, nil, rotuloDispositivo, sha256.Size)
	if err != nil {
		panic("auth: derivar a chave do cookie de dispositivo: " + err.Error())
	}
	return &Devices{chave: chave, clk: clk, contas: map[DeviceID]*contaDeDispositivo{}}
}

// DeviceStamp é o carimbo das credenciais que o cookie de dispositivo leva
// sob o MAC: o SHA-256 do hash da senha e da data de ativação do 2FA (nil,
// desligado). Muda com a troca de senha e o set-password (o hash) e com o
// reset-2fa, a ativação e o disable (a data). Não muda com a troca de
// autenticador, que mantém a data de ativação.
//
// Um cookie cunhado com o 2FA desligado volta a valer se o 2FA for ligado e
// depois resetado com a mesma senha: quem o tem entrou com essa senha, que
// continua valendo e, com o 2FA desligado, já abre a conta sozinha.
type DeviceStamp [sha256.Size]byte

// NewDeviceStamp calcula o carimbo de um hash de senha e da data de ativação
// do 2FA.
func NewDeviceStamp(passwordHash string, totpConfirmedAt *time.Time) DeviceStamp {
	h := sha256.New()
	h.Write([]byte(passwordHash))
	h.Write([]byte{0})
	if totpConfirmedAt != nil {
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(totpConfirmedAt.UnixMicro()))) // #nosec G115 -- só identidade: o sinal não importa
	}
	var c DeviceStamp
	h.Sum(c[:0])
	return c
}

// Issue emite o valor de um cookie novo para o usuário, com o carimbo das
// credenciais atuais dele, e o prazo do cookie.
func (d *Devices) Issue(userID uuid.UUID, carimbo DeviceStamp) (value string, expires time.Time, err error) {
	var id DeviceID
	if _, err := rand.Read(id[:]); err != nil {
		return "", time.Time{}, fmt.Errorf("sortear o dispositivo: %w", err)
	}
	expires = d.clk.Now().Add(DeviceTTL).Truncate(time.Second)
	bruto := make([]byte, 0, tamValorDecodific)
	bruto = append(bruto, id[:]...)
	bruto = binary.BigEndian.AppendUint64(bruto, uint64(expires.Unix())) // #nosec G115 -- um instante de 2026 em diante é positivo
	bruto = append(bruto, d.mac(userID, carimbo, bruto)...)
	return base64.RawURLEncoding.EncodeToString(bruto), expires, nil
}

// Check confere o cookie para a conta userID com as credenciais atuais dela
// (carimbo): formato, prazo e MAC. Um cookie de outra conta, vencido,
// adulterado, assinado com outra chave mestra ou cunhado antes de a senha ou o
// 2FA mudarem não vale, e quem chama segue pelo bloqueio da conta.
func (d *Devices) Check(value string, userID uuid.UUID, carimbo DeviceStamp) (DeviceID, bool) {
	bruto, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(bruto) != tamValorDecodific {
		return DeviceID{}, false
	}
	corpo, mac := bruto[:tamIDDispositivo+tamPrazo], bruto[tamIDDispositivo+tamPrazo:]
	if !hmac.Equal(mac, d.mac(userID, carimbo, corpo)) {
		return DeviceID{}, false
	}
	prazo := binary.BigEndian.Uint64(corpo[tamIDDispositivo:])
	if !d.clk.Now().Before(time.Unix(int64(prazo), 0)) { // #nosec G115 -- o prazo passou pelo MAC: foi o servidor que o escreveu
		return DeviceID{}, false
	}
	var id DeviceID
	copy(id[:], corpo[:tamIDDispositivo])
	return id, true
}

// mac amarra o corpo (identificador e prazo) à conta e às credenciais dela: o
// cookie de um usuário não vale no login de outro, nem depois de a senha ou o
// 2FA mudarem.
func (d *Devices) mac(userID uuid.UUID, carimbo DeviceStamp, corpo []byte) []byte {
	h := hmac.New(sha256.New, d.chave)
	h.Write(userID[:])
	h.Write(carimbo[:])
	h.Write(corpo)
	return h.Sum(nil)
}

// Ignored diz se o dispositivo errou o limite e está sendo ignorado: o login
// que o apresenta volta ao bloqueio da conta até o prazo passar.
func (d *Devices) Ignored(id DeviceID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.contas[id]
	return ok && d.clk.Now().Before(c.ignorarAte)
}

// Attempts é o contador do dispositivo como Attempts. Reserve devolve false
// se o dispositivo passou a ser ignorado entre o Ignored e a reserva.
func (d *Devices) Attempts(id DeviceID) Attempts {
	return dispositivoAttempts{d: d, id: id}
}

type dispositivoAttempts struct {
	d  *Devices
	id DeviceID
}

// dispositivoParado é quanto a conta de um dispositivo sem tentativa nenhuma
// fica na memória (e nunca antes do fim do período ignorado).
const dispositivoParado = 24 * time.Hour

func (a dispositivoAttempts) Reserve(context.Context) (bool, error) {
	d := a.d
	d.mu.Lock()
	defer d.mu.Unlock()
	agora := d.clk.Now()
	for id, c := range d.contas {
		if agora.Sub(c.visto) > dispositivoParado && !agora.Before(c.ignorarAte) {
			delete(d.contas, id)
		}
	}
	c, ok := d.contas[a.id]
	if !ok {
		c = &contaDeDispositivo{}
		d.contas[a.id] = c
	}
	c.visto = agora
	if agora.Before(c.ignorarAte) {
		return false, nil
	}
	if !c.ignorarAte.IsZero() {
		// O período ignorado passou: a contagem recomeça, como no bloqueio
		// vencido da conta.
		c.reservadas, c.ignorarAte = 0, time.Time{}
	}
	c.reservadas++
	if c.reservadas >= LockThreshold {
		c.ignorarAte = agora.Add(LockDuration)
	}
	return true, nil
}

func (a dispositivoAttempts) Refund(context.Context) error {
	d := a.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.contas[a.id]; ok && c.reservadas > 0 {
		c.reservadas--
		if c.reservadas < LockThreshold {
			c.ignorarAte = time.Time{}
		}
	}
	return nil
}

func (a dispositivoAttempts) Reset(context.Context) error {
	d := a.d
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.contas, a.id)
	return nil
}
