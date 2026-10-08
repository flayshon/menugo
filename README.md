# MenuGo API

Backend for a multi-tenant SaaS that gives restaurants online ordering and
delivery logistics: menus, a public online menu, orders, delivery zones and
drivers.

Written in Go with the standard library (`net/http`, `database/sql`,
`log/slog`) and MariaDB, in the style of Alex Edwards' *Let's Go Further*.

**Status:** all planned phases are implemented: users, authentication,
restaurants, membership/roles, menu management, the public menu, delivery
zones, customer ordering (delivery and pickup, with tracking and
cancellation), order management, drivers, driver assignment, the delivery
lifecycle and real-time updates (server-sent events), plus rate limiting,
soft deletion and opening hours.

API reference: [docs/api.md](docs/api.md) (prose, with the reasoning behind
the rules) and [docs/openapi.yaml](docs/openapi.yaml) (OpenAPI 3.1, for
tools: client generators, Swagger UI, Postman). A test fails if a route is
added or removed without updating the OpenAPI file; `make docs/lint`
validates it (needs Node.js).

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
| `LIMITER_ENABLED`   | `true`        | Rate limiting on or off                          |
| `LIMITER_RPS`       | `20`          | General limit: requests per second per client IP |
| `LIMITER_BURST`     | `40`          | General limit: burst per client IP               |
| `TRUST_PROXY_HEADERS` | `false`     | Take the client IP from the last `X-Forwarded-For` entry. Enable **only** behind a reverse proxy that appends it; otherwise clients can spoof their IP. |
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
run only when `TEST_DB_DSN` is set, and are skipped otherwise. Each running
test gets a migrated, empty `menugo_test_<random>` database of its own, so
they run in parallel. Databases are reused between tests (emptied in
between) and dropped when the package's tests finish (`testdb.Main` in each
package's `TestMain`). Tests must therefore not assume particular IDs. The
setup script creates a `menugo_test` user that may only touch databases with
that prefix.

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
- **Orders.** The server prices every order from the menu and delivery zone;
  clients can't send prices. Everything about an order (customer details,
  address, item names and prices, fee) is copied into it, so menu or zone
  changes never alter placed orders. Placing an order is one transaction.
  Status changes go through one state machine (`internal/data/orderstatus.go`),
  lock the order row, and are recorded in `order_status_history`.
- **Deliveries.** A delivery records one driver assignment and follows the
  order's status, whether staff or the driver moves it. Everything that
  changes an order or its deliveries locks the order row first, so they
  serialize without deadlocks. A generated column with a unique key lets the
  database itself guarantee at most one active delivery per order. Drivers
  only see customer contact details while a delivery is in progress.
- **Real-time updates.** Changes to orders and deliveries write a row to
  `events` in the same transaction, so there's an event exactly when a change
  commits. Each API instance polls that table every second and fans events
  out (`internal/events`) to the server-sent event streams connected to it,
  so this works with several instances. The poller re-reads a 10-second
  window, because auto-increment IDs can commit out of order, and skips IDs
  it has already published. Streams lift the server's read and write
  timeouts for themselves, send heartbeats, re-check access every minute,
  and are closed when shutdown starts so it doesn't wait on them. Events are
  deleted after 24 hours.
- **Customer tracking.** Customers get a random tracking token; only its hash
  is stored. Tracking responses leave out the customer's contact details and
  address. Request logs show `/v1/tracking/{token}` instead of the real path.
- **Responses.** Handlers return dedicated response structs, never database
  models. Errors are always `{"error": …}`; validation errors map field names
  to messages.
- **Deletion.** Deleting a restaurant is a soft delete (`deleted_at`): it
  disappears for members and customers, but its orders are kept. Slugs are
  unique only among live restaurants (a generated column, `live_slug`, is
  NULL once deleted). The membership check every restaurant route goes
  through ignores deleted restaurants, so access is revoked in one place.
  Other deletions (menu items, zones, drivers) keep orders intact through
  copied names and prices.
- **Opening hours.** A weekly schedule per restaurant in its own IANA time
  zone (`time/tzdata` is embedded, so this works without a system time zone
  database), plus a pause switch staff can flip. Placing an order checks
  both inside the order's transaction.
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
- **Rate limiting.** Token buckets per client IP for all requests
  (configurable), plus fixed, stricter limits for registering and logging in
  (10/min per IP), login attempts per email from any IP (10 per 15 min), and
  placing or cancelling public orders (20/min per IP). Over the limit is `429`
  with `Retry-After`. State is in memory, per instance: running several
  instances needs a shared store (e.g. Redis) or sticky load balancing.
- **Dependencies.** `github.com/go-sql-driver/mysql`, `golang.org/x/crypto`
  (bcrypt) and `golang.org/x/time/rate` (rate limiting). Everything else is
  the standard library, including routing (`http.ServeMux` patterns) and
  migrations.

### Not done yet

- Saved customer addresses: each order carries its own address for now.
- CORS for the authenticated API, for when a management front end is on
  another origin. (The public menu already allows any origin.)
- Email verification and password reset (needs a mailer).
- Invitations for people who don't have an account yet.
- Pagination for menus and zones: those lists return everything, which is
  fine at their sizes. Order lists are paginated.
- Image uploads: items store an image URL; hosting is up to the client.
