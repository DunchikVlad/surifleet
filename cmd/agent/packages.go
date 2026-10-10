// packages.go — управление пакетами Suricata на сенсоре (чанк 111, backlog
// заказчика 2026-10-11; capability packages): сервер шлёт PackageTask
// (check/install/remove/update по пакету suricata|suricata-update), агент
// выполняет через apt/dpkg (сенсоры Ubuntu/Debian, агент от root). Журнал
// идемпотентности не пишется: apt сам идемпотентен по семантике, повторные
// install/remove — безопасные no-op (как у service_action чанка 105).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// Пределы на операции с пакетами.
const (
	pkgCheckTimeout = 30 * time.Second // dpkg-query — быстро
	pkgAptTimeout   = 5 * time.Minute  // apt-get (загрузка/распаковка)
)

// pkgWhitelist — какие пакеты управляемы через capability packages.
var pkgWhitelist = map[string]bool{
	"suricata":        true,
	"suricata-update": true,
}

// executePackages — check/install/remove/update пакета suricata или
// suricata-update.
func (e *taskExecutor) executePackages(task *agentv1.Task, pt *agentv1.PackageTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "package", pt.GetPackage(), "action", pt.GetAction())

	if !e.hasCap("packages") {
		e.failPackage(taskID, nil, "capability packages не включена для хоста")
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failPackage(taskID, nil, "дедлайн задачи истёк")
		return
	}
	pkg := strings.TrimSpace(pt.GetPackage())
	if !pkgWhitelist[pkg] {
		e.failPackage(taskID, nil, "пакет вне белого списка: "+pkg+" (доступны: suricata, suricata-update)")
		return
	}
	verb, args, ok := packageCmd(pt.GetAction(), pkg)
	if !ok {
		e.failPackage(taskID, nil, "неизвестное действие: "+pt.GetAction().String())
		return
	}

	res := &agentv1.PackageResult{Package: pkg, Action: packageActionName(pt.GetAction())}
	timeout := pkgAptTimeout
	if pt.GetAction() == agentv1.PackageAction_PACKAGE_ACTION_CHECK {
		timeout = pkgCheckTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, verb, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	res.Output = tail(string(out), 15)
	if err != nil && pt.GetAction() != agentv1.PackageAction_PACKAGE_ACTION_CHECK {
		// check читает код возврата как факт «не установлен» — не ошибка.
		e.failPackage(taskID, res, fmt.Sprintf("%s %s: %v; вывод: %s", verb, strings.Join(args, " "), err, res.Output))
		return
	}
	// Фактическое состояние после действия — из dpkg (для check это и есть
	// само действие; код возврата dpkg-query ненулевой при отсутствии).
	installed, version := packageStatus(pkg)
	res.Installed = installed
	res.Version = version
	log.Info("задача над пакетом завершена", "installed", installed, "version", version)
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_Packages{Packages: res},
	})
}

// packageCmd — маппинг (action, пакет) → команда apt/dpkg.
func packageCmd(a agentv1.PackageAction, pkg string) (string, []string, bool) {
	switch a {
	case agentv1.PackageAction_PACKAGE_ACTION_CHECK:
		return "dpkg-query", []string{"-W", "-f=${Status} ${Version}", pkg}, true
	case agentv1.PackageAction_PACKAGE_ACTION_INSTALL:
		return "apt-get", []string{"install", "-y", pkg}, true
	case agentv1.PackageAction_PACKAGE_ACTION_REMOVE:
		return "apt-get", []string{"remove", "-y", pkg}, true
	case agentv1.PackageAction_PACKAGE_ACTION_UPDATE:
		return "apt-get", []string{"install", "--only-upgrade", "-y", pkg}, true
	default:
		return "", nil, false
	}
}

// packageStatus — установлен ли пакет и какая версия (dpkg-query).
func packageStatus(pkg string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), pkgCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${db:Status-Status} ${Version}", pkg).Output()
	if err != nil {
		return false, ""
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 || fields[0] != "installed" {
		return false, ""
	}
	if len(fields) > 1 {
		return true, fields[1]
	}
	return true, ""
}

// packageActionName — строковое имя действия (для результата/аудита).
func packageActionName(a agentv1.PackageAction) string {
	switch a {
	case agentv1.PackageAction_PACKAGE_ACTION_CHECK:
		return "check"
	case agentv1.PackageAction_PACKAGE_ACTION_INSTALL:
		return "install"
	case agentv1.PackageAction_PACKAGE_ACTION_REMOVE:
		return "remove"
	case agentv1.PackageAction_PACKAGE_ACTION_UPDATE:
		return "update"
	default:
		return "unknown"
	}
}

// failPackage — failed с деталями PackageResult (без журнала идемпотентности:
// apt-операции перевыполнимы).
func (e *taskExecutor) failPackage(taskID string, res *agentv1.PackageResult, msg string) {
	e.log.Warn("задача над пакетом завершилась ошибкой", "task_id", taskID, "error", msg)
	var details *agentv1.TaskResult_Packages
	if res != nil {
		details = &agentv1.TaskResult_Packages{Packages: res}
	}
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:   msg,
		Details: details,
	})
}
