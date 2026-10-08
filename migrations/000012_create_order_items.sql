CREATE TABLE order_items (
    id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id    BIGINT UNSIGNED NOT NULL,
    order_id         BIGINT UNSIGNED NOT NULL,
    -- NULL once the menu item is deleted; name and price are kept below.
    menu_item_id     BIGINT UNSIGNED NULL,
    name             VARCHAR(200)    NOT NULL,
    unit_price_cents BIGINT          NOT NULL,
    quantity         INT             NOT NULL,
    line_total_cents BIGINT          NOT NULL,
    notes            VARCHAR(200)    NOT NULL DEFAULT '',
    PRIMARY KEY (id),
    KEY order_items_order_idx (restaurant_id, order_id),
    CONSTRAINT order_items_amounts_check CHECK (
        unit_price_cents >= 0 AND quantity > 0 AND line_total_cents = unit_price_cents * quantity),
    CONSTRAINT order_items_order_fk FOREIGN KEY (restaurant_id, order_id)
        REFERENCES orders (restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT order_items_menu_item_fk FOREIGN KEY (menu_item_id) REFERENCES menu_items (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
