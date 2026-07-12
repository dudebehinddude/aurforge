# Aurforge

Aurforge is a personal, self-hosted Arch package builder and pacman repository.
It imports AUR packages or validated local `PKGBUILD` directories, delays AUR builds
until each commit is old enough, builds in disposable Arch containers, and
publishes resulting package archives for pacman and yay clients.

## Host Requirements

- Ubuntu Server with Docker Engine and the Docker Compose plugin
- An external PostgreSQL database reachable through `DATABASE_URL`
- An x86_64 host for the current Arch builder image

## Configure And Start

Copy `.env.example` to `.env`. Set `DATABASE_URL`. `AURFORGE_DATA_ROOT` and
`AURFORGE_LOCAL_IMPORT_ROOT` may be relative to the compose project directory;
the worker resolves the absolute host path from the data-root bind mount.

If PostgreSQL exposes a port on the same Ubuntu host, use
`host.docker.internal` in `DATABASE_URL`, for example:

```text
postgres://aurforge:password@host.docker.internal:5432/aurforge?sslmode=disable
```

Aurforge maps that hostname to the Docker host. A database on another machine
can use its normal LAN hostname or IP address instead.

To reach Postgres on another Compose project without publishing its port, join
that project's Docker network instead:

```env
AURFORGE_DATABASE_NETWORK=postgres_default
AURFORGE_DATABASE_EXTERNAL=true
DATABASE_URL=postgres://aurforge:password@postgres:5432/aurforge?sslmode=disable
```

`AURFORGE_DATABASE_NETWORK` must be the existing network name (`docker network ls`).
`postgres` in the URL is the database container/service name on that network.

Create the required storage directories on the server:

```sh
sudo install -d -m 0750 /srv/aurforge/{sources,cache,staging,repo,logs}
sudo install -d -m 0750 /srv/aurforge-imports
```

Start the control services and repository endpoint (`--build` also refreshes the
makepkg builder image the worker launches):

```sh
docker compose up --build -d
```

While the controller is running, Aurforge installs a host `aurforge` command into
`/usr/local/bin`. Stopping or removing the controller deletes that command again.

## Import Packages

```sh
aurforge add <aur-query>
aurforge add --local <package>
aurforge update --local <package>
aurforge remove <package>
aurforge status
```

`add <query>` searches the AUR and asks you to select a package. Aurforge clones
the selected AUR package, resolves its recursive AUR dependency graph, displays
the package metadata and static audit warnings, then asks for one confirmation.

`add --local` takes a package directory name under the host import root
(`AURFORGE_LOCAL_IMPORT_ROOT`, mounted at `/imports`). Absolute paths under
`/imports` still work. Accepted packages are snapshotted under managed
`sources/`; builders never receive the import directory.

`remove <package>` works for both AUR and local packages. It cancels related
jobs, removes published packages from the pacman repository, deletes source
snapshots, and drops the managed package from the database.

## Update Behavior

Aurforge tracks the exact AUR Git commit. Builds wait until that commit is at
least `AURFORGE_UPDATE_DELAY` old (default 12 hours), measured from the commit
timestamp—not from when Aurforge first noticed it. Already-old commits can build
immediately. If a newer AUR revision appears before an eligible job runs, the
older job is skipped so a reverted or replaced PKGBUILD is never built.

Local packages update only through the explicit `aurforge update --local` flow,
which lets you inspect the resulting package metadata before it is queued.
Confirmed local imports are eligible to build immediately.

Normal `yay` updates only download packages already published by Aurforge; they
do not compile packages on clients.

## Resource And Isolation Policy

The worker starts one build at a time by default. Every job gets a new disposable
Arch container with CPU, memory, PID, and timeout limits, a read-only source
snapshot mount, a shared pacman cache, a staging output directory, and no Docker
socket. The build rootfs stays writable so `makepkg --syncdeps` can install
build dependencies; isolation comes from the disposable container and the
limited mounts, not from a read-only root.

The worker is the only service with the Docker socket because it creates build
containers. The socket is host-root equivalent, so do not grant untrusted users
access to the Compose project or Docker group.

Configure `AURFORGE_BUILD_CPU_LIMIT` to about 80% of host CPUs, for example
`6.4` on an eight-core machine. `AURFORGE_BUILD_CPU_SHARES` keeps builds lower
priority while other containers are busy. Memory, PID, and timeout limits are
configured in `.env` as well.

## Notifications And Audit

Set `AURFORGE_NTFY_URL`, `AURFORGE_NTFY_TOPIC`, and optionally
`AURFORGE_NTFY_TOKEN` to receive ntfy notifications only for failures.

Aurforge records source snapshots and package metadata in PostgreSQL. Its
deterministic audit flags skipped checksums, `sudo`, Docker socket references,
and privileged-container references. Build artifacts are hashed before
publication. These checks reduce accidental risk but do not make untrusted AUR
code safe.

## Pacman Clients

Add the repository above `extra` in `/etc/pacman.conf`:

```ini
[aurforge]
SigLevel = Optional TrustAll
Server = http://<aurforge-server>:8088
```

This initial deployment targets a trusted LAN and serves unsigned packages.
Package signing can be added later without changing the import or build model.
