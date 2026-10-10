package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Параметры карты sid→источник (чанк 95): suricata-update хранит
// включённые источники в update/sources/*.yaml (поле source: <имя>) и
// скачанные тарболы в update/cache/<md5(url)>-*.tar.gz (md5 от URL —
// URL берём из update/cache/index.yaml).
const (
	updateStateDir   = "/var/lib/suricata/update"
	indexPath        = updateStateDir + "/cache/index.yaml"
	sourcesDir       = updateStateDir + "/sources"
	cacheDir         = updateStateDir + "/cache"
	sidMapMaxEntries = 500_000
)

var (
	idxSrcRe  = regexp.MustCompile(`^\s{2}(\S+):\s*$`) // «  et/open:» под sources:
	idxURLRe  = regexp.MustCompile(`^\s+url:\s*(\S+)`)  // «    url: https://...»
	yamlSrcRe = regexp.MustCompile(`^source:\s*(\S+)`)  // source: et/open
)

// buildSidSourceMap — sid → имя источника suricata-update. Для каждого
// включённого источника (yaml в sources/) имя берём из поля source:,
// URL из index.yaml, тарбол — cache/<md5(url)>-*.tar.gz; sids — из всех
// .rules внутри тарбола. Без ошибок на отсутствующих файлах (защитно).
func buildSidSourceMap() map[int64]string {
	out := map[int64]string{}

	// URL источников из index.yaml: имя → url.
	urls := map[string]string{}
	cur := ""
	if raw, err := os.ReadFile(indexPath); err == nil {
		for _, ln := range strings.Split(string(raw), "\n") {
			if m := idxSrcRe.FindStringSubmatch(ln); m != nil {
				cur = m[1]
				continue
			}
			if m := idxURLRe.FindStringSubmatch(ln); m != nil && cur != "" {
				if _, ok := urls[cur]; !ok {
					urls[cur] = m[1]
				}
			}
		}
	}

	// Для каждого включённого источника — тарбол по md5(url).
	entries, err := os.ReadDir(sourcesDir)
	if err != nil {
		return out
	}
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sourcesDir, en.Name()))
		if err != nil {
			continue
		}
		src := ""
		for _, ln := range strings.Split(string(raw), "\n") {
			if m := yamlSrcRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
				src = m[1]
				break
			}
		}
		if src == "" {
			continue
		}
		url, ok := urls[src]
		if !ok {
			continue
		}
		sum := md5.Sum([]byte(url))
		glob := filepath.Join(cacheDir, fmt.Sprintf("%x-*", sum))
		tars, err := filepath.Glob(glob)
		if err != nil || len(tars) == 0 {
			continue
		}
		for _, tf := range tars {
			sidsFromTarInto(tf, src, out)
		}
	}
	return out
}

// sidsFromTarInto — все sid из .rules-файлов тарбола → out[src].
func sidsFromTarInto(path, src string, out map[int64]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".rules") {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, 64<<20))
		if err != nil {
			continue
		}
		for sid := range parseSids(data) {
			if len(out) >= sidMapMaxEntries {
				return
			}
			out[sid] = src
		}
	}
}
