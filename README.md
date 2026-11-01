# mediaserver

One home server for all of v0-0x's apps, for a Raspberry Pi or any Linux
computer that stays on. It is one program with a side for each app:

| Side | For | Port | Code | From |
|---|---|---|---|---|
| **Anime** | the Aniko apps | 43211 (discovery 43212/udp) | `anime/` | Aniko's server, as Aniko Hub |
| **Movies & TV** | the KumoBox apps | 43311 (discovery 43312/udp) | `movies/` | KumoBox's server |

The sides are separate: each has its own library, devices, logins and data
folder (`~/.local/share/mediaserver/anime` and `…/movies`), and each app
only finds and talks to its own side. They share the program, its service,
its updates (one release for both) and the dashboard's password.

**Using it:** [packaging/README.md](packaging/README.md) (install, moving
from Aniko Hub, setting up each side, updates, troubleshooting).

## How it fits together

```
cmd/mediaserver      the program: starts both sides, one log, data folders,
                     moves Aniko Hub's data, restarts after an update
anime/engine         starts the anime side (Aniko's app as a hub: no player,
                     the dashboard, mediaserver's updater)
anime/internal/...   Aniko's server packages
movies/engine        starts the movies side (KumoBox's app: no player,
                     sharing always on, updated with mediaserver)
movies/internal/...  KumoBox's server packages
web/anime            Aniko's web UI: on a server it shows the dashboard
web/movies           KumoBox's web UI: the movies side's pages
packaging/           install.sh, the systemd service, the users' README
```

- **Connecting.** The anime side answers the Aniko apps exactly as Aniko Hub
  did (same port, same identity and pairings once its data is moved), and
  tells them it's Aniko `anime/engine.ProtocolVersion`. The movies side is a
  KumoBox to the KumoBox apps (it says it runs on `mediaserver`, so they
  don't offer to share with it or to update it), version
  `movies/engine.ProtocolVersion`.
- **One dashboard.** The anime side's web UI (`web/anime`, `pages/hub`)
  is mediaserver's dashboard, with a slider: Anime, Movies, TV. The Movies
  and TV pages (`pages/hub/media`) ask the movies side through the anime
  side, at `/api/movies/...` (`anime/internal/api/movies.go`), passed on in
  the program as the movies side's own computer's, for the signed-in owner
  only.
- **Signing in.** The dashboard's sign-in (the anime side's `kumo_session`
  cookie) also opens KumoBox's own pages on port 43311: cookies go to every
  port of a host, and the movies side asks the anime side whether a request
  has it (`movies/internal/api`: `Server.Owner`). Without it, its pages send
  the browser to the dashboard; the KumoBox apps' own requests
  (`/api/peer/`) are checked by their keys, as always.
- **Updates.** The anime side's updater looks for `server-v<version>`
  releases of this repository, downloads
  `mediaserver-<version>-linux-<arch>.tar.gz`, swaps the program and the
  process starts itself again. The movies side's updater is off
  (`update.Checker.Managed`).
- **Uploads.** Both sides take files and folders a piece at a time
  (`*/internal/upload`): from their own pages (`POST /api/upload`, for the
  signed-in browser) and from the apps they let download there
  (`/api/peer/upload`, signed like every request between them).

## Building

```bash
make            # both sides' pages, then ./dist/mediaserver
make test       # go test ./...
make run        # runs it with its data in ./dev-data
```

Go (see `go.mod`) and Node.js are needed. The program is static
(`CGO_ENABLED=0`), so `GOOS=linux GOARCH=arm64 make server` builds the Pi's.

## Releases

`.github/workflows/release.yml` tests every push, and on the default branch
publishes the GitHub Release `server-v1.0.<commits>`: a beta, unless the
commit's message says `[stable]`.

## Bringing in a newer Aniko or KumoBox

`movies/` is a copy of KumoBox's `server/internal` (imports renamed to
`github.com/v0-0x/mediaserver/movies/...`), and `web/movies` of its `web/`:
KumoBox has every change made here, so a newer KumoBox is copied over as it
is (keeping `update_test.go`'s `TestVersionOf`, which reads the app's
desktop/package.json), then `movies/engine.ProtocolVersion` is raised to its
version.

`anime/` is Aniko's `server/internal` from 2.0.42, with the hub (the server
side Aniko itself no longer has since 2.0.44), and `web/anime` its `web/`
with the dashboard. A newer Aniko's changes are brought in by hand: the
device side and what the apps send the server must stay as they expect.
Run `make test` after either.
