// Пакет joblog читает хвост NDJSON-файла (одна JSON-строка на строку) без загрузки всего файла в память.
package joblog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// MaxLinesLimit — верхняя граница числа строк в одном запросе (память и лимиты Telegram).
const MaxLinesLimit = 100

// maxReadChunk — максимум байт для одного чтения с конца файла (запас под ~100 длинных JSON-строк).
const maxReadChunk = 512 * 1024

// ParsedLine — одна успешно распарсенная запись NDJSON.
type ParsedLine struct {
	// Fields — полный объект JSON в виде map (для сериализации в API).
	Fields map[string]any
}

// TailResult — результат чтения хвоста лога.
type TailResult struct {
	Entries     []ParsedLine
	ParseErrors int  // строки, которые не удалось разобрать как JSON
	Truncated   bool // запрошенный maxLines был больше MaxLinesLimit
}

// TailEntries читает последние непустые строки из path, парсит каждую как JSON-объект.
// Пустые строки пропускаются; битые JSON-строки увеличивают ParseErrors, остальные строки всё равно возвращаются.
// maxLines <= 0 даёт пустой Entries без ошибки. maxLines ограничивается MaxLinesLimit (Truncated=true).
func TailEntries(path string, maxLines int) (TailResult, error) {
	var out TailResult
	if maxLines <= 0 {
		return out, nil
	}
	if maxLines > MaxLinesLimit {
		out.Truncated = true
		maxLines = MaxLinesLimit
	}

	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return out, err
	}
	size := st.Size()
	if size == 0 {
		return out, nil
	}

	chunk := int64(maxReadChunk)
	if size < chunk {
		chunk = size
	}
	start := size - chunk
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return out, err
	}
	buf := make([]byte, chunk)
	if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return out, err
	}

	lines := splitNonEmptyLines(buf, start > 0)
	if len(lines) == 0 {
		return out, nil
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	out.Entries = make([]ParsedLine, 0, len(lines))
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			out.ParseErrors++
			continue
		}
		out.Entries = append(out.Entries, ParsedLine{Fields: m})
	}
	return out, nil
}

// splitNonEmptyLines режет buf по '\n', отбрасывает пустые; если skipFirstIncomplete — первую строку (обрезанную при seek).
func splitNonEmptyLines(buf []byte, skipFirstIncomplete bool) [][]byte {
	raw := bytes.Split(buf, []byte{'\n'})
	if skipFirstIncomplete && len(raw) > 0 {
		raw = raw[1:]
	}
	var out [][]byte
	for _, ln := range raw {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		out = append(out, ln)
	}
	return out
}
