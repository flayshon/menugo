CREATE TABLE menu_categories (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id BIGINT UNSIGNED NOT NULL,
    name          VARCHAR(100)    NOT NULL,
    description   VARCHAR(500)    NOT NULL DEFAULT '',
    sort_order    INT             NOT NULL DEFAULT 0,
    is_visible    BOOLEAN         NOT NULL DEFAULT TRUE,
    version       INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    -- Target of menu_items' composite foreign key, which guarantees an item
    -- and its category belong to the same restaurant.
    UNIQUE KEY menu_categories_restaurant_id_id_uk (restaurant_id, id),
    UNIQUE KEY menu_categories_restaurant_name_uk (restaurant_id, name),
    CONSTRAINT menu_categories_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
