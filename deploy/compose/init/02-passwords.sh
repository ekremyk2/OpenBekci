#!/bin/sh
# Compose only: gives the three application roles the password from
# OPENBEKCI_DB_PASSWORD instead of the development default in 01-roles.sql.
#
# The postgres entrypoint `source`s this file when it is not executable, so it
# must not change shell options of the caller or call `exit`: everything runs
# in a subshell-style function that only returns.
openbekci_set_passwords() {
	pw="${OPENBEKCI_DB_PASSWORD:-}"
	case "$pw" in
	"" | changeme | change-me | password)
		echo "02-passwords.sh: OPENBEKCI_DB_PASSWORD is unset or a placeholder ('changeme'); set a real password in deploy/compose/.env (openssl rand -hex 24)" >&2
		return 1
		;;
	*[!A-Za-z0-9._~-]*)
		echo "02-passwords.sh: OPENBEKCI_DB_PASSWORD may only contain letters, digits and . _ ~ - (it is embedded in postgres:// URLs); use: openssl rand -hex 24" >&2
		return 1
		;;
	esac
	psql -v ON_ERROR_STOP=1 -U postgres -d postgres -v pw="$pw" <<'SQL'
ALTER ROLE openbekci_migrator PASSWORD :'pw';
ALTER ROLE openbekci_app PASSWORD :'pw';
ALTER ROLE openbekci_platform PASSWORD :'pw';
SQL
}
openbekci_set_passwords || exit 1
