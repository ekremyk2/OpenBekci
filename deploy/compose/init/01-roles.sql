-- OpenBekci database bootstrap. Run as a superuser, once per cluster.
-- Used by the compose postgres container (docker-entrypoint-initdb.d) and by
-- tools/dev-db.ps1 / tools/dev-db.sh.
--
-- DEV PASSWORDS: every role below is created with the password 'openbekci'.
-- That is for local development and the compose quickstart ONLY. Change them
-- (ALTER ROLE ... PASSWORD ...) for any deployment reachable by others.

SELECT 'CREATE ROLE openbekci_migrator LOGIN BYPASSRLS PASSWORD ''openbekci'''
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'openbekci_migrator')\gexec

SELECT 'CREATE ROLE openbekci_app LOGIN NOBYPASSRLS PASSWORD ''openbekci'''
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'openbekci_app')\gexec

SELECT 'CREATE ROLE openbekci_platform LOGIN BYPASSRLS PASSWORD ''openbekci'''
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'openbekci_platform')\gexec

SELECT 'CREATE DATABASE openbekci OWNER openbekci_migrator'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'openbekci')\gexec

\connect openbekci

-- Tables and sequences created by the migrator are usable by the other roles.
ALTER DEFAULT PRIVILEGES FOR ROLE openbekci_migrator IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO openbekci_app, openbekci_platform;
ALTER DEFAULT PRIVILEGES FOR ROLE openbekci_migrator IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO openbekci_app, openbekci_platform;
ALTER DEFAULT PRIVILEGES FOR ROLE openbekci_migrator IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO openbekci_app, openbekci_platform;

REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT ALL ON SCHEMA public TO openbekci_migrator;
GRANT USAGE ON SCHEMA public TO openbekci_app, openbekci_platform;
