#!/bin/sh
# Create the two control-plane databases (§99).
#
# The global tier and a cell's tier are separate databases because they are separate
# *places*: the global tier is US-hosted and holds no player data, a cell's tier lives in
# the cell's region and holds everything that names a player. Locally they share one
# container so the development stack does not grow a service, while the code is shaped for
# the split that production actually has.
#
# This runs only when the data directory is empty. An existing stack needs them created by
# hand — `migrate` will say so plainly rather than inventing a database.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<EOSQL
  CREATE DATABASE "${PG_GLOBAL_DATABASE:-playersense_global}";
  CREATE DATABASE "${PG_CELL_DATABASE:-playersense_eu_1}";
EOSQL
