-- Deleting a restaurant hides it instead of erasing it and its orders.
-- Slugs only need to be unique among restaurants that aren't deleted, so
-- the unique key moves to a column that is NULL once a restaurant is
-- deleted (unique keys allow any number of NULLs).
ALTER TABLE restaurants
    ADD COLUMN deleted_at DATETIME(6) NULL AFTER is_published,
    ADD COLUMN live_slug VARCHAR(63) AS (IF(deleted_at IS NULL, slug, NULL)) PERSISTENT,
    DROP INDEX restaurants_slug_uk,
    ADD UNIQUE KEY restaurants_live_slug_uk (live_slug);
