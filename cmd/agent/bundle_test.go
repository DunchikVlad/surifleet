package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackTarGzRoundTrip(t *testing.T) {
	files := []bundleFile{
		{name: "a.txt", data: []byte("hello")},
		{name: "dir/b.txt", data: []byte(strings.Repeat("x", 100))},
	}
	data, err := packTarGz(files)
	if err != nil {
		t.Fatalf("packTarGz: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		got[hdr.Name] = string(body)
	}
	if got["a.txt"] != "hello" {
		t.Errorf("a.txt = %q", got["a.txt"])
	}
	if got["dir/b.txt"] != strings.Repeat("x", 100) {
		t.Errorf("dir/b.txt длина = %d", len(got["dir/b.txt"]))
	}
}

func TestPackTarGzTotalLimit(t *testing.T) {
	big := make([]byte, bundleMaxTotalBytes)
	files := []bundleFile{{name: "big.bin", data: big}, {name: "extra", data: []byte("x")}}
	if _, err := packTarGz(files); err == nil {
		t.Fatal("ожидали ошибку превышения предела")
	}
}

func TestReadTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.log")
	content := strings.Repeat("0123456789", 1000) // 10 КБ
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := readTail(p, 100)
	if err != nil {
		t.Fatalf("readTail: %v", err)
	}
	if len(data) != 100 {
		t.Fatalf("длина хвоста = %d, хочу 100", len(data))
	}
	if string(data) != content[len(content)-100:] {
		t.Error("хвост не совпадает с концом файла")
	}
	// Меньше предела — весь файл.
	data, err = readTail(p, 1<<20)
	if err != nil {
		t.Fatalf("readTail full: %v", err)
	}
	if string(data) != content {
		t.Error("малый файл прочитан не целиком")
	}
	// Несуществующий.
	if _, err := readTail(filepath.Join(dir, "nope"), 10); err == nil {
		t.Fatal("ожидали ошибку на несуществующем файле")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"test1":         "test1",
		"my instance":   "my_instance",
		"eth0/main.yml": "eth0_main.yml",
		"":              "instance",
		"suricata-1.2":  "suricata-1.2",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, хочу %q", in, got, want)
		}
	}
}
