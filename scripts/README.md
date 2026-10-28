# sxops build scripts

The exact scripts used to build the release deliverables. They reference
the release build environment via absolute paths
(`T=/home/z/my-project/tools`, `SRC=/home/z/my-project/src/sxops`) —
change those at the top to match your machine.

| Script | Purpose |
|--------|---------|
| `setup-host-toolchain.sh` | First-time environment (amd64): Go SDK + GTK/GL/X11/Wayland dev+runtime libraries for CGO, all in `tools/` without root |
| `setup-aarch64-toolchain.sh` + `make-aarch64-cc-wrapper.sh` | Cross toolchain + arm64 GTK/X11 libraries for `linux/arm64` |
| `find-missing-arm64-libs.sh` | DT_NEEDED closure scanner used while assembling the arm64 prefix |
| `build-all.sh` | Builds both Linux targets: `linux-amd64`, `linux-arm64` |
| `make-universal-linux.sh` | Packs amd64+arm64 into the single self-extracting `sxops-linux` file |
| `package-deliverables.sh` | Assembles the download folder: universal launcher, `sxops-src.zip`, checksums |

Quick start on a normal Linux box (amd64 only):

    sudo apt install golang gcc libgtk-3-dev libgl1-mesa-dev libglx-mesa-dev
    go build .

For the arm64 target, read `build-all.sh` — it needs the aarch64 cross
toolchain (see the setup scripts above). sxops is a native Linux program
(amd64 and arm64); on Windows it runs unchanged inside WSL2.
