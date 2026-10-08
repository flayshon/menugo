-- Postal codes (or prefixes of them) a zone delivers to, normalized to
-- uppercase letters and digits. The primary key means each entry belongs to
-- at most one zone per restaurant.
CREATE TABLE delivery_zone_postal_codes (
    restaurant_id BIGINT UNSIGNED NOT NULL,
    postal_code   VARCHAR(10)     NOT NULL,
    zone_id       BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (restaurant_id, postal_code),
    KEY delivery_zone_postal_codes_zone_idx (restaurant_id, zone_id),
    CONSTRAINT delivery_zone_postal_codes_zone_fk FOREIGN KEY (restaurant_id, zone_id)
        REFERENCES delivery_zones (restaurant_id, id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
