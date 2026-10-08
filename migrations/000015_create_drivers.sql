-- A driver's profile in one restaurant. Drivers are users with the 'driver'
-- role in restaurant_users; the composite foreign key means a profile can't
-- exist without that membership, and goes away with it.
CREATE TABLE drivers (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id BIGINT UNSIGNED NOT NULL,
    user_id       BIGINT UNSIGNED NOT NULL,
    name          VARCHAR(100)    NOT NULL,
    phone         VARCHAR(16)     NOT NULL,
    vehicle       VARCHAR(100)    NOT NULL DEFAULT '',
    -- Inactive drivers can't be given new deliveries.
    is_active     BOOLEAN         NOT NULL DEFAULT TRUE,
    version       INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY drivers_restaurant_id_id_uk (restaurant_id, id),
    UNIQUE KEY drivers_restaurant_user_uk (restaurant_id, user_id),
    KEY drivers_user_idx (user_id),
    CONSTRAINT drivers_membership_fk FOREIGN KEY (restaurant_id, user_id)
        REFERENCES restaurant_users (restaurant_id, user_id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
