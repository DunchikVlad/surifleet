// resources.go — сбор ResourceSummary для heartbeat (ТЗ п.4):
// загрузка CPU (два сэмпла /proc/stat), занятая память (/proc/meminfo),
// заполненность диска с логами (statfs). Без внешних библиотек, только /proc.
package main

import (
	"os"
	"strings"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// resourceSampler хранит предыдущий сэмпл CPU между heartbeat'ами
// (CPU% считается как доля busy-времени между двумя сэмплами).
type resourceSampler struct {
	prevIdle  uint64
	prevTotal uint64
	hasPrev   bool
}

// sample собирает текущий снимок ресурсов. diskPath — каталог логов
// инстанса (или "/" если discovery ничего не нашёл).
func (s *resourceSampler) sample(diskPath string) *agentv1.ResourceSummary {
	return &agentv1.ResourceSummary{
		CpuPercent:      s.cpuPercent(),
		MemBytes:        memUsedBytes(),
		DiskUsedPercent: diskUsedPercent(diskPath),
	}
}

// cpuSample — сырые счётчики первой строки /proc/stat ("cpu  user nice system idle ...").
type cpuSample struct{ idle, total uint64 }

// readCPUSample читает суммарные счётчики CPU из /proc/stat.
func readCPUSample() (cpuSample, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuSample{}, false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, false
	}
	var s cpuSample
	for i, f := range fields[1:] {
		var v uint64
		for _, c := range f {
			if c < '0' || c > '9' {
				return cpuSample{}, false
			}
			v = v*10 + uint64(c-'0')
		}
		s.total += v
		if i == 3 { // idle — 4-я колонка; iowait (5-я) тоже «простой»
			s.idle += v
		}
		if i == 4 {
			s.idle += v
		}
	}
	return s, true
}

// cpuPercent — доля busy-времени между предыдущим и текущим сэмплами, %.
// На первом сэмпле (нет базы) возвращает 0.
func (s *resourceSampler) cpuPercent() float64 {
	cur, ok := readCPUSample()
	if !ok {
		return 0
	}
	defer func() { s.prevIdle, s.prevTotal, s.hasPrev = cur.idle, cur.total, true }()
	if !s.hasPrev || cur.total <= s.prevTotal {
		return 0
	}
	dTotal := float64(cur.total - s.prevTotal)
	dIdle := float64(cur.idle - s.prevIdle)
	if dIdle > dTotal {
		dIdle = dTotal
	}
	return (dTotal - dIdle) / dTotal * 100
}

// memUsedBytes — занятая память: MemTotal - MemAvailable из /proc/meminfo.
func memUsedBytes() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, avail int64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var v int64
		for _, c := range fields[1] {
			if c < '0' || c > '9' {
				v = -1
				break
			}
			v = v*10 + int64(c-'0')
		}
		if v < 0 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024 // значения в KiB
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	if total <= 0 || avail > total {
		return 0
	}
	return total - avail
}

// diskUsedPercent — заполненность ФС, на которой лежит path, %.
// Реализации — statfs_linux.go / statfs_other.go (агент работает на Linux;
// на прочих ОС — заглушка, чтобы бинарь собирался кросс-платформенно).
