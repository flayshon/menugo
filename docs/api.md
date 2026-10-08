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
| 429 | Rate limit exceeded; wait the number of seconds in `Retry-After` |
| 500 | Unexpected server error (details are only in the server log) |

**Roles.** Each user has a role in each restaurant they belong to:

| Role | View restaurant | Update restaurant | Delete restaurant | Manage members | View menu, zones & drivers | Edit menu, zones & drivers | Mark items sold out | Manage orders & assign drivers | Deliver |
|------|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `restaurant_owner` | ✓ | ✓ | ✓ | admins, staff, drivers | ✓ | ✓ | ✓ | ✓ |   |
| `restaurant_admin` | ✓ | ✓ |   | staff, drivers | ✓ | ✓ | ✓ | ✓ |   |
| `restaurant_staff` | ✓ |   |   |   | ✓ |   | ✓ | ✓ |   |
| `driver`           | ✓ |   |   |   |   |   |   |   | own deliveries |

---

## Health

### `GET /v1/healthcheck`

No authentication. `200` if the API and database are reachable, `503`
otherwise.

```json
{"status": "available", "system_info": {"environment": "development", "version": "0.1.0"}}
```

## Public menu

### `GET /v1/menus/{slug}`

No authentication. What customers see. `404` if the slug doesn't exist **or
the restaurant isn't published** (the two are indistinguishable).

- Only visible categories that have at least one item are included, in
  `sort_order`, each with its items in `sort_order`.
- Sold-out items are included with `"is_available": false`; show them, but
  don't let customers order them.
- Only customer-facing fields are returned (the restaurant's email, internal
  IDs, versions and timestamps are not). Item `id`s will be used to place
  orders.
- Response headers: `Cache-Control: public, max-age=60` (changes can take up
  to a minute to show) and `Access-Control-Allow-Origin: *`, so any website
  can embed the menu.

```json
{
  "menu": {
    "restaurant": {
      "slug": "pizza-place", "name": "Pizza Place", "description": "", "phone": "",
      "address_line": "", "city": "Recife", "postal_code": "", "currency": "BRL"
    },
    "categories": [
      {
        "id": 1, "name": "Pizzas", "description": "",
        "items": [
          {"id": 1, "name": "Margherita", "description": "", "price_cents": 4500, "image_url": "", "is_available": true}
        ]
      }
    ]
  }
}
```

### `GET /v1/menus/{slug}/delivery-quote?postal_code=...`

No authentication. Whether the restaurant delivers to a postal code, and on
what terms. Use it before checkout.

```json
{"delivery_quote": {"postal_code": "50030230", "fee_cents": 800, "min_order_cents": 5000, "currency": "BRL"}}
```

`422` with `{"error": {"postal_code": "..."}}` if the restaurant doesn't
deliver there.

## Ordering (customers)

Customers don't need an account. These endpoints allow any origin, including
the CORS preflight for `POST`, so a storefront on any domain can use them.

### `POST /v1/menus/{slug}/orders`: place an order

```json
{
  "fulfillment": "delivery",
  "customer": {"name": "Ana Souza", "phone": "+55 81 99999-0000", "email": ""},
  "address": {"line": "Rua da Aurora, 100", "details": "apto 12", "city": "Recife", "postal_code": "50030-230"},
  "items": [
    {"item_id": 1, "quantity": 1, "notes": "no olives"},
    {"item_id": 2, "quantity": 2}
  ],
  "notes": "ring the bell",
  "expected_total_cents": 6500
}
```

| Field | Rules |
|-------|-------|
| `fulfillment` | `delivery` or `pickup` |
| `customer.name` | required, ≤ 100 characters |
| `customer.phone` | required; spaces and punctuation are ignored; 8–15 digits, optional leading `+` |
| `customer.email` | optional, valid |
| `address` | required for delivery (`line`, `city`, `postal_code` required; `details` optional); must be absent for pickup |
| `items` | 1–50 lines; `quantity` 1–99; `notes` ≤ 200 characters. The same item may appear on several lines. |
| `notes` | ≤ 500 characters |
| `expected_total_cents` | optional: the total the customer was shown. If the real total differs (e.g. a price changed), nothing is ordered and you get `409` with the current `total_cents`. |

**Prices are never taken from the request.** Unit prices come from the menu,
the delivery fee from the delivery zone covering the postal code, and the
total is computed by the server. Sending a price field is a `400`.

`201 Created`:

```json
{
  "order": {
    "status": "pending",
    "fulfillment": "delivery",
    "restaurant": {"slug": "pizza", "name": "Pizza Place", "phone": ""},
    "items": [
      {"name": "Pizza", "quantity": 1, "unit_price_cents": 4500, "line_total_cents": 4500, "notes": "no olives"},
      {"name": "Soda", "quantity": 2, "unit_price_cents": 600, "line_total_cents": 1200, "notes": ""}
    ],
    "subtotal_cents": 5700,
    "delivery_fee_cents": 800,
    "total_cents": 6500,
    "currency": "BRL",
    "notes": "ring the bell",
    "placed_at": "2026-10-08T19:20:56.2Z",
    "status_history": [{"status": "pending", "at": "2026-10-08T19:20:56.2Z"}],
    "can_cancel": true
  },
  "tracking_token": "LQ6TQ6Y3K5Q5ZJ2D3OQ6YVNF5E"
}
```

The `tracking_token` is shown **only once**: the server stores only its hash.
Give it to the customer (e.g. as a link to your tracking page). Customer
details and the address are not included in this response or in tracking,
since tracking links tend to get shared.

Errors (`422` unless stated):

| Case | Field |
|------|-------|
| Item missing, from another restaurant, in a hidden category, or sold out | `items[i].item_id` |
| No active zone covers the postal code | `address.postal_code` |
| Subtotal below the zone's minimum | `items` |
| Restaurant not published | `404` |
| `expected_total_cents` differs | `409`, body includes `total_cents` |

### `GET /v1/tracking/{token}`

The order, as above (without `tracking_token`). `404` for unknown tokens.
Orders stay trackable even if the restaurant is later unpublished.

Cancelled orders' last `status_history` entry may include a `reason` given
by the restaurant.

### `POST /v1/tracking/{token}/cancel`

Cancels the order if the restaurant hasn't confirmed it yet (`can_cancel` is
`true`). `200` with the updated order; `409` once it can no longer be
cancelled by the customer.

Tracking also includes `driver`: the first name of the assigned driver while
the order is assigned or on its way, otherwise `null`.

### Order lifecycle

```
delivery: pending → confirmed → preparing → ready_for_delivery → out_for_delivery → delivered
pickup:   pending → confirmed → preparing → ready_for_pickup  → picked_up
```

Any other move is rejected. The restaurant can cancel until a delivery order
is `out_for_delivery` (pickup orders: until `picked_up`). Customers can only
cancel while the order is `pending`. Every change is recorded with who made it
and when.

## Orders (restaurant)

Under `/v1/restaurants/{restaurantID}/orders`, for owners, admins and staff.

An order, as the restaurant sees it:

```json
{
  "id": 17,
  "status": "confirmed",
  "fulfillment": "delivery",
  "customer": {"id": 4, "name": "Ana Souza", "phone": "+5581999990000", "email": ""},
  "address": {"line": "Rua da Aurora, 100", "details": "apto 12", "city": "Recife", "postal_code": "50030230"},
  "delivery_zone_id": 1,
  "items": [
    {"id": 31, "menu_item_id": 1, "name": "Pizza", "quantity": 1, "unit_price_cents": 4500, "line_total_cents": 4500, "notes": "no olives"}
  ],
  "subtotal_cents": 4500, "delivery_fee_cents": 800, "total_cents": 5300, "currency": "BRL",
  "notes": "ring the bell",
  "status_history": [
    {"from": null, "to": "pending", "actor_type": "customer", "actor_user_id": null, "reason": "", "at": "..."},
    {"from": "pending", "to": "confirmed", "actor_type": "user", "actor_user_id": 2, "reason": "", "at": "..."}
  ],
  "next_statuses": ["preparing", "cancelled"],
  "version": 2, "created_at": "...", "updated_at": "..."
}
```

- `address` and `delivery_zone_id` are `null` for pickup orders
  (`delivery_zone_id` also once the zone is deleted).
- `menu_item_id` is `null` once the item is deleted from the menu; the order
  keeps its name and price.
- `next_statuses` lists the moves allowed from the current status (see
  [Order lifecycle](#order-lifecycle)): use it to decide which buttons to show.

- `delivery` (single orders only) is the order's current driver assignment:
  the one in progress, or how the last one ended. Absent if no driver was
  ever assigned (or the last one was unassigned). See [Deliveries](#deliveries).

### `GET /v1/restaurants/{restaurantID}/orders`

Orders with their items (without `status_history`), plus pagination metadata.

| Query parameter | Meaning |
|-----------------|---------|
| `status` | comma-separated statuses, e.g. `pending,confirmed,preparing` |
| `fulfillment` | `delivery` or `pickup` |
| `from`, `to` | placed at or after `from` and before `to` (RFC 3339, e.g. `2026-10-08T00:00:00-03:00`) |
| `sort` | `-created_at` (newest first, default) or `created_at` (oldest first, e.g. for a kitchen queue) |
| `page`, `page_size` | default 1 and 20; `page_size` at most 100 |

```json
{
  "orders": [...],
  "metadata": {"current_page": 1, "page_size": 20, "first_page": 1, "last_page": 3, "total_records": 47}
}
```

### `GET /v1/restaurants/{restaurantID}/orders/{orderID}`

`200` `{"order": {...}}` with `status_history`.

### `PATCH /v1/restaurants/{restaurantID}/orders/{orderID}`

Changes the status; nothing else about an order can be changed.

```json
{"status": "preparing"}
{"status": "cancelled", "reason": "Out of dough tonight"}
```

`reason` (≤ 255 characters) is only accepted when cancelling, and is shown to
the customer. `200` with the updated order; `409` if the move isn't allowed
from the current status (including when someone else changed it first);
`422` for an unknown status.

## Drivers

Under `/v1/restaurants/{restaurantID}/drivers`. A driver is a registered user
with the `driver` role in the restaurant plus a driver profile. A user can
drive for several restaurants.

```json
{
  "id": 3, "user_id": 12, "email": "joao@example.com", "name": "João Silva",
  "phone": "81988887777", "vehicle": "Honda CG 160", "is_active": true,
  "version": 1, "created_at": "...", "updated_at": "..."
}
```

| Method and path | Who | Result |
|-----------------|-----|--------|
| `GET /drivers[?active=true]` | staff and up | `200` `{"drivers": [...]}` by name |
| `POST /drivers` | owners, admins | `201`; see below |
| `GET /drivers/{driverID}` | staff and up | `200` |
| `PATCH /drivers/{driverID}` | owners, admins | `200`; `name`, `phone`, `vehicle`, `is_active`, optional `version` |
| `DELETE /drivers/{driverID}` | owners, admins | `204`; removes the profile **and** the driver role. `409` while the driver has deliveries in progress. To pause someone, set `is_active: false` instead. |

`POST /drivers` takes `{"email", "phone", "name"?, "vehicle"?, "is_active"?}`.
The email must belong to a registered user. If they aren't a member yet they
get the `driver` role; if they're a member with another role, `422`. `name`
defaults to the user's name. Phones are normalized like customers' (8–15
digits).

Inactive drivers can finish their current deliveries but can't be given new
ones. Removing a driver through `DELETE /members/{userID}` follows the same
rules.

## Deliveries

A delivery is one driver assignment for a delivery order. It follows the
order, whoever moves it, staff or driver:

| Order becomes | Delivery becomes |
|---------------|------------------|
| `out_for_delivery` | `picked_up` |
| `delivered` | `delivered` |
| `cancelled` | `cancelled` |

Reassigning ends the current delivery as `unassigned` and starts a new one,
so the history is kept. Restaurants don't have to assign drivers: orders
without one move through every status as before.

```json
{
  "id": 9, "status": "assigned",
  "driver": {"id": 3, "name": "João Silva", "phone": "81988887777", "vehicle": "Honda CG 160"},
  "assigned_by_user_id": 2,
  "assigned_at": "...", "picked_up_at": null, "delivered_at": null, "ended_at": null
}
```

`driver` is `null` if the driver has since been removed.

### `PUT /v1/restaurants/{restaurantID}/orders/{orderID}/driver`

Staff and up. `{"driver_id": 3}` assigns (or reassigns) a driver. `200`
`{"delivery": {...}}`. Assigning the current driver again changes nothing.

- Only delivery orders that are `confirmed`, `preparing` or
  `ready_for_delivery` (`409` otherwise: once an order is out, its driver
  can't change).
- The driver must be an active driver of this restaurant (`422`).

### `DELETE /v1/restaurants/{restaurantID}/orders/{orderID}/driver`

Staff and up. Takes the driver off the order. `204`; `404` if it has none.

### For drivers: `/v1/me/deliveries`

Any authenticated user: their own deliveries, across every restaurant they
drive for.

- `GET /v1/me/deliveries` lists them, newest first. Use
  `?status=assigned,picked_up` for the ones in progress. Pagination as for
  orders.
- `GET /v1/me/deliveries/{deliveryID}` shows one. Other people's deliveries
  are `404`.
- `PATCH /v1/me/deliveries/{deliveryID}` reports progress:
  `{"status": "picked_up"}` when leaving with the order (the order must be
  `ready_for_delivery`), `{"status": "delivered"}` when it's handed over.
  The order moves with it, and its history records the driver. `409` if the
  order isn't ready yet or the delivery was reassigned; `422` for any other
  status.

```json
{
  "id": 9, "status": "assigned",
  "assigned_at": "...", "picked_up_at": null, "delivered_at": null, "ended_at": null,
  "restaurant": {"id": 1, "name": "Pizza Place", "phone": "", "address_line": "", "city": "Recife"},
  "order": {
    "id": 17, "status": "ready_for_delivery",
    "customer": {"name": "Ana Souza", "phone": "+5581999990000"},
    "address": {"line": "Rua da Aurora, 100", "details": "apto 12", "city": "Recife", "postal_code": "50030230"},
    "items": [{"name": "Pizza", "quantity": 1, "notes": "no olives"}],
    "total_cents": 5300, "currency": "BRL", "notes": "ring the bell"
  }
}
```

`customer` and `address` are only included while the delivery is in
progress (`assigned` or `picked_up`); they are `null` before and after.

Staff can also mark orders `out_for_delivery` and `delivered` themselves
(e.g. if a driver's phone dies); the history shows who did it.

## Delivery zones

Under `/v1/restaurants/{restaurantID}/delivery-zones`. A zone:

```json
{
  "id": 1, "name": "Centro", "fee_cents": 800, "min_order_cents": 5000, "is_active": true,
  "postal_codes": ["50030", "50040000"],
  "version": 1, "created_at": "...", "updated_at": "..."
}
```

| Field | Rules |
|-------|-------|
| `name` | required, ≤ 100 characters, unique within the restaurant |
| `fee_cents` | required, 0–10,000,000 (0 = free delivery) |
| `min_order_cents` | default 0. Minimum subtotal (before the fee). |
| `is_active` | default `true` |
| `postal_codes` | 1–1000 entries. Spaces and punctuation are removed and letters uppercased (`"50030-230"` → `"50030230"`); each entry must then have 2–10 letters or digits. |

**How postal codes match.** Each entry matches every postal code that starts
with it: `"50030"` covers `50030-000` to `50030-999`, and a full code covers
just itself. If several entries match, the **longest** decides, so you can
carve exceptions out of a large area with a more specific zone. If that zone
is inactive, the address isn't delivered to. An entry can only belong to one
zone per restaurant (`422` naming the entry otherwise).

| Method and path | Who | Result |
|-----------------|-----|--------|
| `GET /delivery-zones` | staff and up | `200` `{"delivery_zones": [...]}` by name |
| `POST /delivery-zones` | owners, admins | `201` `{"delivery_zone": {...}}` with `Location` |
| `GET /delivery-zones/{zoneID}` | staff and up | `200` |
| `PATCH /delivery-zones/{zoneID}` | owners, admins | `200`; partial update; `postal_codes`, if sent, replaces the whole list; optional `version` |
| `DELETE /delivery-zones/{zoneID}` | owners, admins | `204`. Existing orders keep their address and fee. |

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
  "is_published": false,
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
| `is_published` | default `false`. While `false`, the public menu returns 404. |

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

Owners only. `204 No Content`. The restaurant is unpublished and disappears
for everyone: members get `404` on all its endpoints and it leaves their
`GET /v1/restaurants` list. Its data is kept, so customers can still track
past orders, and its slug becomes free for a new restaurant. `409` while it
has orders in progress: finish or cancel them first. There is no undelete
endpoint yet.

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
