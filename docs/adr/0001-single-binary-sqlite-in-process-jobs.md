# One binary on SQLite, with durable job queues inside the process

Proxier runs as one Go binary in one container and stores its state in one SQLite file. Worker pools in that process execute jobs stored in the database, including provisioning, checks, syncs, refreshes and notifications.

The application serves a few members and a small number of servers and routers. A separate database server, message broker or worker service would add deployment and backup work at this scale. SQLite uses one writer and WAL mode. Stored jobs support retries, cancellation, live logs and recovery after a restart. Backups use consistent database snapshots.

## Considered options

- **Separate worker binary sharing the SQLite file** (the alcs pattern): independent restarts, but two writers on one SQLite file, and nothing gained at this scale.
- **Remote workers or agents pulling jobs over an API**: needed only if work must run elsewhere. If probe agents arrive, they will be an external-checker driver, not a second job system.
- **Postgres + a queue library**: more infrastructure for no current need.

## Consequences

- Every job step must be safe to run twice, because an interrupted job resumes from its first unfinished step after a restart.
- Splitting the work across processes later would mean moving to a database that tolerates several writers. Keep module code free of in-process assumptions beyond the job API.
