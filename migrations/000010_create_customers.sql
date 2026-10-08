-- People who have ordered from a restaurant. Customers don't have accounts;
-- they are identified per restaurant by their (normalized) phone number.
CREATE TABLE customers (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id BIGINT UNSIGNED NOT NULL,
    name          VARCHAR(100)    NOT NULL,
    phone         VARCHAR(16)     NOT NULL,
    email         VARCHAR(254)    NOT NULL DEFAULT '',
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY customers_restaurant_id_id_uk (restaurant_id, id),
    UNIQUE KEY customers_restaurant_phone_uk (restaurant_id, phone),
    CONSTRAINT customers_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
