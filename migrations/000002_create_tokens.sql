-- Only the SHA-256 hash of each token is stored, never the token itself.
CREATE TABLE tokens (
    hash       BINARY(32)      NOT NULL,
    user_id    BIGINT UNSIGNED NOT NULL,
    scope      VARCHAR(32)     NOT NULL,
    expiry     DATETIME(6)     NOT NULL,
    created_at DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (hash),
    KEY tokens_user_id_idx (user_id),
    KEY tokens_expiry_idx (expiry),
    CONSTRAINT tokens_user_fk FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
