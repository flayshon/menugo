-- Restaurants start unpublished: their public menu is only visible once an
-- owner or admin turns this on.
ALTER TABLE restaurants
    ADD COLUMN is_published BOOLEAN NOT NULL DEFAULT FALSE AFTER currency;
