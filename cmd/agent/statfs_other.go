//go:build !linux

package main

// diskUsedPercent — заглушка для не-Linux платформ (агент работает только
// на Linux; сборка на прочих ОС нужна для локальной проверки на Windows).
func diskUsedPercent(string) float64 { return 0 }
