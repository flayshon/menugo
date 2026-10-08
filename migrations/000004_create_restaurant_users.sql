-- Which users may act on which restaurant, and in what role. This table is the
-- tenant boundary for every authenticated request.
CREATE TABLE restaurant_users (
    restaurant_id BIGINT UNSIGNED NOT NULL,
    user_id       BIGINT UNSIGNED NOT NULL,
    role          ENUM ('restaurant_owner', 'restaurant_admin', 'restaurant_staff', 'driver') NOT NULL,
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (restaurant_id, user_id),
    KEY restaurant_users_user_id_idx (user_id),
    CONSTRAINT restaurant_users_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE,
    CONSTRAINT restaurant_users_user_fk FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
