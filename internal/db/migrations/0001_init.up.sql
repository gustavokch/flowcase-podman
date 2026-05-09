-- Initial schema for the Flowcase orchestrator. Mirrors the SQLAlchemy
-- models in flowcase/models/ exactly: column names, types, nullability,
-- and defaults.

CREATE TABLE user (
    id          TEXT     PRIMARY KEY,
    username    TEXT     NOT NULL UNIQUE,
    password    TEXT     NOT NULL,
    auth_token  TEXT     NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    groups      TEXT     NOT NULL,
    usertype    TEXT     NOT NULL DEFAULT 'Internal',
    protected   INTEGER  NOT NULL DEFAULT 0
);

CREATE TABLE "group" (
    id                  TEXT     PRIMARY KEY,
    display_name        TEXT     NOT NULL,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    protected           INTEGER  NOT NULL,
    perm_admin_panel    INTEGER  NOT NULL,
    perm_view_instances INTEGER  NOT NULL,
    perm_edit_instances INTEGER  NOT NULL,
    perm_view_users     INTEGER  NOT NULL,
    perm_edit_users     INTEGER  NOT NULL,
    perm_view_droplets  INTEGER  NOT NULL,
    perm_edit_droplets  INTEGER  NOT NULL,
    perm_view_registry  INTEGER  NOT NULL,
    perm_edit_registry  INTEGER  NOT NULL,
    perm_view_groups    INTEGER  NOT NULL,
    perm_edit_groups    INTEGER  NOT NULL
);

CREATE TABLE droplet (
    id                                TEXT    PRIMARY KEY,
    display_name                      TEXT    NOT NULL,
    description                       TEXT,
    image_path                        TEXT,
    droplet_type                      TEXT    NOT NULL,
    container_docker_image            TEXT,
    container_docker_registry         TEXT,
    container_cores                   INTEGER,
    container_memory                  INTEGER,
    container_persistent_profile_path TEXT,
    container_network                 TEXT,
    server_ip                         TEXT,
    server_port                       INTEGER,
    server_username                   TEXT,
    server_password                   TEXT,
    restricted_groups                 TEXT
);

CREATE TABLE droplet_instance (
    id         TEXT     PRIMARY KEY,
    droplet_id TEXT     NOT NULL REFERENCES droplet(id),
    user_id    TEXT     NOT NULL REFERENCES user(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE registry (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    url        TEXT     NOT NULL
);

CREATE TABLE log (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    level      TEXT     NOT NULL,
    message    TEXT     NOT NULL
);
