HOW TO RUN SXOPS ON WINDOWS (through WSL)
==========================================

sxops is a Linux program, so on Windows it runs inside Windows' own
built-in Linux layer, called WSL2. You set this up once (about 10
minutes, mostly downloading); after that sxops works like a normal app.

Just copy-paste the commands, one at a time, and press Enter after each.


STEP 1 - Install the Linux layer (one time)
--------------------------------------------

1. Open the Start menu, type:  terminal
2. Right-click "Terminal" and choose "Run as administrator".
3. Copy-paste this command and press Enter:

       wsl --install -d Ubuntu

4. If Windows says it needs to restart: restart the PC and continue
   with step 5. You do NOT need to repeat the command.
5. Open the "Ubuntu" app from the Start menu. Pick any Linux username
   and password (the password is only used inside Ubuntu).

   From now on, everything happens inside this Ubuntu window.


STEP 2 - Add the support sxops needs (one time)
------------------------------------------------

In the Ubuntu window, copy-paste (password = the one from step 1; you
won't see it while typing, that's normal):

    sudo apt update && sudo apt install -y libgtk-3-0 libgl1 tor


STEP 3 - Put sxops into Ubuntu (one time)
------------------------------------------

If you downloaded "sxops-linux" with your browser, it is in your
Windows Downloads folder. This copies it into Ubuntu (replace YourName
with your Windows user name; Tab completes the names):

    cp /mnt/c/Users/YourName/Downloads/sxops-linux ~/
    chmod +x ~/sxops-linux


STEP 4 - Start sxops
---------------------

    ~/sxops-linux

The first start unpacks the program for your CPU, then the sxops window
opens on your Windows desktop. Press its "Set up my SimpleX server"
button once and follow along - done. You can close the Ubuntu window;
the servers keep running in the background.


EVERY DAY AFTER THAT
--------------------

Just open the "Ubuntu" app from the Start menu and type:

    ~/sxops-linux


IF SOMETHING GOES WRONG
-----------------------

The sxops window does not appear, or an error about "display" / "WSLg":
    1. Open Terminal as administrator again, run:  wsl --update
    2. Restart the PC, then try  ~/sxops-linux  again.
    An error mentioning libgtk / libGL / "shared library":
    re-run the STEP 2 command inside Ubuntu, then try again.

"The Linux system could not start (error 0x80370102)":
    Restart the PC; if it persists, enable "Intel VT-x / AMD-V"
    (Virtualization Technology) in the BIOS/UEFI during boot.

"Tor not installed / not connected":
    Re-run the STEP 2 command, then press "Connect" on the Tor page in
    sxops. The first connection can take a few minutes.

Tor never connects (often after the PC slept):
    The clock inside Linux drifted. Fix:
        1. In any Windows terminal:  wsl --shutdown
        2. Open the Ubuntu app again
        3. Start sxops and press "Retry connection" on the Tor page
    (alternative, inside Ubuntu:  sudo hwclock -s)

Tor cannot connect at all (network blocks Tor):
    1. In a browser, open https://bridges.torproject.org and select
       "Custom Bridges" - copy the lines it shows.
    2. Paste them into the bridges box on the sxops Tor page (one per
       line) and press "Apply and restart Tor".
    obfs4 bridges additionally need, in Ubuntu:
        sudo apt install obfs4proxy

Tor stalls at 25 percent after applying bridges:
    The bridge lines themselves are not connecting - they are offline,
    outdated or blocked. sxops checks every bridge address before
    applying and aborts a reconnect after about two minutes of failed
    bridge attempts with a clear message. Get FRESH lines from
    https://bridges.torproject.org (old ones expire) and apply again.

The SimpleX app cannot connect to my server:
    The app needs Tor switched on - it has no built-in "Use Tor"
    switch. Phones: install Orbot (F-Droid / Play Store / App Store)
    and press Start in it - that is all. PCs (Windows / Linux /
    macOS): that PC needs its own Tor, and the app's SOCKS proxy
    (app settings: Network & servers -> SOCKS proxy) points at it -
    easiest is the Tor Browser, kept open, proxy 127.0.0.1 port
    9150. On Windows the Tor Expert Bundle (torproject.org -
    unzip and run tor.exe) is a lighter alternative, proxy port
    9050. The port for sxops' own Tor is shown on the sxops
    Dashboard ("How to connect" card). Also wait for the green
    "reachable" check in sxops. If it still fails, run
    ~/sxops-linux check  in Ubuntu: when that passes, the server is
    fine and Orbot / the app's proxy settings are the fix.

Updating the server programs:
    sxops -> Updates (left menu) -> "Update all". Data, keys and
    addresses are kept. Terminal alternative:  ~/sxops-linux update

Stopping everything completely:
    Close sxops, then in any Windows terminal:  wsl --shutdown

Your data folder (keys, database, addresses), from Windows Explorer:
    \\wsl.localhost\Ubuntu\home\yourname\.local\share\sxops
    (replace "yourname" with your Ubuntu user name). Copying that folder
    to a safe place = a full backup.

Removing sxops from Windows:
    Close sxops, then in any Windows terminal:
        wsl --unregister Ubuntu
    (or Settings -> Apps -> Ubuntu -> Uninstall).

More help: README.md (came in the same download as this file).
