# mediaserver

mediaserver is the home server for the Aniko, KumoBox and Utako apps, for a
computer that stays on: a Raspberry Pi 4 or 5, or any Linux computer. One
program, one service, one install and one updater, with a side for each app:

| Side | For | Port |
|---|---|---|
| **Anime** | the Aniko apps | 43211 |
| **Movies & TV** | the KumoBox apps | 43311 |
| **Music** | the Utako apps | 43411 |

All are managed from one dashboard, `http://<your Pi>.local:43211`: the
slider at its top switches between **Anime**, **Movies**, **TV** and
**Music**.

The sides are kept apart: each has its own library folders, its own
devices (an Aniko app only ever sees the anime side, a KumoBox app only the
movies side, a Utako app only the music side), its own logins (AniList for
anime, Letterboxd for movies) and its own data
(`~/.local/share/mediaserver/anime`, `…/movies` and `…/music`). What they
share is the program, its updates, and the dashboard. Movies and TV are the
same side seen apart: their own titles, the same devices, folders and
settings. (KumoBox's own pages, at port 43311 with the same sign-in, are
there for what the dashboard doesn't do, like fixing a match.)

The anime side does what Aniko Hub did:

- **Streaming.** The devices play its episodes as they are, which costs the
  Pi almost nothing. A file a device can't play (some TVs and phones can't
  play HEVC or 10-bit video) is converted as it plays, with the Pi's video
  chip, at most 2 at a time.
- **The same place on every device.** The devices on the same AniList
  account carry on where any of them stopped, and share their list, theme
  and player settings. Each account's progress is kept apart.
- **AniList logins**, **downloads** onto the server, and the **dashboard**:
  what it's streaming to whom, its devices and logins, its library and
  disks, and the Pi's CPU, memory, temperature and network.

The movies side is a KumoBox that plays nothing: it shares its library of
movies and shows with the KumoBox apps you allow, streams to them (converting
what they can't play), and takes their downloads when you let them.

The music side is Utako's server, and the dashboard's simplest: it shares
the songs in its folders (their tags, covers and lengths) with the Utako apps
you let in, streams them as they are, keeps the settings and logins one of
them sends (sealed, for the others you let in), and takes the songs they
upload when you let them. Its Music pages show who's listening to what, the
devices, and the library's folders and songs.

## What you need

- A Raspberry Pi 4 or 5 (2 GB is plenty) with **Raspberry Pi OS Lite,
  64-bit** (32-bit works too), or another computer with Linux.
- The libraries on a USB drive (USB 3, the blue ports) or the SD card. Mount
  the drive at the same place every boot, for example `/mnt/media` with a
  line in `/etc/fstab`; the desktop version of Raspberry Pi OS mounts it
  under `/media/<you>/` by itself.
- A wired network connection gives the steadiest streams.

## Install

On the Pi, as your usual user (not root):

```bash
curl -fsSL https://raw.githubusercontent.com/v0-0x/mediaserver/HEAD/packaging/install.sh | bash
```

Add `-s -- --beta` after `bash` for the newest beta. The script installs
mediaserver for your Pi's processor into `~/.local/bin/mediaserver`, ffmpeg
and ani-cli, and a service that starts it with the Pi. At the end it prints
the dashboard's address and a **setup code**.

Or download `mediaserver-<version>-linux-<arm64|arm|amd64>.tar.gz` from the
newest release, unpack it and run its `./install.sh`.

Options: `--beta`, `--no-ffmpeg`, `--no-ani-cli`, `--uninstall` (keeps the
data), `--purge` (removes the data too).

### Coming from Aniko Hub

Aniko Hub's last update installs mediaserver in its place, by itself: its
dashboard's **Settings › Updates** offers it. Everything stays: the library,
the AniList logins, the devices and their pairing, the dashboard's password
and the address (port 43211). Its data moves to
`~/.local/share/mediaserver/anime` (a link is left where it was).

It then still runs under Aniko Hub's service (`aniko-hub`). Run the install
command above once to give it its own (`mediaserver`): it replaces Aniko
Hub's.

## Set it up

1. Open the dashboard from a browser on the network:
   `http://raspberrypi.local:43211` (your Pi's name, or its address).
2. Type the setup code and choose the dashboard's password. (On the Pi,
   `~/.local/bin/mediaserver --setup-code` shows the code again.)

**Anime (the Aniko apps):**

3. **Library › Add a folder**: the folder your anime are in.
4. On each computer, TV or phone with Aniko, turn on **Settings › Other
   devices**. The device shows up on the dashboard under **Devices**: turn
   on **Share the library** for it.
5. On one device logged in to AniList, in **Settings › Other devices**, on
   the server, press **Send**: it takes the AniList login, theme and
   settings, and every position watched, so the other devices start where
   it is. For a TV that isn't logged in, turn on **Give it an AniList
   login** for it on the dashboard.

**Movies & TV (the KumoBox apps):**

6. Slide to **Movies** (or **TV**) at the top of the dashboard. In
   **Settings**, add a TMDB key (free, from themoviedb.org: movies and shows
   are matched and get their posters with it); in **Library**, add the
   folders your movies and shows are in. A movies folder added to the anime
   side by mistake has **Move to Movies & TV** next to it there.
7. On each computer, TV or phone with KumoBox, turn on **Settings › Other
   devices › Library sharing**. The device shows up in **Movies › Devices**:
   turn on **Share the library** for it (and **Let it download here** to let
   it download and upload onto the server).

**Music (the Utako apps):**

8. Slide to **Music**. In **Library › Add a folder**, add the folder your
   music is in (songs keep their tags; a `cover.jpg` by them is their
   cover; their lengths need ffmpeg, which the install puts there).
9. In Utako on each computer or phone, turn on **Settings › Media server ›
   Use a media server**. The device shows up in **Music › Devices**: turn
   on **Share the music** for it (and **Let it upload here** to let it
   upload songs). Utako then has a playlist named after the server, with
   its covers.
10. To have the same settings and logins on another device (an iPhone,
    say): on the one that has them, **Send my settings and logins**; on
    the other, **Get settings and logins from it**. The Music overview
    shows what the server keeps, and can forget it.

## Uploading files

From the computer you're on, to the server's library, files or whole
folders (they keep their names):

- **In a browser:** the dashboard's **Library › Upload**, under Anime,
  Movies, TV or Music.
- **From the Aniko and KumoBox apps:** **Local library › Upload**, or
  **Upload** by the server in **Settings › Other devices**. The server lets
  that device download there first (the dashboard's **Devices** for Aniko,
  the movies side's **Settings › Other devices** for KumoBox: **Let it
  download here**).
- **From Utako:** **Settings › Media server › Upload a folder…** or
  **Upload a song…**, once **Music › Devices** lets it (**Let it upload
  here**).

They go a piece at a time: an upload cut short carries on where it stopped
when it's sent again. A file is never written over another one, and the
library picks the new files up by itself.

## Updates

mediaserver looks for new versions by itself; the dashboard's **Settings ›
Updates** installs one (it restarts in a few seconds), for both sides at
once. **Beta updates** there offers the betas too.

## When something's wrong

- **The log:** `journalctl -u mediaserver -f` (`-u aniko-hub` while it still
  runs under Aniko Hub's service). The dashboard's and the movies side's
  Logs pages show it too.
- **Restart it:** `sudo systemctl restart mediaserver`
- **The dashboard doesn't open:** check the service runs
  (`systemctl status mediaserver`) and use the Pi's address instead of its
  name.
- **KumoBox's own pages (port 43311) send you to the dashboard:** sign in
  there first, with the same address (name or IP).
- **A device doesn't show up:** some routers keep devices from finding each
  other. In the device's settings, **Add by address** with the Pi's address
  and port 43211 (Aniko) or 43311 (KumoBox); in Utako, type the Pi's
  address in **Settings › Media server › Address** (it adds port 43411
  itself).
- **Episodes or movies don't show:** the folder must be readable by the user
  mediaserver runs as; the library's **Scan** looks again.
- **Forgot the dashboard's password:** on the Pi,

  ```bash
  sudo systemctl stop mediaserver
  ~/.local/bin/mediaserver --reset-password
  sudo systemctl start mediaserver
  ```

  then open the dashboard and set a new one with the setup code it prints.
