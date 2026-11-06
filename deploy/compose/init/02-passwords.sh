#!/bin/sh
# Compose only: gives the three application roles the password from
# OPENBEKCI_DB_PASSWORD instead of the development default in 01-roles.sql.
set -eu
psql -v ON_ERROR_STOP=1 -U postgres -d postgres -v pw="$OPENBEKCI_DB_PASSWORD" <<'SQL'
ALTER ROLE openbekci_migrator PASSWORD :'pw';
ALTER ROLE openbekci_app PASSWORD :'pw';
ALTER ROLE openbekci_platform PASSWORD :'pw';
SQL
