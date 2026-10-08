-- A short-lived log of changes, written in the same transaction as the
-- change itself, that API instances poll to push real-time updates to
-- clients (server-sent events). Rows are deleted after a day. No foreign
-- keys: it's a log, and it must not get in the way of deleting anything.
CREATE TABLE events (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id  BIGINT UNSIGNED NOT NULL,
    order_id       BIGINT UNSIGNED NOT NULL,
    type           VARCHAR(32)     NOT NULL,
    order_status   VARCHAR(32)     NOT NULL,
    -- The driver this event concerns, if any: the one assigned or
    -- unassigned, or the order's current driver for status changes.
    driver_user_id BIGINT UNSIGNED NULL,
    created_at     DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY events_created_idx (created_at),
    KEY events_restaurant_idx (restaurant_id, id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
