// Package web — встраивание собранного React-фронтенда (vite build → dist/)
// в бинарь сервера. В git закоммичен только dist/placeholder.txt, чтобы
// go:embed работал на чистом клоне без npm; реальная сборка dist игнорируется
// git'ом (см. .gitignore) и попадает в бинарь при перекате.
package web

import "embed"

// Dist — содержимое web/dist (React-сборка или placeholder).
//
//go:embed dist
var Dist embed.FS
