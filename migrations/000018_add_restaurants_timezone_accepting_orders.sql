-- timezone: IANA name (e.g. America/Recife) that opening hours are in.
-- accepting_orders: a switch to pause orders, e.g. when the kitchen is
-- overwhelmed, independently of opening hours.
ALTER TABLE restaurants
    ADD COLUMN timezone VARCHAR(64) NOT NULL DEFAULT 'UTC' AFTER currency,
    ADD COLUMN accepting_orders BOOLEAN NOT NULL DEFAULT TRUE AFTER is_published;
