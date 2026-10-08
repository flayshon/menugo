CREATE TABLE delivery_zones (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id   BIGINT UNSIGNED NOT NULL,
    name            VARCHAR(100)    NOT NULL,
    fee_cents       BIGINT          NOT NULL,
    min_order_cents BIGINT          NOT NULL DEFAULT 0,
    is_active       BOOLEAN         NOT NULL DEFAULT TRUE,
    version         INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at      DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY delivery_zones_restaurant_id_id_uk (restaurant_id, id),
    UNIQUE KEY delivery_zones_restaurant_name_uk (restaurant_id, name),
    CONSTRAINT delivery_zones_fee_check CHECK (fee_cents >= 0),
    CONSTRAINT delivery_zones_min_order_check CHECK (min_order_cents >= 0),
    CONSTRAINT delivery_zones_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
