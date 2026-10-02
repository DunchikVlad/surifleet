-- 000008 down: удалить встроенные роли (user_roles удалятся каскадом).
DELETE FROM roles WHERE organization_id IS NULL AND name IN ('admin','operator','analyst','viewer');
