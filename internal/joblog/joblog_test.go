package joblog

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTailEntries_emptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := TailEntries(p, 10)
	if err != nil {
		t.Fatalf("TailEntries: %v", err)
	}
	if len(res.Entries) != 0 || res.ParseErrors != 0 || res.Truncated {
		t.Fatalf("ожидали пустой результат, got %+v", res)
	}
}

func TestTailEntries_sampleNDJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "send.log")
	content := `{"time":"2026-03-27T21:08:14.698503495Z","level":"INFO","event":"video_pipeline","request_id":"rid1","probe_ms":6416}
{"time":"2026-03-27T21:08:14.699677826Z","level":"INFO","event":"delivery","request_id":"rid1","status":"request"}

{"not":"json"
{"time":"2026-03-27T21:11:41.935292149Z","level":"INFO","event":"video_pipeline","request_id":"rid2"}
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := TailEntries(p, 10)
	if err != nil {
		t.Fatalf("TailEntries: %v", err)
	}
	if res.ParseErrors != 1 {
		t.Fatalf("ParseErrors: ожидали 1 битую строку, got %d", res.ParseErrors)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("ожидали 3 валидные записи, got %d", len(res.Entries))
	}
	if res.Entries[0].Fields["request_id"] != "rid1" || res.Entries[0].Fields["event"] != "video_pipeline" {
		t.Fatalf("первая запись: %+v", res.Entries[0].Fields)
	}
	if res.Truncated {
		t.Fatal("Truncated не ожидался")
	}
}

func TestTailEntries_maxLinesCap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "many.log")
	var b strings.Builder
	for i := 0; i < 120; i++ {
		b.WriteString(`{"n":`)
		b.WriteString(strings.Repeat("9", 3)) // длина числа не важна, нужны уникальные строки
		b.WriteString(`}`)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := TailEntries(p, 200)
	if err != nil {
		t.Fatalf("TailEntries: %v", err)
	}
	if !res.Truncated {
		t.Fatal("ожидали Truncated при запросе > MaxLinesLimit")
	}
	if len(res.Entries) != MaxLinesLimit {
		t.Fatalf("ожидали %d строк, got %d", MaxLinesLimit, len(res.Entries))
	}
}

func TestTailEntries_tailFromLargeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.log")
	// Много коротких строк + маркер в конце; чтение только хвоста должно вернуть последние строки.
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString(`{"i":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`}`)
		b.WriteByte('\n')
	}
	b.WriteString(`{"last":true,"request_id":"tail-marker"}`)
	b.WriteByte('\n')
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := TailEntries(p, 5)
	if err != nil {
		t.Fatalf("TailEntries: %v", err)
	}
	if len(res.Entries) != 5 {
		t.Fatalf("ожидали 5 записей, got %d", len(res.Entries))
	}
	last := res.Entries[len(res.Entries)-1].Fields
	if last["last"] != true || last["request_id"] != "tail-marker" {
		t.Fatalf("последняя запись должна быть маркером: %+v", last)
	}
}

// paddingLine возвращает одну длинную JSON-строку (~110 байт) для раздувания файла > maxReadChunk.
func paddingLine(i int) string {
	return `{"p":"` + strings.Repeat("a", 90) + `","i":` + strconv.Itoa(i) + `}` + "\n"
}

func TestTailEntries_largeFileUsesTailChunk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge.log")
	var b strings.Builder
	// > maxReadChunk байт валидного NDJSON, в конце маркер.
	for i := 0; i < 6000; i++ {
		b.WriteString(paddingLine(i))
	}
	b.WriteString(`{"last":true,"request_id":"chunk-tail"}`)
	b.WriteByte('\n')
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Size() <= int64(maxReadChunk) {
		t.Fatalf("фикстура должна быть больше maxReadChunk (%d), got %d", maxReadChunk, st.Size())
	}
	res, err := TailEntries(p, 3)
	if err != nil {
		t.Fatalf("TailEntries: %v", err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("ожидали 3 записи, got %d", len(res.Entries))
	}
	last := res.Entries[len(res.Entries)-1].Fields
	if last["last"] != true || last["request_id"] != "chunk-tail" {
		t.Fatalf("последняя запись — маркер: %+v", last)
	}
}

func TestTailEntries_maxLinesZero(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	_ = os.WriteFile(p, []byte(`{"a":1}`+"\n"), 0o600)
	res, err := TailEntries(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("ожидали 0 записей при maxLines=0, got %d", len(res.Entries))
	}
}
