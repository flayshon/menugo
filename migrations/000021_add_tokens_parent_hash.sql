-- Stream tickets (scope 'stream') record the authentication token they were
-- issued from, so revoking that token (logging out) deletes them too, and
-- streams opened with them can keep checking it.
ALTER TABLE tokens
    ADD COLUMN parent_hash BINARY(32) NULL AFTER scope,
    ADD KEY tokens_parent_hash_idx (parent_hash),
    ADD CONSTRAINT tokens_parent_fk FOREIGN KEY (parent_hash) REFERENCES tokens (hash) ON DELETE CASCADE;
