-- Customer details, the delivery address and all amounts are copied into the
-- order when it is placed, so later changes to the customer, menu or zones
-- never change an existing order.
CREATE TABLE orders (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id       BIGINT UNSIGNED NOT NULL,
    customer_id         BIGINT UNSIGNED NOT NULL,
    -- SHA-256 of the customer's tracking token; the token itself isn't stored.
    tracking_token_hash BINARY(32)      NOT NULL,
    fulfillment         ENUM ('delivery', 'pickup') NOT NULL,
    status              ENUM ('pending', 'confirmed', 'preparing', 'ready_for_delivery', 'ready_for_pickup',
                              'out_for_delivery', 'delivered', 'picked_up', 'cancelled') NOT NULL,
    customer_name       VARCHAR(100)    NOT NULL,
    customer_phone      VARCHAR(16)     NOT NULL,
    customer_email      VARCHAR(254)    NOT NULL DEFAULT '',
    address_line        VARCHAR(255)    NOT NULL DEFAULT '',
    address_details     VARCHAR(255)    NOT NULL DEFAULT '',
    address_city        VARCHAR(100)    NOT NULL DEFAULT '',
    address_postal_code VARCHAR(10)     NOT NULL DEFAULT '',
    delivery_zone_id    BIGINT UNSIGNED NULL,
    notes               VARCHAR(500)    NOT NULL DEFAULT '',
    currency            CHAR(3)         NOT NULL,
    subtotal_cents      BIGINT          NOT NULL,
    delivery_fee_cents  BIGINT          NOT NULL,
    total_cents         BIGINT          NOT NULL,
    version             INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY orders_restaurant_id_id_uk (restaurant_id, id),
    UNIQUE KEY orders_tracking_token_hash_uk (tracking_token_hash),
    KEY orders_restaurant_status_created_idx (restaurant_id, status, created_at),
    KEY orders_restaurant_created_idx (restaurant_id, created_at),
    CONSTRAINT orders_amounts_check CHECK (
        subtotal_cents >= 0 AND delivery_fee_cents >= 0 AND total_cents = subtotal_cents + delivery_fee_cents),
    CONSTRAINT orders_pickup_has_no_fee_check CHECK (fulfillment = 'delivery' OR delivery_fee_cents = 0),
    CONSTRAINT orders_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE,
    CONSTRAINT orders_customer_fk FOREIGN KEY (restaurant_id, customer_id)
        REFERENCES customers (restaurant_id, id) ON DELETE CASCADE,
    -- Not a composite key: deleting a zone must keep its orders, and a
    -- composite ON DELETE SET NULL would also null restaurant_id.
    CONSTRAINT orders_delivery_zone_fk FOREIGN KEY (delivery_zone_id) REFERENCES delivery_zones (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
