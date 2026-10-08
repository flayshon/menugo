# MenuGo API reference

Base path: `/v1`. All request and response bodies are JSON.

## Conventions

**Authentication.** Protected endpoints need an `Authorization: Bearer <token>`
header; get a token from `POST /v1/tokens/authentication`. A missing token on
a protected endpoint, or an invalid or expired token anywhere, gives
`401 Unauthorized` with `WWW-Authenticate: Bearer`.

**Responses.** Successful responses wrap the result in a named key, e.g.
`{"restaurant": {...}}`. Times are RFC 3339 in UTC. Every response has an
`X-Request-ID` header; quote it when reporting a problem.

**Request bodies.** Must be a single JSON object of at most 1 MB. Unknown
fields are rejected with `400 Bad Request`, so typos don't fail silently.

**Errors.** Always `{"error": ...}`:

```json
{"error": "the requested resource could not be found"}
```

Validation failures (`422 Unprocessable Entity`) map field names to messages:

```json
{"error": {"email": "must be a valid email address", "password": "must be at least 8 characters long"}}
```

| Status | Meaning |
|--------|---------|
| 400 | Malformed JSON, wrong types, unknown fields |
| 401 | Missing, invalid or expired token; bad login credentials |
| 403 | Your role in this restaurant doesn't allow the action |
| 404 | Not found, **or a restaurant you aren't a member of** |
| 405 | Method not supported for this path (see the `Allow` header) |
| 409 | Edit conflict (stale `version`) or duplicate |
| 422 | Validation failed |
| 500 | Unexpected server error (details are only in the server log) |

**Roles.** Each user has a role in each restaurant they belong to:

| Role | View restaurant | Update restaurant | Delete restaurant | Manage members | View menu | Edit menu | Mark items sold out |
|------|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `restaurant_owner` | ✓ | ✓ | ✓ | admins, staff, drivers | ✓ | ✓ | ✓ |
| `restaurant_admin` | ✓ | ✓ |   | staff, drivers | ✓ | ✓ | ✓ |
| `restaurant_staff` | ✓ |   |   |   | ✓ |   | ✓ |
| `driver`           | ✓ |   |   |   |   |   |   |

---

## Health

### `GET /v1/healthcheck`

No authentication. `200` if the API and database are reachable, `503`
otherwise.

```json
{"status": "available", "system_info": {"environment": "development", "version": "0.1.0"}}
```

## Users and authentication

### `POST /v1/users`: register

| Field | Rules |
|-------|-------|
| `name` | required, ≤ 100 characters |
| `email` | required, valid, ≤ 254 characters, unique (case-insensitive) |
| `password` | required, ≥ 8 characters, ≤ 72 bytes |

`201 Created`:

```json
{"user": {"id": 1, "name": "Alice", "email": "alice@example.com", "created_at": "2026-10-08T18:24:43.35Z"}}
```

### `GET /v1/users/me`: the authenticated user

`200 OK` with `{"user": {...}}`, as above.

### `POST /v1/tokens/authentication`: log in

```json
{"email": "alice@example.com", "password": "pa55word123"}
```

`201 Created`:

```json
{"authentication_token": {"token": "GKOP2LNLZDQOZW56EBOXLOOWYF", "expiry": "2026-10-09T18:24:43.52Z"}}
```

Wrong email and wrong password both give the same `401`.

### `DELETE /v1/tokens/authentication`: log out

Revokes the token used to make the request. `204 No Content`.

## Restaurants

A restaurant:

```json
{
  "id": 1,
  "slug": "pizza-place",
  "name": "Pizza Place",
  "description": "",
  "phone": "",
  "email": "",
  "address_line": "",
  "city": "São Paulo",
  "postal_code": "",
  "currency": "BRL",
  "version": 1,
  "created_at": "2026-10-08T18:24:43.53Z",
  "updated_at": "2026-10-08T18:24:43.53Z"
}
```

| Field | Rules |
|-------|-------|
| `name` | required, ≤ 200 characters |
| `slug` | required, 3–63 characters, lowercase letters, digits and single hyphens, unique. Will be used in public menu URLs. |
| `currency` | required, one of `BRL USD EUR GBP CAD AUD` |
| `description` | ≤ 2000 characters |
| `phone` | ≤ 30 characters |
| `email` | valid if present |
| `address_line` / `city` / `postal_code` | ≤ 255 / 100 / 20 characters |

### `POST /v1/restaurants`: create

Any authenticated user. The caller becomes the `restaurant_owner`.
`201 Created` with `{"restaurant": {...}}` and a `Location` header.

### `GET /v1/restaurants`: list mine

The restaurants the caller belongs to, each with the caller's `role`:

```json
{"restaurants": [{"id": 1, "slug": "pizza-place", "...": "...", "role": "restaurant_owner"}]}
```

### `GET /v1/restaurants/{restaurantID}`

Any member. `{"restaurant": {...}}`.

### `PATCH /v1/restaurants/{restaurantID}`

Owners and admins. Send only the fields to change. Optionally include the
`version` you last saw; if the restaurant has changed since, you get `409` and
nothing is saved.

```json
{"name": "Pizza Place Centro", "version": 1}
```

`200 OK` with the updated restaurant (its `version` goes up by one).

### `DELETE /v1/restaurants/{restaurantID}`

Owners only. Deletes the restaurant and all its data. `204 No Content`.

## Members

### `GET /v1/restaurants/{restaurantID}/members`

Owners and admins.

```json
{"members": [{"user_id": 1, "name": "Alice", "email": "alice@example.com", "role": "restaurant_owner", "created_at": "..."}]}
```

### `POST /v1/restaurants/{restaurantID}/members`

Owners and admins. Gives an already registered user a role. You can only grant
roles below your own (`403` otherwise).

```json
{"email": "bob@example.com", "role": "restaurant_staff"}
```

`201 Created` with `{"member": {...}}`. `422` if no user has that email;
`409` if they're already a member.

### `DELETE /v1/restaurants/{restaurantID}/members/{userID}`

Owners and admins, for members whose role is below their own. Owners can't be
removed. `204 No Content`.

## Menu

All menu endpoints are under `/v1/restaurants/{restaurantID}/menu`. Prices are
integers in the minor unit of the restaurant's currency (`price_cents: 4500`
is R$ 45,00 for a BRL restaurant); decimals are rejected with `400`.

IDs from another restaurant behave as if they don't exist: `404` in the URL,
`422` when used as a `category_id`.

### `GET /v1/restaurants/{restaurantID}/menu`: the whole menu

Staff and up. Categories in `sort_order`, each with its items in `sort_order`
(ties broken by creation order). Hidden categories and sold-out items are
included; this is the management view, not the public menu.

```json
{
  "menu": {
    "categories": [
      {
        "id": 2, "name": "Pizzas", "description": "", "sort_order": 10, "is_visible": true,
        "version": 1, "created_at": "...", "updated_at": "...",
        "items": [
          {"id": 5, "category_id": 2, "name": "Margherita", "description": "Tomato, mozzarella, basil",
           "price_cents": 4500, "image_url": "https://cdn.example.com/m.jpg", "is_available": true,
           "sort_order": 1, "version": 1, "created_at": "...", "updated_at": "..."}
        ]
      }
    ]
  }
}
```

### Categories

A category:

| Field | Rules |
|-------|-------|
| `name` | required, ≤ 100 characters, unique within the restaurant (case-insensitive) |
| `description` | ≤ 500 characters |
| `sort_order` | 0–1,000,000, default 0. Lower comes first. |
| `is_visible` | default `true`. Hidden categories will be left out of the public menu. |

| Method and path | Who | Result |
|-----------------|-----|--------|
| `GET /menu/categories` | staff and up | `200` `{"categories": [...]}` in menu order |
| `POST /menu/categories` | owners, admins | `201` `{"category": {...}}` with `Location` |
| `GET /menu/categories/{categoryID}` | staff and up | `200` `{"category": {...}}` |
| `PATCH /menu/categories/{categoryID}` | owners, admins | `200`; partial update, optional `version` as for restaurants |
| `DELETE /menu/categories/{categoryID}` | owners, admins | `204`; `409` if the category still has items |

### Items

An item:

| Field | Rules |
|-------|-------|
| `category_id` | required; a category of this restaurant |
| `name` | required, ≤ 200 characters |
| `description` | ≤ 2000 characters |
| `price_cents` | required, integer, 0–10,000,000 |
| `image_url` | optional absolute `http`/`https` URL, ≤ 2048 characters. The API stores the link; it doesn't host images. |
| `is_available` | default `true`. `false` means sold out. |
| `sort_order` | 0–1,000,000, default 0 |

| Method and path | Who | Result |
|-----------------|-----|--------|
| `GET /menu/items[?category_id=N]` | staff and up | `200` `{"items": [...]}` |
| `POST /menu/items` | owners, admins | `201` `{"item": {...}}` with `Location` |
| `GET /menu/items/{itemID}` | staff and up | `200` `{"item": {...}}` |
| `PATCH /menu/items/{itemID}` | owners, admins | `200`; partial update (including moving to another category), optional `version` |
| `PUT /menu/items/{itemID}/availability` | staff and up | `200` `{"item": {...}}`; body `{"is_available": false}` |
| `DELETE /menu/items/{itemID}` | owners, admins | `204` |
