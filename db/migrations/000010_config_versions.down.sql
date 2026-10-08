-- 000010 down
UPDATE roles SET permissions = array_remove(array_remove(permissions, 'config.read'), 'config.write')
WHERE name = 'operator' AND is_builtin;
DROP TABLE IF EXISTS config_versions;
