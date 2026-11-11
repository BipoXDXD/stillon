package targets

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
)

func TestMudouOCriterioDeSaude(t *testing.T) {
	contem := &ContentRule{Mode: ContentContains, Value: "ok"}
	outra := &ContentRule{Mode: ContentContains, Value: "pronto"}
	base := fields{URL: "https://a.test/", Method: "GET", ContentRule: contem}

	casos := []struct {
		nome   string
		depois func(f *fields)
		quer   bool
	}{
		{"nada mudou", func(*fields) {}, false},
		{"outra URL", func(f *fields) { f.URL = "https://b.test/" }, true},
		{"outro método", func(f *fields) { f.Method = "HEAD" }, true},
		{"outra regra", func(f *fields) { f.ContentRule = outra }, true},
		{"regra removida", func(f *fields) { f.ContentRule = nil }, true},
		{"mesma regra em outro ponteiro", func(f *fields) { c := *contem; f.ContentRule = &c }, false},
		{"só o nome", func(f *fields) { f.Name = "novo" }, false},
		{"só o intervalo", func(f *fields) { f.IntervalSec = 600 }, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			depois := base
			c.depois(&depois)
			if got := mudouOCriterioDeSaude(base.URL, base.Method, contem, depois); got != c.quer {
				t.Errorf("mudouOCriterioDeSaude = %v, quer %v", got, c.quer)
			}
		})
	}
}

func TestMudouOCriterioDeSaudeSemRegraAntes(t *testing.T) {
	depois := fields{URL: "https://a.test/", Method: "GET", ContentRule: &ContentRule{Mode: ContentContains, Value: "ok"}}
	if !mudouOCriterioDeSaude("https://a.test/", "GET", nil, depois) {
		t.Error("pôr uma regra onde não havia deveria zerar a histerese")
	}
}

func TestTLSWarnDepois(t *testing.T) {
	sim, nao := true, false
	casos := []struct {
		nome  string
		patch *bool
		atual bool
		quer  bool
	}{
		{"ausente mantém ligado", nil, true, true},
		{"ausente mantém desligado", nil, false, false},
		{"desliga", &nao, true, false},
		{"liga", &sim, false, true},
		{"repete o valor", &sim, true, true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := (Patch{TLSWarn: c.patch}).tlsWarnDepois(c.atual); got != c.quer {
				t.Errorf("tlsWarnDepois = %v, quer %v", got, c.quer)
			}
		})
	}
}

func linhaDeTeste() db.Target {
	modo, valor, ponteiro := "contains", "ok", ""
	limiar := int32(800)
	return db.Target{
		ID: uuid.MustParse("00000000-0000-7000-8000-000000000001"), Name: "api", URL: "https://a.test/",
		Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
		DegradedThresholdMS: &limiar, Enabled: true, TlsWarn: true,
		CreatedAt:   time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		ContentMode: &modo, ContentValue: &valor, ContentJsonPointer: &ponteiro,
	}
}

func TestFieldsDaLinha(t *testing.T) {
	row := linhaDeTeste()
	headers := map[string]string{"X-Chave": "v"}
	tags := []string{"prod"}

	f := fieldsDaLinha(row, headers, tags)

	quer := fields{
		Name: "api", URL: "https://a.test/", Method: "GET",
		IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
		DegradedThresholdMS: row.DegradedThresholdMS, Headers: headers, Tags: tags,
		ContentRule: &ContentRule{Mode: ContentContains, Value: "ok"},
		urlGravada:  "https://a.test/",
	}
	if !reflect.DeepEqual(f, quer) {
		t.Errorf("fieldsDaLinha = %+v, quer %+v", f, quer)
	}
}

func TestFieldsDaLinhaSemRegra(t *testing.T) {
	row := linhaDeTeste()
	row.ContentMode, row.ContentValue, row.ContentJsonPointer = nil, nil, nil
	if f := fieldsDaLinha(row, nil, nil); f.ContentRule != nil {
		t.Errorf("ContentRule = %+v, quer nil", f.ContentRule)
	}
}

func TestComoTargetNaoVazaValorDeHeaderEOrdenaOsNomes(t *testing.T) {
	id := uuid.MustParse("00000000-0000-7000-8000-000000000002")
	criado := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	f := fields{
		Name: "api", URL: "https://a.test/", Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
		Headers: map[string]string{"X-Z": "segredo", "X-A": "segredo"}, Tags: []string{"prod"},
		ContentRule: &ContentRule{Mode: ContentContains, Value: "ok"},
	}

	got := f.comoTarget(id, true, false, criado)

	quer := Target{
		ID: id, Name: "api", URL: "https://a.test/", Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
		Enabled: true, TLSWarn: false, HeaderNames: []string{"X-A", "X-Z"}, Tags: []string{"prod"}, CreatedAt: criado,
		ContentRule: &ContentRule{Mode: ContentContains, Value: "ok"},
	}
	if !reflect.DeepEqual(got, quer) {
		t.Errorf("comoTarget = %+v, quer %+v", got, quer)
	}
}

func TestComoTargetSemHeadersTemListaVazia(t *testing.T) {
	got := fields{}.comoTarget(uuid.Nil, false, false, time.Time{})
	if got.HeaderNames == nil || len(got.HeaderNames) != 0 {
		t.Errorf("HeaderNames = %#v, quer lista vazia não nula", got.HeaderNames)
	}
}

func TestUpdateTargetParams(t *testing.T) {
	userID := uuid.MustParse("00000000-0000-7000-8000-0000000000aa")
	id := uuid.MustParse("00000000-0000-7000-8000-0000000000bb")
	proximo := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	modo, valor, ponteiro := "json_equals", "1", "/a"
	f := fields{
		Name: "api", URL: "https://a.test/", Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
		ContentRule: &ContentRule{Mode: ContentJSONEquals, Value: "1", JSONPointer: "/a"},
	}

	got := f.updateTargetParams(userID, id, []byte("cifrado"), true, proximo, true)

	quer := db.UpdateTargetParams{
		UserID: userID, ID: id, Name: "api", URL: "https://a.test/", Method: "GET",
		IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, HeadersEnc: []byte("cifrado"),
		Enabled: true, NextRunAt: proximo, TlsWarn: true,
		ContentMode: &modo, ContentValue: &valor, ContentJsonPointer: &ponteiro,
	}
	if !reflect.DeepEqual(got, quer) {
		t.Errorf("updateTargetParams = %+v, quer %+v", got, quer)
	}
}
