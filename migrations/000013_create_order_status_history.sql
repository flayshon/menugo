-- Every status an order has been in, who moved it there and when. The first
-- row (from_status NULL) is the order being placed.
CREATE TABLE order_status_history (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id BIGINT UNSIGNED NOT NULL,
    order_id      BIGINT UNSIGNED NOT NULL,
    from_status   VARCHAR(32)     NULL,
    to_status     VARCHAR(32)     NOT NULL,
    actor_type    ENUM ('customer', 'user', 'system') NOT NULL,
    actor_user_id BIGINT UNSIGNED NULL,
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY order_status_history_order_idx (restaurant_id, order_id, id),
    CONSTRAINT order_status_history_order_fk FOREIGN KEY (restaurant_id, order_id)
        REFERENCES orders (restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT order_status_history_user_fk FOREIGN KEY (actor_user_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
