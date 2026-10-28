# sxops — your own SimpleX Chat server, in one click

sxops is a small program with a friendly window that sets up and runs
**your own SimpleX Chat servers** on your PC — reachable from anywhere
through the Tor network, with no port forwarding, no domain and no
cloud bills. Your messages travel through your own machine instead of
servers operated by anyone else.

**One download for everyone** — a single file, `sxops-linux`, that runs
on Linux (Ubuntu, Debian, Mint, Fedora… x86-64 and ARM64, including
Raspberry Pi 4/5; the first start unpacks the right version for your
CPU) and on Windows 10/11 through Windows' own built-in Linux layer
(WSL2, one-time setup — see *Install on Windows*; the printable version
is **README-WSL.txt** next to this file).

## What sxops does

- Sets up the official SimpleX servers for you: SMP (chats), XFTP
  (files), an optional chat relay (channels) and an optional
  notification server — one button, no configuration to write.
- Publishes `.onion` addresses through Tor — works behind any router,
  no public IP needed. Nothing starts behind your back; you press Start.
- Verifies the servers are reachable (live checks on the Dashboard,
  `sxops check` runs a full protocol test).
- Updates the servers with one click (official releases from GitHub;
  data, keys and addresses stay the same).
- Gives you admin tools: statistics, connected clients, and the full
  official moderation — adaptive ban dialog, unban, ban list,
  registrations gate.
- Supports censored networks: Tor bridges (incl. obfs4) can be pasted
  into the Tor page.
- Keeps everything in one folder — copying that folder is a full backup.

## Install on Linux

1. Install Tor once: `sudo apt install tor`
2. Make the file executable and start it:

   ```
   chmod +x sxops-linux
   ./sxops-linux
   ```

## Install on Windows

sxops runs inside Windows' built-in Linux layer (WSL2 + Ubuntu). One-time
setup, about 10 minutes:

1. Open **Terminal** as administrator (Start menu → type *terminal* →
   right-click → *Run as administrator*) and run:

   ```
   wsl --install -d Ubuntu
   ```

   Restart if Windows asks, then open the **Ubuntu** app once and pick
   any username and password.

2. In the Ubuntu terminal, install the support packages (sxops's window
   + Tor):

   ```
   sudo apt update && sudo apt install -y libgtk-3-0 libgl1 tor
   ```

3. Copy the download into Ubuntu (replace `YourName` with your Windows
   user name; Tab completes the names):

   ```
   cp /mnt/c/Users/YourName/Downloads/sxops-linux ~/
   chmod +x ~/sxops-linux
   ```

4. Start it: `~/sxops-linux` — the sxops window opens on your Windows
   desktop.

**Requirements:** Windows 10 21H2 or newer, or Windows 11. Everyday use:
open the Ubuntu app and run `~/sxops-linux`; the servers keep running in
the background when the window is closed. To stop everything completely:
close sxops and run `wsl --shutdown` in any terminal.

## First run — one button

Press **"Set up my SimpleX server"**. sxops downloads the official
server programs, creates all keys, configuration and addresses,
connects to Tor, publishes your servers and starts everything — with
live progress and plain-word help whenever something needs attention
(for example when Tor is missing). Optional settings are folded away:
a relay display name and a password for new registrations.

## Connect your SimpleX app

1. Install the **SimpleX Chat** app (free, from **Google Play** on
   Android, the **App Store** on iPhone / iPad, or
   <https://simplex.chat> for any device — computers included).
2. In sxops open **Dashboard** and press **Copy** next to the `smp://`
   address (or press **QR** and scan it with the app). Wait for the
   line below it to show **✓ reachable through the Tor network**.
3. In the app: **Settings → SimpleX servers → paste the addresses**
   — `smp://` (your chat server, carries the messages) and `xftp://`
   (your file server, carries pictures and file transfers). The app
   asks to use each server you add — confirm, and new chats and file
   transfers run through your server. The chat relay (optional — hosts
   groups and channels on your server) is an **https link**: paste it
   under *Settings → Network & servers → Chat relay*. Desktop apps can
   also add the `ntf://` address there under *Notification servers*
   (push notifications while the app is closed).
4. Turn on Tor **for the app** — the app has no built-in Tor switch:
   on Android/iOS install **Orbot** (F-Droid / Play Store / App Store)
   and press Start in it — that is all, the app connects through it.
   On a Windows / Linux / macOS PC that PC needs its own Tor, and the
   app's **SOCKS proxy** setting points at it (*Settings → Network &
   servers*): easiest is the **Tor Browser**, kept open, with the proxy
   set to `127.0.0.1:9150`. On Windows the **Tor Expert Bundle**
   (torproject.org — unzip and run `tor.exe`) is a lighter alternative
   that runs Tor in the background, proxy `127.0.0.1:9050`. On the very
   machine running sxops you can instead use sxops' own Tor — its port
   is shown live on the Dashboard's *How to connect* card (usually
   `9050`, but not always — sxops moves Tor's ports when they are taken).

Your contacts don't need to change anything — the server's address
travels inside every link you share. Anyone can also paste your `smp://`
address in their app under *Settings → SimpleX servers* to use your
server deliberately; protecting it with a password (Settings) stops
strangers from doing so uninvited.

## Links in a browser

SimpleX links are meant to be opened in the app, but pasting one into a
browser shows a page too: your server serves the official SimpleX invite
page (with a QR code) for `https://…` links. For `.onion` servers open
them in Tor Browser; the certificate is self-signed, so the browser asks
once to accept it. On a public host, forward your router's port 443 to
port 8443 of the sxops machine to get the same page for your domain.

**Tor-free (clearnet) access** needs a public IP: forward ports 5223
(SMP) and 5225 (XFTP) on your router, give the address a stable name
(dynamic DNS, e.g. DuckDNS — no CGNAT), and enter that name under
**Settings → public host**. Or run sxops on a small VPS with a domain.
Tor stays the always-works fallback either way.

## Everyday use

| Page | What it's for |
|------|---------------|
| **Dashboard** | See what's running, start/stop everything, copy addresses, live statistics |
| **Admin tools** | Live stats, connected clients, moderation: queues, files, bans |
| **Logs** | Look at or save the log files (useful when asking for help) |
| **Tor network** | Tor status, connect/retry, **Tor bridges**, your `.onion` addresses |
| **Updates** | Keep SimpleX up to date with one click |
| **Settings** | Name/password, file storage, notification server, data folder |

**Banning abusive clients** (Admin tools): every chat queue and file on
your server is listed with a **Ban** button and the time it was first
seen. The ban dialog adapts to what you paste: a **user** (a queue ID)
gets the restrictions on that user — block sending/receiving, the ban
notice, or permanent deletion — while a **link** (an `https://` or
`simplex:/` invitation, contact, group or channel link straight from an
abuse report, or a queue the list marks "link") gets its own dialog:
**block the link** with the reason new joins will see (no expiry), or
**delete the queue behind it**, or both. Nothing is ticked by default
and the confirm button stays disabled until you tick at least one
option; a live summary shows what will happen. **Unban** is one click,
the ban list is read from the server's own record, and the server-wide
new-registrations gate has its own card.

Abuse reports reach you as ordinary chats: configure your SimpleX
address in the **Abuse reports** card and the server publishes it on
its info page.

## Troubleshooting

- **Windows: "Windows needs to restart" during setup** — normal for a
  first WSL2 install; continue with the next step afterwards.
- **Windows: "The Linux system could not start (error 0x80370102)"** —
  restart the PC; if it persists, enable virtualization (*Intel VT-x /
  AMD-V*) in the BIOS/UEFI.
- **Windows: the sxops window does not appear** — run `wsl --update` in
  an administrator terminal and restart the PC; if a library error was
  printed in Ubuntu instead, re-run the step 2 install command.
- **"Tor not installed / not connected"** — install it (`sudo apt
  install tor`, inside Ubuntu on Windows) and press **Connect** on the
  Tor page. The first connection can take a few minutes.
- **Tor never connects after the PC slept (Windows/WSL)** — the Linux
  clock drifted: run `wsl --shutdown` in a Windows terminal, open
  Ubuntu again, then **Retry connection** (or `sudo hwclock -s`).
- **Tor cannot connect at all** — the network may block Tor: paste
  bridge lines from <https://bridges.torproject.org> (select *Custom
  Bridges*) into the Tor page and apply. `obfs4` bridges additionally
  need `sudo apt install obfs4proxy`. Applying bridges restarts a
  running Tor and refreshes its cached network data through them (so a
  stuck "asking for networkstatus consensus" after a bridge change
  heals on its own); with Tor off it only saves the lines for the next
  **Connect**.
- **Tor stalls at 25% ("asking for networkstatus consensus") after
  applying bridges** — the bridge lines themselves are not connecting
  (offline, outdated, or blocked). During the reconnect sxops aborts
  after about two minutes of failed bridge attempts and says so
  instead of sitting at 25% for five minutes, and the **Stop** button
  next to *Connecting…* ends a stalled attempt immediately. Get
  *fresh* lines from
  <https://bridges.torproject.org> — bridge lines expire — and apply
  them again.
- **Addresses show "not ready yet"** — they appear once Tor connects;
  addresses may take a few minutes to spread on the Tor network before
  they answer. The Dashboard keeps testing.
- **"Start all" did not start the servers** — servers start only once
  Tor is running: connect Tor, then press Start all again.
- **The app's server test fails at step "Connect"** — the app cannot
  reach the server: Orbot/Tor is not running for the app, or the SOCKS
  port is wrong. Run `sxops check` — when it passes, the server side is
  good and the fix is in the app's proxy settings.
- **A port is already in use** — sxops moves its server to a free local
  port automatically; the address your contacts use stays the same.
- **The chat relay stopped by itself** — it only runs locked to your own
  SMP server. Fix the SMP side (Tor connected, server installed and
  running), then press Start on the relay row again.
- **The notification server does not start** — it needs the
  `postgresql` package (`sudo apt install postgresql`); press Start
  again.
- **Something else** — open **Logs**, pick the component, **Save log to
  a file** and share it; `sxops doctor` checks the environment.

## Where things live / privacy

- All files stay in one data folder (**Settings → Data folder**):
  `~/.local/share/sxops` on Linux (the unpacked launcher puts the
  program itself in `~/.cache/sxops`). On Windows the same folder,
  inside WSL2: paste `\\wsl.localhost\Ubuntu\home\yourname\.local\share\sxops`
  into Explorer's address bar.
- Nothing is sent anywhere except downloads from GitHub (official
  SimpleX releases) and the Tor network traffic itself.
- The servers bind to `127.0.0.1` only; from outside your machine they
  are reachable exclusively through Tor (plus the public host you may
  configure yourself).
- Uninstalling = close sxops and delete the data folder. On Windows you
  can additionally remove the Linux layer: `wsl --unregister Ubuntu`.

## Terminal commands (optional)

sxops also works without the window — handy for scripts or machines
without a screen. On Windows, run them inside the Ubuntu terminal, e.g.
`~/sxops-linux status`.

```
sxops                 # open the GUI (default)
sxops setup           # guided setup without GUI
sxops start | stop    # start / stop everything (stop --tor also stops Tor)
sxops update [--check]  # update components (--check only reports)
sxops status          # text status of all components + Tor reachability
sxops addresses       # print the server addresses
sxops check           # full verification: Tor reachability + protocol test
sxops doctor          # environment diagnostics
sxops daemon          # run as a background supervisor (manual)
sxops version
```

## Building from source

sxops is written in Go with the Fyne GUI toolkit and builds as a native
Linux program (on Windows the same build runs inside WSL2):

```
go build .                        # Linux (needs X11/GL dev headers)
GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build .   # ARM64
```

The source zip includes the `scripts/` folder with the exact build and
packaging scripts used for the release. Versions used at release time:
simplexmq **v6.5.0** servers, simplex-chat CLI **v7.0.3** (chat relay),
Fyne v2.8.2, Go 1.27. The full source is in `sxops-src.zip` next to this
file — the same code that produced the binaries.

*sxops is an independent convenience tool; the SimpleX servers it manages
are official, unmodified binaries published by the
[SimpleX project](https://github.com/simplex-chat).*
