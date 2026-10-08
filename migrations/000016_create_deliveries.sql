-- One row per driver assignment. Reassigning an order ends its current
-- delivery ('unassigned') and starts a new one, so the history is kept. The
-- address and customer are on the order.
CREATE TABLE deliveries (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id       BIGINT UNSIGNED NOT NULL,
    order_id            BIGINT UNSIGNED NOT NULL,
    -- NULL only if the driver's profile was later deleted.
    driver_id           BIGINT UNSIGNED NULL,
    status              ENUM ('assigned', 'picked_up', 'delivered', 'unassigned', 'cancelled') NOT NULL,
    assigned_by_user_id BIGINT UNSIGNED NULL,
    assigned_at         DATETIME(6)     NOT NULL,
    picked_up_at        DATETIME(6)     NULL,
    delivered_at        DATETIME(6)     NULL,
    -- When it was unassigned or cancelled.
    ended_at            DATETIME(6)     NULL,
    -- Equal to order_id while the delivery is in progress, NULL otherwise:
    -- the unique key below allows at most one active delivery per order.
    active_order_id     BIGINT UNSIGNED AS (IF(status IN ('assigned', 'picked_up'), order_id, NULL)) PERSISTENT,
    PRIMARY KEY (id),
    UNIQUE KEY deliveries_one_active_per_order_uk (restaurant_id, active_order_id),
    KEY deliveries_order_idx (restaurant_id, order_id),
    KEY deliveries_driver_status_idx (driver_id, status),
    CONSTRAINT deliveries_order_fk FOREIGN KEY (restaurant_id, order_id)
        REFERENCES orders (restaurant_id, id) ON DELETE CASCADE,
    -- Not composite: a composite ON DELETE SET NULL would also null
    -- restaurant_id. The application only assigns drivers of the order's
    -- restaurant, looked up inside the assigning transaction.
    CONSTRAINT deliveries_driver_fk FOREIGN KEY (driver_id) REFERENCES drivers (id) ON DELETE SET NULL,
    CONSTRAINT deliveries_assigned_by_fk FOREIGN KEY (assigned_by_user_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
