CREATE TABLE users (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name          VARCHAR(100)    NOT NULL,
    email         VARCHAR(254)    NOT NULL,
    password_hash VARBINARY(60)   NOT NULL,
    version       INT UNSIGNED    NOT NULL DEFAULT 1,
    created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    -- The case-insensitive collation makes Alice@x.com and alice@x.com the same.
    UNIQUE KEY users_email_uk (email)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
