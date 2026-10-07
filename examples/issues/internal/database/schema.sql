-- Each row keeps the document as submitted (body, as JSON) next to the
-- columns the app lists, sorts and joins on.

CREATE TABLE IF NOT EXISTS contacts (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE,
    role  TEXT,
    body  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS issues (
    id         INTEGER PRIMARY KEY,
    title      TEXT NOT NULL,
    severity   TEXT NOT NULL,
    status     TEXT NOT NULL,
    assignee   TEXT,
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    project TEXT NOT NULL,
    body    TEXT NOT NULL
);
