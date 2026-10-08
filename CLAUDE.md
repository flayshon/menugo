# MenuGo API

Multi-tenant REST API (Go + MariaDB) for restaurant online ordering and
delivery. README.md covers setup and design; docs/api.md and
docs/openapi.yaml are the API reference. The original project brief is in
docs/go_delivery_backend_claude_code_prompt.txt (historical; everything in
it is built).

## Commands

- `make test`: all tests with -race. Integration tests skip unless
  `TEST_DB_DSN` is set; the Makefile loads it from `.envrc`.
- `make audit`: tidy check, gofmt, vet, tests. Run before committing.
- `make db/migrate`, `make db/migrations/new name=create_things`
- `make docs/lint`: validate docs/openapi.yaml (needs Node.js).
- `.envrc` uses `KEY=value` with parentheses in DSNs: it is not a shell
  script, so don't `source` it (and never print it: it holds DB passwords).

## Style

- Follow Let's Go / Let's Go Further, written with current Go (ServeMux
  patterns, log/slog, `for range n`, `wg.Go`, etc.).
- Standard library first. Dependencies today: go-sql-driver/mysql,
  x/crypto (bcrypt), x/time/rate. Ask before adding any.
- database/sql with plain SQL, no ORM. No repository/service interfaces;
  define interfaces only where consumed and needed.
- Handlers are methods on `application` in cmd/api; models and validation
  rules live in internal/data. Match the surrounding code's shape: the
  `getX(w, r) *data.X` helpers, `switch { case errors.Is(...) }` error
  mapping, dedicated request/response structs with JSON tags (never encode
  data models directly).

## Rules that must hold

- **Tenancy.** Restaurant routes go through `requireRestaurantRole`. Use
  `contextGetMembership(r).RestaurantID` to scope every query, never an ID
  from the body. Non-members get 404 (same body as missing), wrong role 403.
  Every query on restaurant-owned tables has `restaurant_id = ?`.
- **Money** is int64 minor units (`*_cents`). **Times** are UTC
  `DATETIME(6)`; in internal/data, use `now()` when writing them.
- **Order status** changes only through `transitionTx` (internal/data/orders.go):
  it locks the order row, applies the state machine (orderstatus.go), writes
  history, syncs the delivery and records an event. Anything that changes an
  order or its deliveries locks the order row first (avoids deadlocks) and
  calls `recordEvent` in the same transaction (real-time updates rely on it).
- Restaurants are soft-deleted; the membership check hides deleted ones.
- Never log request bodies, query strings, tokens or customer details.
  Routes with `{token}` in the path are logged by pattern (see logPath).
- Errors returned to clients never include internal details; use the
  helpers in cmd/api/errors.go.

## Database and migrations

- MariaDB can't roll back DDL: one schema change per migration file, and
  never edit an applied migration; add a new one.
- Duplicate-key and foreign-key errors are detected by constraint name
  (`isDuplicateKey`, `isForeignKeyViolation`), so name every constraint.
- Patterns in use: composite FKs `(restaurant_id, x_id)` for tenant
  integrity; generated columns with unique keys for "unique among active
  rows" (`live_slug`, `active_order_id`).

## Tests

- Unit tests for rules; integration tests against a real MariaDB via
  `testdb.New(t)`. Packages using it need `TestMain` calling `testdb.Main`.
- Test databases are reused (emptied between tests): don't assume IDs or
  that auto-increment starts at 1.
- New restaurant-scoped endpoints need a tenant-isolation test (another
  restaurant's IDs give 404, including through the caller's own restaurant
  URL) and a role test.
- When behaviour changes, update docs/api.md; when routes change, also
  docs/openapi.yaml (TestOpenAPIDocumentsEveryRoute enforces it).
