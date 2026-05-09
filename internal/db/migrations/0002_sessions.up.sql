-- scs/sqlite3store schema. The store is opaque (token, data blob, expiry)
-- so this never needs to evolve.
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expiry);
