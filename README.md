# MenuGo API

Backend for a multi-tenant SaaS that gives restaurants online ordering and
delivery logistics: menus, a public online menu, orders, delivery zones and
drivers.

Written in Go with the standard library (`net/http`, `database/sql`,
`log/slog`) and MariaDB, in the style of Alex Edwards' *Let's Go Further*.

**Status:** Phase 2. Users, authentication, restaurants, membership/roles and
menu management (categories and items) are implemented. The public menu,
orders, delivery zones, drivers and deliveries come in later phases.

API reference: [docs/api.md](docs/api.md).

## Requirements

- Go 1.27+
- MariaDB 10.6+ (uses `INSERT … RETURNING`)

## Getting started

```sh
# 1. Create the databases and users, and write .envrc (asks for sudo).
#    On a fresh install it also initializes the data directory and starts
#    mariadb.service.
./scripts/setup-db.sh

# 2. Create the schema.
make db/migrate

# 3. Run the API (http://localhost:4000).
make run/api
```

Try it:

```sh
curl localhost:4000/v1/healthcheck

curl -X POST localhost:4000/v1/users \
  -d '{"name": "Alice", "email": "alice@example.com", "password": "pa55word123"}'

curl -X POST localhost:4000/v1/tokens/authentication \
  -d '{"email": "alice@example.com", "password": "pa55word123"}'

curl -X POST localhost:4000/v1/restaurants -H "Authorization: Bearer <token>" \
  -d '{"name": "Pizza Place", "slug": "pizza-place", "currency": "BRL"}'
```

If you'd rather not use the setup script, create a database and user yourself
and copy `.envrc.example` to `.envrc`.

## Configuration

Configuration comes from environment variables (the Makefile loads `.envrc`).
Each can be overridden with a command-line flag; run `go run ./cmd/api -h`.
Invalid settings stop the application at startup.

| Variable            | Default       | Description                                      |
|---------------------|---------------|--------------------------------------------------|
| `DB_DSN`            | *(required)*  | MariaDB DSN, e.g. `user:pass@tcp(127.0.0.1:3306)/menugo` |
| `PORT`              | `4000`        | HTTP port                                        |
| `ENV`               | `development` | `development`, `staging` or `production`. Development logs text; the others log JSON. |
| `DB_MAX_OPEN_CONNS` | `25`          | Connection pool size                             |
| `DB_MAX_IDLE_CONNS` | `25`          | Idle connections kept in the pool                |
| `DB_MAX_IDLE_TIME`  | `15m`         | How long an idle connection is kept              |
| `AUTH_TOKEN_TTL`    | `24h`         | Lifetime of authentication tokens                |
| `SHUTDOWN_TIMEOUT`  | `30s`         | Time allowed for in-flight requests on shutdown  |
| `TEST_DB_DSN`       | *(unset)*     | Tests only; see below                            |

Whatever the DSN says, the application always connects with `parseTime`, UTC
as both the Go location and the session `time_zone`, strict (`TRADITIONAL`)
SQL mode and `utf8mb4_unicode_ci`.

## Migrations

Migrations are plain SQL files in [`migrations/`](migrations), embedded in the
binary and applied in order by `go run ./cmd/api -migrate` (`make db/migrate`).
Applied versions are recorded in `schema_migrations`.

- Create one with `make db/migrations/new name=create_things`.
- There are no down migrations; to undo a change, write a new migration.
- MariaDB can't roll back DDL, so keep one table change per file. If a
  migration fails part way, it's marked dirty and later runs refuse to
  continue until you've fixed the schema by hand and deleted its
  `schema_migrations` row.

## Tests

```sh
make test     # go test -race ./...
make audit    # tidy check, gofmt, go vet, tests
```

Unit tests need nothing. Integration tests (data access and HTTP endpoints)
run only when `TEST_DB_DSN` is set, and are skipped otherwise. Each one
creates its own `menugo_test_<random>` database, migrates it and drops it at
the end, so they run in parallel and leave nothing behind. The setup script
creates a `menugo_test` user that may only touch databases with that prefix.

## Project layout

```
cmd/api/            HTTP layer: main, config, server lifecycle, routes,
                    middleware, handlers, JSON helpers, error responses
internal/data/      domain types, validation rules and SQL for each model
internal/database/  opening MariaDB connections; running migrations
internal/migrate/   the migration runner
internal/testdb/    per-test databases for integration tests
internal/validator/ validation helper
migrations/         SQL migrations (embedded)
scripts/            local setup
docs/               API reference
```

## Design notes

- **Tenancy.** The restaurant is the tenant. Restaurant-scoped routes live
  under `/v1/restaurants/{restaurantID}/…` and go through
  `requireRestaurantRole`, which loads the caller's row from
  `restaurant_users` and puts it in the request context. Handlers scope every
  query with that membership's `RestaurantID`, never with an ID from the
  request body. Non-members get a 404 (same as a missing restaurant), so IDs
  can't be probed; members with the wrong role get a 403.
- **Roles.** `restaurant_owner`, `restaurant_admin`, `restaurant_staff`,
  `driver`. Owners and admins manage the restaurant; only owners delete it.
  Members can only grant or revoke roles below their own, and ownership can't
  be granted or removed through the API yet.
- **Authentication.** Stateful bearer tokens, as in *Let's Go Further*:
  128 random bits, with only the SHA-256 hash stored, so tokens can be revoked
  (logout) and a database leak doesn't leak usable tokens. Expired tokens are
  deleted hourly. Passwords use bcrypt (cost 12). Logins for unknown emails
  still run a bcrypt comparison, so response times don't reveal which emails
  are registered.
- **Responses.** Handlers return dedicated response structs, never database
  models. Errors are always `{"error": …}`; validation errors map field names
  to messages.
- **Concurrency.** Editable records carry a `version`. Updates only apply if
  the version hasn't changed since the record was read (409 otherwise), and
  clients can send `version` to make sure they're editing what they saw.
- **Tenant integrity in the schema.** Besides scoping every query by
  restaurant, child tables reference their parents through composite keys
  where it matters: `menu_items (restaurant_id, category_id)` references
  `menu_categories (restaurant_id, id)`, so the database itself rejects an
  item in another restaurant's category.
- **Money and time.** Amounts are integer minor units (`price_cents BIGINT`);
  JSON decimals are rejected. Times are `DATETIME(6)` in UTC.
- **Dependencies.** `github.com/go-sql-driver/mysql` and `golang.org/x/crypto`
  (bcrypt). Everything else is the standard library, including routing
  (`http.ServeMux` patterns) and migrations.

### Not done yet

- Rate limiting, especially on login (planned before production).
- CORS, for when a browser front end is on another origin.
- Email verification and password reset (needs a mailer).
- Invitations for people who don't have an account yet.
- Pagination: menu lists return everything, which is fine at restaurant-menu
  sizes.
- Image uploads: items store an image URL; hosting is up to the client.
