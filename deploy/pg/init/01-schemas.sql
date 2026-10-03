-- One PostgreSQL, one schema per service (spec §3.1 #12). Runs once on an
-- empty data volume. Service roles/grants are added by each service's
-- golang-migrate migrations in stage 1+.
CREATE SCHEMA IF NOT EXISTS gateway;
CREATE SCHEMA IF NOT EXISTS download;
CREATE SCHEMA IF NOT EXISTS music;
CREATE EXTENSION IF NOT EXISTS pg_trgm; -- search (spec §5.4)
