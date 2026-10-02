-- 000008: встроенные роли RBAC (чанк 28). organization_id IS NULL — роль
-- глобальная (встроенная). permissions — каталог строк вида rules.read;
-- '*' у admin — все права. Права ролей:
--   viewer:   чтение флота/хостов/агентов/правил/IOC/фидов
--   analyst:  viewer + rules.write, ioc.write, feeds.write
--   operator: viewer + hosts.write, rules.deploy
--   admin:    *
INSERT INTO roles (organization_id, name, description, permissions, is_builtin) VALUES
 (NULL, 'admin', 'Администратор: полный доступ, включая пользователей, роли и аудит',
  ARRAY['*'], true),
 (NULL, 'operator', 'Оператор развёртывания: деплой правил, управление хостами/инстансами/агентами; без пользователей и политик доступа',
  ARRAY['fleet.read','hosts.read','hosts.write','agents.read','rules.read','rules.deploy','ioc.read','feeds.read'], true),
 (NULL, 'analyst', 'Аналитик: управление правилами, IOC, фидами, тюнинг; без деплоя и управления хостами',
  ARRAY['fleet.read','hosts.read','agents.read','rules.read','rules.write','ioc.read','ioc.write','feeds.read','feeds.write'], true),
 (NULL, 'viewer', 'Наблюдатель: read-only дашборды, статусы, правила, IOC, фиды',
  ARRAY['fleet.read','hosts.read','agents.read','rules.read','ioc.read','feeds.read'], true);
