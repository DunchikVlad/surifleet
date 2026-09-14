-- 000003_discovery: снимок авто-discovery Suricata на хосте.
-- Агент после подключения к Hub присылает DiscoveryReport (proto agent.v1):
-- найденный бинарь (путь, версия, сборка из исходников), найденные инстансы
-- (suricata.yaml, каталоги правил/логов, интерфейсы захвата, systemd-юнит).
-- Hub кладёт отчёт целиком в hosts.discovery (jsonb, форма openapi
-- DiscoveryReport без обёртки host_id/received_at) и фиксирует время
-- получения в hosts.discovered_at. Отчёт эфемерен: каждый новый перезаписывает
-- предыдущий; подтверждённые пользователем инстансы живут в таблице instances.

ALTER TABLE hosts
    ADD COLUMN discovery     jsonb,
    ADD COLUMN discovered_at timestamptz;

COMMENT ON COLUMN hosts.discovery IS
    'Последний DiscoveryReport агента (jsonb): {instances[], binary{path,version,built_from_source,build_info}}; NULL — агент ещё не присылал';
COMMENT ON COLUMN hosts.discovered_at IS
    'Время получения последнего DiscoveryReport (UTC)';
