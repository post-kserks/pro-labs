-- ============================================================
-- Этап 1: Настройка безопасности и RBAC
-- TDE, RBAC, TLS, токены
-- ============================================================
-- VaultDB использует встроенные роли: admin, writer, reader
-- TDE настраивается через vaultdb.yaml
-- Токены управляются через HTTP admin endpoint

-- Проверяем статус шифрования (TDE)
SHOW ENCRYPTION STATUS;

-- Информация о RBAC ролях
SELECT 'VaultDB RBAC: admin=*, writer=DML, reader=SELECT' as rbac_info;

-- Создаем базу данных
CREATE DATABASE IF NOT EXISTS docvault;

-- Проверяем шифрование
SHOW ENCRYPTION STATUS;

-- Информация об управлении токенами
SELECT 'Token revocation: POST /admin/revoke-token with Bearer token' as token_info;
