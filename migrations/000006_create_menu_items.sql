CREATE TABLE menu_items (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id BIGINT UNSIGNED NOT NULL,
    category_id   BIGINT UNSIGNED NOT NULL,
    name          VARCHAR(200)    NOT NULL,
    description   VARCHAR(2000)   NOT NULL DEFAULT '',
    -- Minor units of the restaurant's currency (e.g. centavos for BRL).
    price_cents   BIGINT          NOT NULL,
    image_url     VARCHAR(2048)   NOT NULL DEFAULT '',
    is_available  BOOLEAN         NOT NULL DEFAULT TRUE,
    sort_order    INT             NOT NULL DEFAULT 0,
    version       INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY menu_items_restaurant_id_id_uk (restaurant_id, id),
    KEY menu_items_category_idx (restaurant_id, category_id, sort_order),
    CONSTRAINT menu_items_price_check CHECK (price_cents >= 0),
    CONSTRAINT menu_items_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE,
    -- Composite key: a category from another restaurant can never be used.
    -- The API refuses to delete a category that still has items; the cascade
    -- is only for deleting a whole restaurant.
    CONSTRAINT menu_items_category_fk FOREIGN KEY (restaurant_id, category_id)
        REFERENCES menu_categories (restaurant_id, id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
