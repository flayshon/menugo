-- Why an order was cancelled, shown to the customer. Empty for other changes.
ALTER TABLE order_status_history
    ADD COLUMN reason VARCHAR(255) NOT NULL DEFAULT '' AFTER actor_user_id;
