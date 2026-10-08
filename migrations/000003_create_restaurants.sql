CREATE TABLE restaurants (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    slug         VARCHAR(63)     NOT NULL,
    name         VARCHAR(200)    NOT NULL,
    description  VARCHAR(2000)   NOT NULL DEFAULT '',
    phone        VARCHAR(30)     NOT NULL DEFAULT '',
    email        VARCHAR(254)    NOT NULL DEFAULT '',
    address_line VARCHAR(255)    NOT NULL DEFAULT '',
    city         VARCHAR(100)    NOT NULL DEFAULT '',
    postal_code  VARCHAR(20)     NOT NULL DEFAULT '',
    currency     CHAR(3)         NOT NULL,
    version      INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at   DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at   DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY restaurants_slug_uk (slug)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
