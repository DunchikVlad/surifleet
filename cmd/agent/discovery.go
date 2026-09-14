// discovery.go — авто-discovery существующей установки Suricata на хосте
// (ТЗ п.4): бинарь (путь, версия, флаги сборки, из пакета или из исходников),
// конфиги suricata.yaml (каталоги правил/логов, интерфейсы захвата),
// systemd-юнит и его состояние. Результат уходит на Hub сообщением
// DiscoveryReport; дальше пользователь подтверждает инстансы через
// POST /api/v1/hosts/{id}/confirm_discovery.
package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// Кандидаты расположения бинаря и конфига (пакетная установка и сборка
// из исходников с префиксом /usr/local).
var (
	binaryCandidates = []string{"/usr/bin/suricata", "/usr/sbin/suricata", "/usr/local/bin/suricata", "/usr/local/sbin/suricata"}
	configCandidates = []string{"/etc/suricata/suricata.yaml", "/usr/local/etc/suricata/suricata.yaml"}
)

// Дефолты Suricata, если ключей нет в yaml.
const (
	defaultRulesDir = "/var/lib/suricata/rules"
	defaultLogDir   = "/var/log/suricata"
)

// commandTimeout — лимит на внешние команды discovery (--build-info, systemctl).
const commandTimeout = 10 * time.Second

// discoverSuricata собирает DiscoveryReport. Возвращает nil, если бинарь
// Suricata на хосте не найден (тогда и отчёт слать бессмысленно).
func discoverSuricata(log *slog.Logger) *agentv1.DiscoveryReport {
	binPath, ok := findBinary()
	if !ok {
		log.Info("discovery: бинарь suricata не найден")
		return nil
	}

	bin := probeBinary(binPath, log)
	report := &agentv1.DiscoveryReport{Binary: bin}

	for _, cfgPath := range configCandidates {
		if _, err := os.Stat(cfgPath); err != nil {
			continue
		}
		rulesDir, logDir, ifaces := parseSuricataYAML(cfgPath)
		unit := findSystemdUnit(log)
		inst := &agentv1.DiscoveredInstance{
			Name:              instanceName(unit),
			ConfigPath:        cfgPath,
			RulesDir:          rulesDir,
			LogDir:            logDir,
			CaptureInterfaces: ifaces,
			SuricataVersion:   bin.GetVersion(),
			BuildFlags:        binFeatures(bin.GetBuildInfo()),
			SystemdUnit:       unit,
		}
		report.Instances = append(report.Instances, inst)
	}
	log.Info("discovery завершён",
		"binary", binPath, "version", bin.GetVersion(), "instances", len(report.GetInstances()))
	return report
}

// findBinary ищет исполняемый файл suricata: сначала PATH, затем типовые пути.
func findBinary() (string, bool) {
	if p, err := exec.LookPath("suricata"); err == nil {
		return p, true
	}
	for _, p := range binaryCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// probeBinary запускает `suricata --build-info` и разбирает вывод.
// built_from_source — эвристика: бинарь не принадлежит deb/rpm-пакету.
func probeBinary(path string, log *slog.Logger) *agentv1.SuricataBinary {
	bin := &agentv1.SuricataBinary{Path: path}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--build-info").Output()
	if err != nil {
		log.Warn("discovery: suricata --build-info не удался", "path", path, "err", err)
		return bin
	}
	info := string(out)
	bin.Version = binVersion(info)
	bin.BuildInfo = info
	bin.BuiltFromSource = builtFromSource(path)
	return bin
}

// versionRe — строка "This is Suricata version 8.0.3 RELEASE" / "Suricata version X".
var versionRe = regexp.MustCompile(`Suricata version ([0-9][0-9A-Za-z.\-]*)`)

// binVersion извлекает версию из вывода --build-info / -V.
func binVersion(buildInfo string) string {
	if m := versionRe.FindStringSubmatch(buildInfo); m != nil {
		return m[1]
	}
	return ""
}

// binFeatures извлекает строку флагов сборки ("Features: NFQ AF_PACKET ...").
func binFeatures(buildInfo string) string {
	for _, line := range strings.Split(buildInfo, "\n") {
		if v, ok := strings.CutPrefix(line, "Features:"); ok {
			return strings.Join(strings.Fields(v), " ")
		}
	}
	return ""
}

// builtFromSource — true, если бинарь не принадлежит пакетному менеджеру
// (сборка из исходников обычно лежит в /usr/local и dpkg/rpm о нём не знает).
func builtFromSource(path string) bool {
	if strings.HasPrefix(path, "/usr/local/") {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if _, err := exec.LookPath("dpkg-query"); err == nil {
		// dpkg-query -S путь → владелец-пакет; код 1 = «не найдено».
		return exec.CommandContext(ctx, "dpkg-query", "-S", path).Run() != nil
	}
	if _, err := exec.LookPath("rpm"); err == nil {
		return exec.CommandContext(ctx, "rpm", "-qf", path).Run() != nil
	}
	return false
}

// parseSuricataYAML — лёгкий разбор suricata.yaml без yaml-парсера:
// вытаскиваем только default-rule-path, default-log-dir и интерфейсы
// захвата из секций af-packet/pcap. Комментарии и плейсхолдер "default"
// отбрасываются; отсутствующие ключи заменяются дефолтами.
func parseSuricataYAML(path string) (rulesDir, logDir string, ifaces []string) {
	rulesDir, logDir = defaultRulesDir, defaultLogDir
	f, err := os.Open(path)
	if err != nil {
		return rulesDir, logDir, defaultIfaceFallback()
	}
	defer f.Close()

	section := "" // текущая секция верхнего уровня (отступ 0)
	var afPacket, pcap []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || strings.HasPrefix(trimmed, "%") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			key, _, _ := strings.Cut(trimmed, ":")
			section = key
			// Ключи верхнего уровня со скалярным значением.
			if v, ok := scalarValue(trimmed, "default-rule-path"); ok {
				rulesDir = v
			}
			if v, ok := scalarValue(trimmed, "default-log-dir"); ok {
				logDir = v
			}
			continue
		}
		// "- interface: NAME" внутри af-packet/pcap.
		if v, ok := scalarValue(strings.TrimPrefix(trimmed, "- "), "interface"); ok && v != "default" {
			switch section {
			case "af-packet":
				afPacket = append(afPacket, v)
			case "pcap":
				pcap = append(pcap, v)
			}
		}
	}
	switch {
	case len(afPacket) > 0:
		ifaces = afPacket
	case len(pcap) > 0:
		ifaces = pcap
	default:
		ifaces = defaultIfaceFallback()
	}
	return rulesDir, logDir, ifaces
}

// scalarValue разбирает строку вида "key: value [# комментарий]".
func scalarValue(line, key string) (string, bool) {
	k, v, ok := strings.Cut(line, ":")
	if !ok || strings.TrimSpace(k) != key {
		return "", false
	}
	// Инлайн-комментарий: отрезаем по " #".
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	v = strings.Trim(strings.TrimSpace(v), `"'`)
	if v == "" {
		return "", false
	}
	return v, true
}

// defaultIfaceFallback — интерфейс маршрута по умолчанию (`ip route show default`).
func defaultIfaceFallback() []string {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return []string{fields[i+1]}
		}
	}
	return nil
}

// findSystemdUnit — первый юнит suricata* из list-unit-files ("" — если systemd
// нет или юнита нет).
func findSystemdUnit(log *slog.Logger) string {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "list-unit-files", "suricata*",
		"--type=service", "--no-legend", "--no-pager").Output()
	if err != nil {
		log.Debug("discovery: systemctl list-unit-files не удался", "err", err)
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

// instanceName — имя инстанса из имени юнита ("suricata.service" → "suricata").
func instanceName(unit string) string {
	if unit == "" {
		return "suricata"
	}
	return strings.TrimSuffix(unit, ".service")
}

// normalizeUnitState приводит ActiveState systemd к набору протокола
// (active | inactive | failed | reloading).
func normalizeUnitState(activeState string) string {
	switch activeState {
	case "active", "inactive", "failed", "reloading":
		return activeState
	default: // activating, deactivating, maintenance, ...
		return "inactive"
	}
}

// unitStatus — состояние юнита и PID главного процесса (systemctl show).
func unitStatus(unit string) (state string, pid int32) {
	if unit == "" {
		return "", 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", unit,
		"-p", "ActiveState", "-p", "MainPID", "--no-pager").Output()
	if err != nil {
		return "", 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "ActiveState="); ok {
			state = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "MainPID="); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				pid = int32(n) //nolint:gosec — PID помещается в int32
			}
		}
	}
	return state, pid
}
