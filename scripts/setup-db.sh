#!/usr/bin/env bash
#
# Prepares a local MariaDB server for MenuGo development and testing.
#
# What it does:
#   1. Initializes the MariaDB data directory if it has never been initialized.
#   2. Starts the mariadb systemd service.
#   3. Creates the `menugo` database and a `menugo` user for the application.
#   4. Creates a `menugo_test` user that may create and drop databases named
#      `menugo_test_*` (the integration tests create one database per test).
#   5. Writes a `.envrc` file with DB_DSN and TEST_DB_DSN (if one doesn't exist).
#
# It is safe to run more than once: existing databases are kept and the users'
# passwords are reset to the ones written to .envrc.
#
# Usage:
#   ./scripts/setup-db.sh
#
# Environment overrides:
#   MARIADB          client command used to run admin SQL (default: "sudo mariadb")
#   DB_HOST/DB_PORT  where the app connects (default: 127.0.0.1:3306)
#   SKIP_SERVICE=1   don't initialize or start the systemd service

set -euo pipefail

MARIADB=${MARIADB:-sudo mariadb}
DB_HOST=${DB_HOST:-127.0.0.1}
DB_PORT=${DB_PORT:-3306}
DATADIR=${DATADIR:-/var/lib/mysql}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
envrc="$repo_root/.envrc"

gen_password() {
	# 24 random alphanumeric characters; safe to embed in a DSN and in SQL.
	# (Read a fixed amount of randomness so no pipe is closed early, which
	# would trip `set -o pipefail`.)
	local chars
	chars=$(head -c 512 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9')
	echo "${chars:0:24}"
}

# Reuse passwords from an existing .envrc so re-running doesn't break it.
existing_password() {
	local var=$1
	[[ -f $envrc ]] || return 0
	sed -n "s/^${var}=[^:]*:\([^@]*\)@.*/\1/p" "$envrc" | head -n 1
}

app_password=$(existing_password DB_DSN)
test_password=$(existing_password TEST_DB_DSN)
app_password=${app_password:-$(gen_password)}
test_password=${test_password:-$(gen_password)}

if [[ ${SKIP_SERVICE:-0} != 1 ]]; then
	if ! sudo test -d "$DATADIR/mysql"; then
		echo "==> Initializing MariaDB data directory in $DATADIR"
		sudo mariadb-install-db --user=mysql --basedir=/usr --datadir="$DATADIR"
	fi

	echo "==> Starting mariadb.service"
	sudo systemctl start mariadb
fi

echo "==> Creating databases and users"
# Users are created for both 'localhost' (unix socket) and '127.0.0.1' (TCP),
# because MariaDB treats them as different hosts.
$MARIADB <<SQL
CREATE DATABASE IF NOT EXISTS menugo CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE USER IF NOT EXISTS 'menugo'@'localhost';
CREATE USER IF NOT EXISTS 'menugo'@'127.0.0.1';
ALTER USER 'menugo'@'localhost' IDENTIFIED BY '${app_password}';
ALTER USER 'menugo'@'127.0.0.1' IDENTIFIED BY '${app_password}';
GRANT ALL PRIVILEGES ON menugo.* TO 'menugo'@'localhost';
GRANT ALL PRIVILEGES ON menugo.* TO 'menugo'@'127.0.0.1';

CREATE USER IF NOT EXISTS 'menugo_test'@'localhost';
CREATE USER IF NOT EXISTS 'menugo_test'@'127.0.0.1';
ALTER USER 'menugo_test'@'localhost' IDENTIFIED BY '${test_password}';
ALTER USER 'menugo_test'@'127.0.0.1' IDENTIFIED BY '${test_password}';
GRANT ALL PRIVILEGES ON \`menugo\_test\_%\`.* TO 'menugo_test'@'localhost';
GRANT ALL PRIVILEGES ON \`menugo\_test\_%\`.* TO 'menugo_test'@'127.0.0.1';

FLUSH PRIVILEGES;
SQL

db_dsn="menugo:${app_password}@tcp(${DB_HOST}:${DB_PORT})/menugo"
test_db_dsn="menugo_test:${test_password}@tcp(${DB_HOST}:${DB_PORT})/"

if [[ -f $envrc ]]; then
	echo "==> $envrc already exists; leaving it alone. Expected values:"
	echo "DB_DSN=$db_dsn"
	echo "TEST_DB_DSN=$test_db_dsn"
else
	echo "==> Writing $envrc"
	umask 077
	cat >"$envrc" <<EOF
# Local development settings. Read by the Makefile. Do not commit.
ENV=development
PORT=4000
DB_DSN=${db_dsn}
TEST_DB_DSN=${test_db_dsn}
EOF
fi

echo "==> Done. Next: make db/migrate && make run/api"
