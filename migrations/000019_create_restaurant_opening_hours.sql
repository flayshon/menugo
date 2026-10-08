-- A restaurant's weekly schedule, in its own time zone. Times are minutes
-- since midnight. An interval whose closes_minute is not after opens_minute
-- runs past midnight into the next day. No rows means always open.
CREATE TABLE restaurant_opening_hours (
    id            BIGINT UNSIGNED  NOT NULL AUTO_INCREMENT,
    restaurant_id BIGINT UNSIGNED  NOT NULL,
    day_of_week   TINYINT UNSIGNED NOT NULL, -- 0 = Sunday, as Go's time.Weekday
    opens_minute  SMALLINT         NOT NULL,
    closes_minute SMALLINT         NOT NULL,
    PRIMARY KEY (id),
    KEY restaurant_opening_hours_restaurant_idx (restaurant_id, day_of_week),
    CONSTRAINT restaurant_opening_hours_day_check CHECK (day_of_week <= 6),
    CONSTRAINT restaurant_opening_hours_times_check CHECK (
        opens_minute BETWEEN 0 AND 1439 AND closes_minute BETWEEN 1 AND 1440 AND opens_minute <> closes_minute),
    CONSTRAINT restaurant_opening_hours_restaurant_fk FOREIGN KEY (restaurant_id) REFERENCES restaurants (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
